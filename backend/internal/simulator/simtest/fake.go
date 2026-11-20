// Package simtest provides an in-process test double of the engine protocol (v1) with
// instantaneous transitions (equivalent to ENGINE_TIME_SCALE=0). It is used only by Go tests;
// full-fidelity behaviour is covered by the Rust engine's own tests and the system tests.
package simtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

type res struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Owner      string         `json:"owner"`
	State      string         `json:"state"`
	DependsOn  []string       `json:"depends_on"`
	Dependents []string       `json:"dependents"`
	Attributes map[string]any `json:"attributes"`
	Version    int64          `json:"version"`
}

type event struct {
	Seq        int64  `json:"seq"`
	TS         string `json:"ts"`
	ResourceID string `json:"resource_id"`
	Kind       string `json:"kind"`
	Type       string `json:"type"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason"`
	Owner      string `json:"owner"`
}

// Engine is a fake engine server.
type Engine struct {
	*httptest.Server
	Token string

	mu     sync.Mutex
	res    map[string]*res
	events []event
	seq    int64
	notify chan struct{}
	Quota  map[string]int
	Calls  map[string]int
	Reach  func(body map[string]any) map[string]any
	// Optional hooks for stateless simulation endpoints (see sim.go for the default behaviour).
	LBSimulate  func(body map[string]any) map[string]any
	LBHealth    func(body map[string]any) map[string]any
	ASGEvaluate func(body map[string]any) map[string]any
	cpu         map[string]float64 // forced CPU per instance (SetCPU)
	extraRPS    map[string]float64 // extra_load_rps per instance (LB simulation / workload)
	profile     map[string]string  // workload profile per instance
	// Lifecycle, when set, answers POST /v1/storage/lifecycle/evaluate (default: no actions).
	Lifecycle func(body map[string]any) map[string]any
}

var initial = map[string][2]string{ // kind -> {transitional initial, final}
	"instance": {"pending", "running"}, "nat_gateway": {"pending", "available"}, "load_balancer": {"provisioning", "active"},
}

func New(token string) *Engine {
	e := &Engine{Token: token, res: map[string]*res{}, notify: make(chan struct{}), Quota: map[string]int{"instance": 20}, Calls: map[string]int{}}
	e.Server = httptest.NewServer(http.HandlerFunc(e.serve))
	return e
}

func (e *Engine) emit(r *res, typ, from, to, reason string) {
	e.seq++
	e.events = append(e.events, event{Seq: e.seq, TS: time.Now().UTC().Format(time.RFC3339Nano), ResourceID: r.ID, Kind: r.Kind, Type: typ, From: from, To: to, Reason: reason, Owner: r.Owner})
	close(e.notify)
	e.notify = make(chan struct{})
}

func (e *Engine) set(r *res, to, reason string) {
	from := r.State
	r.State = to
	r.Version++
	e.emit(r, "state_changed", from, to, reason)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (e *Engine) view(r *res) *res {
	c := *r
	c.Dependents = []string{}
	for _, o := range e.res {
		for _, d := range o.DependsOn {
			if d == r.ID && !terminal(o.State) {
				c.Dependents = append(c.Dependents, o.ID)
			}
		}
	}
	return &c
}

func terminal(s string) bool { return s == "terminated" || s == "deleted" }

// Kill simulates an engine restart: all runtime state and the event stream are lost.
func (e *Engine) Kill() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.res = map[string]*res{}
	e.events = nil
	e.seq = 0
}

// Count returns registered resources of a kind.
func (e *Engine) Count(kind string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, r := range e.res {
		if r.Kind == kind && !terminal(r.State) {
			n++
		}
	}
	return n
}

func (e *Engine) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		writeJSON(w, 200, map[string]any{"status": "ok", "protocol": 1, "version": "fake"})
		return
	}
	if r.Header.Get("X-Engine-Token") != e.Token {
		writeErr(w, 401, "unauthorized", "bad token")
		return
	}
	e.mu.Lock()
	e.Calls[r.Method+" "+strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")[0]]++
	e.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	switch {
	case path == "/events":
		e.events_(w, r)
	case path == "/stats":
		e.mu.Lock()
		writeJSON(w, 200, map[string]any{"resources_total": len(e.res), "event_seq": e.seq})
		e.mu.Unlock()
	case path == "/metrics/snapshot":
		e.mu.Lock()
		samples := []map[string]any{}
		for _, x := range e.res {
			if x.Kind == "instance" && x.State == "running" {
				samples = append(samples, map[string]any{"resource_id": x.ID, "owner": x.Owner, "cpu_percent": e.cpuFor(x), "memory_percent": 40.0,
					"network_in_bytes": 1000.0, "network_out_bytes": 800.0, "disk_read_bytes": 100.0, "disk_write_bytes": 50.0})
			}
		}
		e.mu.Unlock()
		writeJSON(w, 200, map[string]any{"tick": 1, "ts": time.Now().UTC().Format(time.RFC3339), "samples": samples})
	case path == "/network/reachability":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if e.Reach != nil {
			writeJSON(w, 200, e.Reach(body))
			return
		}
		writeJSON(w, 200, map[string]any{"reachable": true, "hops": []any{}, "blocked_by": nil, "explanation": "fake engine: always reachable", "simulated_latency_ms": 0.5})
	case path == "/storage/lifecycle/evaluate":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if e.Lifecycle != nil {
			writeJSON(w, 200, e.Lifecycle(body))
			return
		}
		writeJSON(w, 200, map[string]any{"actions": []any{}})
	case strings.HasPrefix(path, "/resources/"):
		e.resource(w, r, strings.TrimPrefix(path, "/resources/"))
	case path == "/resources":
		e.mu.Lock()
		items := []*res{}
		for _, x := range e.res {
			if (r.URL.Query().Get("kind") == "" || x.Kind == r.URL.Query().Get("kind")) && (r.URL.Query().Get("owner") == "" || x.Owner == r.URL.Query().Get("owner")) {
				items = append(items, e.view(x))
			}
		}
		e.mu.Unlock()
		writeJSON(w, 200, map[string]any{"items": items})
	default:
		if out, ok := e.simulate(path, r); ok {
			writeJSON(w, 200, out)
			return
		}
		// stateless simulation endpoints: echo an empty success
		writeJSON(w, 200, map[string]any{})
	}
}

