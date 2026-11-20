package simtest

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
)

// SetCPU forces the CPU reported for an instance in /metrics/snapshot (negative clears it).
func (e *Engine) SetCPU(id string, v float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cpu == nil {
		e.cpu = map[string]float64{}
	}
	if v < 0 {
		delete(e.cpu, id)
		return
	}
	e.cpu[id] = v
}

// ExtraLoad returns the extra_load_rps applied to an instance (LB simulation with apply_load).
func (e *Engine) ExtraLoad(id string) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.extraRPS[id]
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// cpuFor computes a deterministic CPU value (caller holds e.mu).
func (e *Engine) cpuFor(x *res) float64 {
	if v, ok := e.cpu[x.ID]; ok {
		return v
	}
	prof, _ := x.Attributes["workload"].(string)
	if p, ok := e.profile[x.ID]; ok {
		prof = p
	}
	base := 25.0
	switch prof {
	case "idle":
		base = 3
	case "stress":
		base = 95
	case "custom":
		if t, ok := x.Attributes["cpu_target"].(float64); ok {
			base = t
		}
	}
	return math.Min(100, base+e.extraRPS[x.ID]*0.5)
}

// recordWorkload stores profile / extra load from PUT /resources/{id}/workload (caller holds e.mu).
func (e *Engine) recordWorkload(id string, wl map[string]any) {
	if e.profile == nil {
		e.profile = map[string]string{}
	}
	if e.extraRPS == nil {
		e.extraRPS = map[string]float64{}
	}
	if p, ok := wl["profile"].(string); ok && p != "" {
		e.profile[id] = p
	}
	if x := e.res[id]; x != nil {
		if t, ok := wl["cpu_target"].(float64); ok {
			if x.Attributes == nil {
				x.Attributes = map[string]any{}
			}
			x.Attributes["cpu_target"] = t
		}
	}
	if v, ok := wl["extra_load_rps"].(float64); ok {
		e.extraRPS[id] = v
	}
}

// simulate implements the stateless LB / autoscaling endpoints (§7, §8).
func (e *Engine) simulate(path string, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	switch path {
	case "/lb/simulate", "/lb/health", "/autoscaling/evaluate":
		_ = json.NewDecoder(r.Body).Decode(&body)
	default:
		return nil, false
	}
	switch path {
	case "/lb/simulate":
		if e.LBSimulate != nil {
			return e.LBSimulate(body), true
		}
		return e.lbSimulate(body), true
	case "/lb/health":
		if e.LBHealth != nil {
			return e.LBHealth(body), true
		}
		return e.lbHealth(body), true
	default:
		if e.ASGEvaluate != nil {
			return e.ASGEvaluate(body), true
		}
		return asgEvaluate(body), true
	}
}

func (e *Engine) lbSimulate(body map[string]any) map[string]any {
	total := int(num(body["requests"]))
	dur := num(body["duration_s"])
	if dur <= 0 {
		dur = 60
	}
	type tgt struct {
		id     string
		weight float64
	}
	var healthy []tgt
	ts, _ := body["targets"].([]any)
	for _, t := range ts {
		m, _ := t.(map[string]any)
		if h, _ := m["healthy"].(bool); h {
			w := num(m["weight"])
			if w <= 0 {
				w = 1
			}
			id, _ := m["id"].(string)
			healthy = append(healthy, tgt{id, w})
		}
	}
	per := []map[string]any{}
	if len(healthy) == 0 {
		return map[string]any{"total": total, "served": 0, "dropped": total, "per_target": per, "avg_latency_ms": 0.0}
	}
	weighted := body["algorithm"] == "weighted_random"
	sumW := 0.0
	for _, t := range healthy {
		if weighted {
			sumW += t.weight
		} else {
			sumW++
		}
	}
	assigned := 0
	apply, _ := body["apply_load"].(bool)
	e.mu.Lock()
	if e.extraRPS == nil {
		e.extraRPS = map[string]float64{}
	}
	for i, t := range healthy {
		w := 1.0
		if weighted {
			w = t.weight
		}
		n := int(float64(total) * w / sumW)
		if i == len(healthy)-1 {
			n = total - assigned
		}
		assigned += n
		per = append(per, map[string]any{"id": t.id, "requests": n, "percent": math.Round(float64(n)*1000/float64(max(total, 1))) / 10,
			"avg_latency_ms": 12.0, "p99_latency_ms": 30.0})
		if apply {
			e.extraRPS[t.id] = float64(n) / dur
		}
	}
	e.mu.Unlock()
	return map[string]any{"total": total, "served": total, "dropped": 0, "per_target": per, "avg_latency_ms": 12.0}
}

func (e *Engine) lbHealth(body map[string]any) map[string]any {
	out := []map[string]any{}
	ts, _ := body["targets"].([]any)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range ts {
		m, _ := t.(map[string]any)
		id, _ := m["id"].(string)
		x := e.res[id]
		switch {
		case x == nil:
			out = append(out, map[string]any{"id": id, "state": "unhealthy", "reason": "Target.NotRegistered"})
		case x.State == "running":
			out = append(out, map[string]any{"id": id, "state": "healthy", "reason": ""})
		default:
			out = append(out, map[string]any{"id": id, "state": "unused", "reason": "Target.InvalidState"})
		}
	}
	return map[string]any{"targets": out}
}

func asgEvaluate(body map[string]any) map[string]any {
	g, _ := body["group"].(map[string]any)
	minS, maxS, desired := int(num(g["min"])), int(num(g["max"])), int(num(g["desired"]))
	clamp := func(n int) int { return max(minS, min(maxS, n)) }
	replace := []string{}
	running, sum := 0, 0.0
	insts, _ := body["instances"].([]any)
	for _, i := range insts {
		m, _ := i.(map[string]any)
		st, _ := m["state"].(string)
		h, _ := m["healthy"].(bool)
		id, _ := m["id"].(string)
		if st != "pending" && (st != "running" || !h) {
			replace = append(replace, id)
			continue
		}
		if st == "running" {
			running++
			sum += num(m["cpu_percent"])
		}
	}
	if num(g["seconds_since_last_activity"]) < num(g["cooldown_s"]) {
		d := clamp(desired)
		return map[string]any{"desired": d, "change": d - desired, "reason": "Within cooldown period; no scaling", "replace": replace}
	}
	if running == 0 {
		d := clamp(desired)
		return map[string]any{"desired": d, "change": d - desired, "reason": "No running instances with metrics", "replace": replace}
	}
	avg := sum / float64(running)
	best, reason := desired, "No policy triggered"
	pols, _ := body["policies"].([]any)
	for _, p := range pols {
		m, _ := p.(map[string]any)
		name, _ := m["name"].(string)
		cand := desired
		switch m["type"] {
		case "target_tracking":
			t := num(m["target_value"])
			if t > 0 && (avg > t || avg < 0.8*t) {
				cand = int(math.Ceil(float64(running) * avg / t))
			}
		case "step":
			th, cmp := num(m["threshold"]), m["comparison"]
			hit := (cmp == ">=" && avg >= th) || (cmp == ">" && avg > th) || (cmp == "<=" && avg <= th) || (cmp == "<" && avg < th)
			if hit {
				cand = desired + int(num(m["adjustment"]))
			}
		}
		if cand != desired && (best == desired || cand > best) {
			best = cand
			reason = fmt.Sprintf("Policy %s: average CPU %.1f%%, scaling to %d", name, avg, clamp(cand))
		}
	}
	d := clamp(best)
	return map[string]any{"desired": d, "change": d - desired, "reason": reason, "replace": replace}
}
