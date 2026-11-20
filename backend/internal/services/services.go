// Package services implements business logic for each simulated cloud service.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
)

// Publisher pushes real-time events to websocket subscribers of an account.
type Publisher interface {
	Publish(account string, msg any)
}

// LogSink receives platform-generated log lines (CloudWatch Logs implements it).
type LogSink interface {
	Append(ctx context.Context, account, group, stream string, lines []string)
}

type nopSink struct{}

func (nopSink) Append(context.Context, string, string, string, []string) {}

type nopPub struct{}

func (nopPub) Publish(string, any) {}

// Deps are shared by all services.
type Deps struct {
	Store  *repositories.Store
	Engine *simulator.Client
	Log    *slog.Logger
	Pub    Publisher
	Logs   LogSink

	seqMu sync.Mutex
}

func NewDeps(store *repositories.Store, eng *simulator.Client, log *slog.Logger) *Deps {
	return &Deps{Store: store, Engine: eng, Log: log, Pub: nopPub{}, Logs: nopSink{}}
}

// RecordEvent persists a platform event and pushes it to subscribers.
func (d *Deps) RecordEvent(ctx context.Context, account, resourceID, resourceType, typ, message string, detail any) {
	var raw json.RawMessage
	if detail != nil {
		raw, _ = json.Marshal(detail)
	}
	e := &repositories.Event{AccountID: account, ResourceID: resourceID, ResourceType: resourceType, Type: typ, Message: message, Detail: raw}
	if err := d.Store.Events.Insert(ctx, e); err != nil {
		d.Log.Error("record event", "err", err)
		return
	}
	d.Pub.Publish(account, map[string]any{"type": "event", "event": e})
}

// NextSeq returns a platform-wide monotonically increasing counter stored in kv_state.
func (d *Deps) NextSeq(ctx context.Context, name string) (uint64, error) {
	d.seqMu.Lock()
	defer d.seqMu.Unlock()
	v, err := d.Store.KV.Get(ctx, "seq:"+name)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return 0, err
	}
	n, _ := strconv.ParseUint(v, 10, 64)
	n++
	return n, d.Store.KV.Set(ctx, "seq:"+name, strconv.FormatUint(n, 10))
}

// EngineErr maps engine errors to API errors.
func EngineErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, simulator.ErrUnavailable) {
		return apierr.New(503, "EngineUnavailable", "the simulation engine is unavailable; try again shortly")
	}
	var e *simulator.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_transition":
			return apierr.InvalidState("%s", e.Message)
		case "dependency_violation":
			return apierr.Dependency("%s", e.Message)
		case "quota_exceeded":
			return apierr.Quota("%s", e.Message)
		case "not_found":
			return apierr.NotFound("%s", e.Message)
		case "invalid_request":
			return apierr.Validation("%s", e.Message)
		}
		return apierr.New(502, "EngineError", "%s", e.Message)
	}
	return err
}

// RepoErr maps repository errors.
func RepoErr(err error, what string) error {
	if errors.Is(err, repositories.ErrNotFound) {
		return apierr.NotFound("%s not found", what)
	}
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint") {
		return apierr.Conflict("%s already exists", what)
	}
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint") {
		return apierr.Dependency("%s is still referenced by other resources", what)
	}
	return err
}

var nameTagRe = func(s string) bool { return len(s) <= 255 }

// ValidateTags enforces tag key/value limits.
func ValidateTags(tags map[string]string) error {
	if len(tags) > 50 {
		return apierr.Validation("a resource can have at most 50 tags")
	}
	for k, v := range tags {
		if k == "" || len(k) > 128 || !nameTagRe(v) {
			return apierr.Validation("tag keys must be 1-128 chars and values at most 255 chars")
		}
		if strings.HasPrefix(strings.ToLower(k), "aws:") {
			return apierr.Validation("tag keys starting with 'aws:' are reserved")
		}
	}
	return nil
}

func strPtr(s string) *string { return &s }