func (e *Engine) events_(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait_ms"))
	deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		e.mu.Lock()
		var out []event
		for _, ev := range e.events {
			if ev.Seq > after {
				out = append(out, ev)
			}
		}
		next, ch := e.seq, e.notify
		e.mu.Unlock()
		if len(out) > 0 || time.Now().After(deadline) {
			if out == nil {
				out = []event{}
			}
			writeJSON(w, 200, map[string]any{"events": out, "next": next, "oldest_available": 1})
			return
		}
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		case <-r.Context().Done():
			return
		}
	}
}

var actions = map[string]map[string][2]string{ // kind -> action -> {required-from ("" any), final}
	"instance": {"start": {"stopped", "running"}, "stop": {"running", "stopped"}, "reboot": {"running", "running"}, "terminate": {"", "terminated"}},
}

func (e *Engine) resource(w http.ResponseWriter, r *http.Request, rest string) {
	id, sub, _ := strings.Cut(rest, "/")
	e.mu.Lock()
	defer e.mu.Unlock()
	x := e.res[id]
	switch {
	case r.Method == http.MethodPut && sub == "":
		var in struct {
			Kind       string         `json:"kind"`
			Owner      string         `json:"owner"`
			State      *string        `json:"state"`
			DependsOn  []string       `json:"depends_on"`
			Attributes map[string]any `json:"attributes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, 400, "invalid_request", err.Error())
			return
		}
		for _, d := range in.DependsOn {
			if _, ok := e.res[d]; !ok {
				writeErr(w, 422, "invalid_request", "dependency "+d+" not registered")
				return
			}
		}
		status := 200
		if x == nil {
			if q, ok := e.Quota[in.Kind]; ok {
				n := 0
				for _, o := range e.res {
					if o.Kind == in.Kind && o.Owner == in.Owner && !terminal(o.State) {
						n++
					}
				}
				if n >= q {
					writeErr(w, 429, "quota_exceeded", "quota for "+in.Kind+" exceeded")
					return
				}
			}
			x = &res{ID: id, Kind: in.Kind, Owner: in.Owner}
			e.res[id] = x
			status = 201
		}
		x.DependsOn, x.Attributes = in.DependsOn, in.Attributes
		if in.State != nil {
			x.State = *in.State
			if p, ok := initial[x.Kind]; ok && x.State == p[0] {
				e.set(x, p[1], "resumed")
			}
		} else if status == 201 {
			if p, ok := initial[x.Kind]; ok {
				x.State = p[0]
				e.emit(x, "registered", "", p[0], "")
				e.set(x, p[1], "initial transition complete")
			} else {
				x.State = "available"
				e.emit(x, "registered", "", "available", "")
			}
		}
		writeJSON(w, status, e.view(x))
	case x == nil:
		writeErr(w, 404, "not_found", "resource "+id+" not found")
	case r.Method == http.MethodGet && sub == "":
		writeJSON(w, 200, e.view(x))
	case r.Method == http.MethodPut && sub == "workload":
		var wl map[string]any
		_ = json.NewDecoder(r.Body).Decode(&wl)
		e.recordWorkload(id, wl)
		writeJSON(w, 200, e.view(x))
	case r.Method == http.MethodPost && sub == "actions":
		var in struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Action == "delete" {
			e.set(x, "deleted", in.Reason)
			writeJSON(w, 202, e.view(x))
			return
		}
		a, ok := actions[x.Kind][in.Action]
		if !ok || terminal(x.State) || (a[0] != "" && x.State != a[0]) {
			writeErr(w, 409, "invalid_transition", "cannot "+in.Action+" "+x.Kind+" in state "+x.State)
			return
		}
		e.set(x, a[1], in.Reason)
		writeJSON(w, 202, e.view(x))
	case r.Method == http.MethodDelete && sub == "":
		var deps []string
		for _, o := range e.res {
			for _, d := range o.DependsOn {
				if d == id && !terminal(o.State) {
					deps = append(deps, o.ID)
				}
			}
		}
		if len(deps) > 0 {
			writeErr(w, 409, "dependency_violation", id+" has dependents: "+strings.Join(deps, ","))
			return
		}
		delete(e.res, id)
		w.WriteHeader(204)
	default:
		writeErr(w, 404, "not_found", "no route")
	}
}
