// Package orchestration runs background control loops: the engine event pump and the
// reconciler that keeps the engine's runtime state consistent with SQLite (the source of truth).
package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

const seqKey = "engine:event_seq"

// EventPump long-polls engine events and dispatches them to per-kind handlers.
type EventPump struct {
	Engine   *simulator.Client
	KV       repositories.KVRepo
	Log      *slog.Logger
	Handlers map[string]func(ctx context.Context, ev simulator.Event)
	// Resync is invoked when the engine restarted or events were missed.
	Resync func(ctx context.Context)
}

func (p *EventPump) Run(ctx context.Context) {
	after := int64(0)
	if v, err := p.KV.Get(ctx, seqKey); err == nil {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	backoff := time.Second
	first := true
	for ctx.Err() == nil {
		page, err := p.Engine.Events(ctx, after, 500, 25*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, simulator.ErrUnavailable) {
				p.Log.Warn("engine unavailable; retrying", "in", backoff.String())
			} else {
				p.Log.Error("engine events", "err", err)
			}
			sleep(ctx, backoff)
			backoff = min(backoff*2, 15*time.Second)
			first = true // after an outage, re-sync once connectivity returns
			continue
		}
		backoff = time.Second
		// Engine restarted (sequence reset) or we fell behind the ring buffer: rebuild engine state.
		restarted := page.Next < after || (page.OldestAvailable > 0 && after < page.OldestAvailable-1)
		if first || restarted {
			if restarted {
				p.Log.Warn("engine event stream reset; resynchronizing", "after", after, "next", page.Next, "oldest", page.OldestAvailable)
				if page.Next < after {
					after = 0
					page.Events = nil
				}
			}
			if p.Resync != nil {
				p.Resync(ctx)
			}
			first = false
		}
		for _, ev := range page.Events {
			if h := p.Handlers[ev.Kind]; h != nil && (ev.Type == "state_changed" || ev.Type == "fault_triggered") {
				h(ctx, ev)
			}
			after = ev.Seq
		}
		if len(page.Events) > 0 {
			_ = p.KV.Set(ctx, seqKey, strconv.FormatInt(after, 10))
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Every runs fn at the given interval until ctx is cancelled (first run after one interval).
func Every(ctx context.Context, d time.Duration, fn func(context.Context)) {
	if d <= 0 {
		return
	}
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}
