package labs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Caller performs an API call (path relative to /api/v1) as the account root, in-process.
type Caller interface {
	Call(ctx context.Context, method, path string, body any) (status int, resp []byte, err error)
}

// Result of a validation.
type Result struct {
	Passed  bool           `json:"passed"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type evaluator struct {
	ctx     context.Context
	c       Caller
	lab     *Lab
	account string
	refs    map[string]Ref
	vals    map[string]string
	active  map[string]bool
}

// failure is a validation failure with a learner-facing message.
type failure struct{ msg string }

func (f *failure) Error() string { return f.msg }

func failf(f string, a ...any) error { return &failure{fmt.Sprintf(f, a...)} }

// Evaluate validates step idx of lab against live state. It never assumes success: every check
// queries the simulator through c.
func Evaluate(ctx context.Context, c Caller, lab *Lab, idx int, account string) Result {
	if idx < 0 || idx >= len(lab.Steps) {
		return Result{Message: "no such step", Details: map[string]any{}}
	}
	ev := &evaluator{ctx: ctx, c: c, lab: lab, account: account, refs: map[string]Ref{}, active: map[string]bool{},
		vals: map[string]string{"account": account, "lab_id": lab.ID, "prefix": lab.ResourcePrefix}}
	for _, r := range lab.Refs {
		ev.refs[r.Name] = r
	}
	details := map[string]any{}
	msg, err := ev.check(lab.Steps[idx].Validation, details)
	for k, v := range ev.vals {
		if !builtins[k] {
			details[k] = v
		}
	}
	if err != nil {
		return Result{Passed: false, Message: err.Error(), Details: details}
	}
	return Result{Passed: true, Message: msg, Details: details}
}

// ---- variables & refs

func (ev *evaluator) subst(s string) (string, error) {
	var ferr error
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		v, err := ev.resolve(name)
		if err != nil && ferr == nil {
			ferr = err
		}
		return v
	})
	return out, ferr
}

func (ev *evaluator) resolve(name string) (string, error) {
	if v, ok := ev.vals[name]; ok {
		return v, nil
	}
	r, ok := ev.refs[name]
	if !ok {
		return "", failf("internal: unknown variable %s", name)
	}
	if ev.active[name] {
		return "", failf("internal: circular reference %s", name)
	}
	ev.active[name] = true
	defer delete(ev.active, name)
	items, err := ev.selectItems(r.Selector)
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if vs := lookup(it, r.Field); len(vs) > 0 {
			v := str(vs[0])
			ev.vals[name] = v
			return v, nil
		}
	}
	what := r.Describe
	if what == "" {
		what = describeSelector(r.Selector)
	}
	return "", failf("Not found yet: %s.", what)
}

func describeSelector(s Selector) string {
	parts := []string{}
	for _, m := range s.Match {
		op := m.Op
		if op == "" {
			op = "="
		}
		parts = append(parts, fmt.Sprintf("%s %s %v", m.Field, op, m.Value))
	}
	d := "a resource from " + s.Resource
	if len(parts) > 0 {
		d += " with " + strings.Join(parts, " and ")
	}
	return d
}

// ---- API access

func (ev *evaluator) get(path string) (any, int, error) {
	st, b, err := ev.c.Call(ev.ctx, "GET", path, nil)
	if err != nil {
		return nil, st, err
	}
	return decodeJSON(b), st, nil
}

func decodeJSON(b []byte) any {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	_ = dec.Decode(&v)
	return v
}

func apiErr(v any, st int) error {
	if m, ok := v.(map[string]any); ok {
		if e, ok := m["error"].(map[string]any); ok {
			if st == 501 || st == 404 && strings.Contains(str(e["message"]), "no such API route") {
				return failf("This service is not available in this CloudSim release (%s).", str(e["code"]))
			}
			return failf("Could not query the simulator: %s: %s", str(e["code"]), str(e["message"]))
		}
	}
	return failf("Could not query the simulator (HTTP %d)", st)
}

// list returns the items of a list endpoint (all pages), a raw array, or a single object.
func (ev *evaluator) list(resource string, query map[string]string) ([]any, error) {
	res, err := ev.subst(resource)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for k, v := range query {
		sv, err := ev.subst(v)
		if err != nil {
			return nil, err
		}
		q.Set(k, sv)
	}
	q.Set("page_size", "200")
	var all []any
	for page := 1; page <= 20; page++ {
		q.Set("page", strconv.Itoa(page))
		v, st, err := ev.get(res + "?" + q.Encode())
		if err != nil {
			return nil, err
		}
		if st == 404 && !isRouteMissing(v) {
			return nil, nil // the named resource does not exist (yet)
		}
		if st >= 400 {
			return nil, apiErr(v, st)
		}
		switch t := v.(type) {
		case []any:
			return t, nil
		case map[string]any:
			items, ok := t["items"].([]any)
			if !ok {
				return []any{t}, nil
			}
			all = append(all, items...)
			if len(items) == 0 || len(all) >= toInt(t["total"]) {
				return all, nil
			}
		default:
			return all, nil
		}
	}
	return all, nil
}

func isRouteMissing(v any) bool {
	m, _ := v.(map[string]any)
	e, _ := m["error"].(map[string]any)
	return strings.Contains(str(e["message"]), "no such API route")
}

func (ev *evaluator) selectItems(s Selector) ([]any, error) {
	items, err := ev.list(s.Resource, s.Query)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, it := range items {
		ok, err := ev.matchAll(it, s.Match)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// ---- matching

func (ev *evaluator) matchAll(item any, ms []Matcher) (bool, error) {
	for _, m := range ms {
		ok, err := ev.match(item, m)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (ev *evaluator) match(item any, m Matcher) (bool, error) {
	op := m.Op
	if op == "" {
		op = "equals"
	}
	want := m.Value
	if s, ok := want.(string); ok {
		sv, err := ev.subst(s)
		if err != nil {
			return false, err
		}
		want = sv
	}
	raw := lookupRaw(item, m.Field)
	vals := lookup(item, m.Field)
	switch op {
	case "exists":
		return len(vals) > 0, nil
	case "not_exists":
		return len(vals) == 0, nil
	case "len_gte":
		n := 0
		for _, r := range raw {
			switch t := r.(type) {
			case []any:
				n = max(n, len(t))
			case map[string]any:
				n = max(n, len(t))
			case string:
				n = max(n, len(t))
			}
		}
		return float64(n) >= num(want), nil
	case "any_elem":
		for _, r := range raw {
			arr, _ := r.([]any)
			for _, e := range arr {
				ok, err := ev.matchAll(e, m.Match)
				if err != nil {
					return false, err
				}
				if ok {
					return true, nil
				}
			}
		}
		return false, nil
	case "not_equals":
		for _, v := range vals {
			if str(v) == str(want) {
				return false, nil
			}
		}
		return true, nil
	}
	for _, v := range vals {
		if cmp(op, v, want) {
			return true, nil
		}
	}
	return false, nil
}

func cmp(op string, got, want any) bool {
	g, w := str(got), str(want)
	switch op {
	case "equals":
		return g == w
	case "prefix":
		return strings.HasPrefix(g, w)
	case "suffix":
		return strings.HasSuffix(g, w)
	case "contains":
		return strings.Contains(g, w)
	case "regex":
		re, err := regexp.Compile(w)
		return err == nil && re.MatchString(g)
	case "in":
		if arr, ok := want.([]any); ok {
			for _, a := range arr {
				if str(a) == g {
					return true
				}
			}
		}
		return false
	case "gt", "gte", "lt", "lte":
		gv, err := strconv.ParseFloat(g, 64)
		if err != nil {
			return false
		}
		wv := num(want)
		switch op {
		case "gt":
			return gv > wv
		case "gte":
			return gv >= wv
		case "lt":
			return gv < wv
		default:
			return gv <= wv
		}
	}
	return false
}

func num(v any) float64 {
	f, _ := strconv.ParseFloat(str(v), 64)
	return f
}

func toInt(v any) int { return int(num(v)) }

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// lookupRaw resolves a dotted path, fanning out over arrays, without flattening the leaves.
func lookupRaw(v any, path string) []any {
	cur := []any{v}
	for _, part := range strings.Split(path, ".") {
		var next []any
		for _, c := range cur {
			switch t := c.(type) {
			case map[string]any:
				if x, ok := t[part]; ok && x != nil {
					next = append(next, x)
				}
			case []any:
				for _, e := range t {
					if m, ok := e.(map[string]any); ok {
						if x, ok := m[part]; ok && x != nil {
							next = append(next, x)
						}
					}
				}
			}
		}
		cur = next
	}
	return cur
}

// lookup is lookupRaw with leaf arrays flattened (so `subnet_ids` matches any element).
func lookup(v any, path string) []any {
	var out []any
	for _, c := range lookupRaw(v, path) {
		if arr, ok := c.([]any); ok {
			out = append(out, arr...)
		} else {
			out = append(out, c)
		}
	}
	return out
}

func describeItem(it any) string {
	m, ok := it.(map[string]any)
	if !ok {
		return str(it)
	}
	id := ""
	for k, v := range m {
		if strings.HasSuffix(k, "_id") && !strings.Contains(k, "vpc") && !strings.Contains(k, "subnet") && str(v) != "" && id == "" {
			id = str(v)
		}
	}
	name := str(m["name"])
	if name == "" {
		for _, k := range []string{"group_name", "user_name", "alarm_name", "role_name", "policy_name", "key"} {
			if n := str(m[k]); n != "" {
				name = n
				break
			}
		}
	}
	switch {
	case id != "" && name != "" && id != name:
		return id + " (" + name + ")"
	case id != "":
		return id
	}
	return name
}

// ---- checks

func (ev *evaluator) check(c Check, details map[string]any) (string, error) {
	for _, r := range c.Refs {
		ev.refs[r.Name] = r
	}
	label := func(def string) string {
		if c.Description != "" {
			return c.Description
		}
		return def
	}
	switch c.Type {
	case "resource_exists", "count_at_least", "absent":
		items, err := ev.selectItems(c.Selector)
		if err != nil {
			return "", err
		}
		descs := []string{}
		for _, it := range items {
			descs = append(descs, describeItem(it))
		}
		details["matched"] = descs
		switch c.Type {
		case "resource_exists":
			if len(items) == 0 {
				return "", failf("Not found yet: %s.", label(describeSelector(c.Selector)))
			}
			return fmt.Sprintf("Found %s.", strings.Join(descs, ", ")), nil
		case "count_at_least":
			if len(items) < c.Count {
				return "", failf("Expected at least %d matching resources, found %d: %s.", c.Count, len(items), label(describeSelector(c.Selector)))
			}
			return fmt.Sprintf("Found %d matching resources: %s.", len(items), strings.Join(descs, ", ")), nil
		default:
			if len(items) > 0 {
				return "", failf("Still present: %s (%s).", strings.Join(descs, ", "), label(describeSelector(c.Selector)))
			}
			return "No matching resources remain.", nil
		}
	case "reachability":
		src, err := ev.subst(c.Source)
		if err != nil {
			return "", err
		}
		dst, err := ev.subst(c.Destination)
		if err != nil {
			return "", err
		}
		proto := c.Protocol
		if proto == "" {
			proto = "tcp"
		}
		st, b, err := ev.c.Call(ev.ctx, "POST", "/vpc/reachability", map[string]any{"source": src, "destination": dst, "protocol": proto, "port": c.Port})
		if err != nil {
			return "", err
		}
		v := decodeJSON(b)
		if st >= 400 {
			return "", apiErr(v, st)
		}
		m, _ := v.(map[string]any)
		got := m["reachable"] == true
		want := c.Reachable == nil || *c.Reachable
		details["reachability"] = m
		probe := fmt.Sprintf("%s → %s %s/%d", src, dst, proto, c.Port)
		if got != want {
			if want {
				return "", failf("%s is not reachable yet: %s", probe, str(m["explanation"]))
			}
			return "", failf("%s is reachable but should be blocked: %s", probe, str(m["explanation"]))
		}
		verb := "reachable"
		if !got {
			verb = "blocked as expected"
		}
		return fmt.Sprintf("%s is %s. %s", probe, verb, str(m["explanation"])), nil
	case "iam_simulate":
		p, err := ev.subst(c.Principal)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(p, "arn:") {
			p = "arn:cloudsim:iam::" + ev.account + ":user/" + p
		}
		res, err := ev.subst(c.ResourceARN)
		if err != nil {
			return "", err
		}
		if res == "" {
			res = "*"
		}
		st, b, err := ev.c.Call(ev.ctx, "POST", "/iam/simulate", map[string]any{"principal_arn": p, "action": c.Action, "resource_arn": res})
		if err != nil {
			return "", err
		}
		v := decodeJSON(b)
		if st >= 400 {
			return "", apiErr(v, st)
		}
		m, _ := v.(map[string]any)
		dec := str(m["decision"])
		details["decision"] = dec
		ok := dec == c.Decision || (c.Decision == "denied" && (dec == "explicitDeny" || dec == "implicitDeny"))
		if !ok {
			return "", failf("%s on %s for %s evaluates to %s; expected %s.", c.Action, res, p, dec, c.Decision)
		}
		return fmt.Sprintf("%s on %s for %s evaluates to %s.", c.Action, res, p, dec), nil
	case "s3_object_exists":
		bucket, err := ev.subst(c.Bucket)
		if err != nil {
			return "", err
		}
		key, err := ev.subst(c.Key)
		if err != nil {
			return "", err
		}
		prefix, err := ev.subst(c.KeyPrefix)
		if err != nil {
			return "", err
		}
		if key != "" {
			v, st, err := ev.get("/s3/buckets/" + url.PathEscape(bucket) + "/object-info/" + escKey(key))
			if err != nil {
				return "", err
			}
			if st == 404 && !isRouteMissing(v) {
				return "", failf("Object %s was not found in bucket %s yet.", key, bucket)
			}
			if st >= 400 {
				return "", apiErr(v, st)
			}
			return fmt.Sprintf("Found object local://%s/%s.", bucket, key), nil
		}
		items, err := ev.list("/s3/buckets/"+url.PathEscape(bucket)+"/objects", map[string]string{"prefix": prefix})
		if err != nil {
			return "", err
		}
		if len(items) == 0 {
			return "", failf("No objects with prefix %q in bucket %s yet.", prefix, bucket)
		}
		m, _ := items[0].(map[string]any)
		return fmt.Sprintf("Found %d object(s) in local://%s, e.g. %s.", len(items), bucket, str(m["key"])), nil
	case "metric_above":
		q := url.Values{"namespace": {c.Namespace}, "metric_name": {c.MetricName}, "period": {"60"}}
		stat := c.Stat
		if stat == "" {
			stat = "Maximum"
		}
		q.Set("stat", stat)
		for k, v := range c.Dimensions {
			sv, err := ev.subst(v)
			if err != nil {
				return "", err
			}
			q.Add("dimension", k+":"+sv)
		}
		win := c.WindowMinutes
		if win <= 0 {
			win = 15
		}
		q.Set("start", time.Now().Add(-time.Duration(win)*time.Minute).UTC().Format(time.RFC3339))
		v, st, err := ev.get("/cloudwatch/metrics?" + q.Encode())
		if err != nil {
			return "", err
		}
		if st >= 400 {
			return "", apiErr(v, st)
		}
		m, _ := v.(map[string]any)
		dps, _ := m["datapoints"].([]any)
		best := -1.0
		for _, dp := range dps {
			if d, ok := dp.(map[string]any); ok {
				best = max(best, num(d["value"]))
			}
		}
		details["max_value"] = best
		if best <= c.Threshold {
			if len(dps) == 0 {
				return "", failf("No %s datapoints in the last %d minutes yet.", c.MetricName, win)
			}
			return "", failf("%s peaked at %.1f in the last %d minutes; it must exceed %.1f.", c.MetricName, best, win, c.Threshold)
		}
		return fmt.Sprintf("%s reached %.1f (> %.1f).", c.MetricName, best, c.Threshold), nil
	case "alarm_state":
		name, err := ev.subst(c.Alarm)
		if err != nil {
			return "", err
		}
		v, st, err := ev.get("/cloudwatch/alarms/" + url.PathEscape(name))
		if err != nil {
			return "", err
		}
		if st == 404 && !isRouteMissing(v) {
			return "", failf("Alarm %s does not exist yet.", name)
		}
		if st >= 400 {
			return "", apiErr(v, st)
		}
		m, _ := v.(map[string]any)
		state := str(m["state"])
		details["state"] = state
		if state != c.State {
			return "", failf("Alarm %s is %s; waiting for %s. %s", name, state, c.State, str(m["state_reason"]))
		}
		return fmt.Sprintf("Alarm %s is in state %s.", name, state), nil
	case "target_health":
		tg, err := ev.subst(c.TargetGroup)
		if err != nil {
			return "", err
		}
		v, st, err := ev.get("/elb/target-groups/" + url.PathEscape(tg) + "/health")
		if err != nil {
			return "", err
		}
		if st >= 400 {
			return "", apiErr(v, st)
		}
		targets := findTargets(v)
		healthy := 0
		for _, t := range targets {
			if m, ok := t.(map[string]any); ok && str(m["health"]) == "healthy" {
				healthy++
			}
		}
		minH := c.MinHealthy
		if minH <= 0 {
			minH = 1
		}
		details["healthy"], details["targets"] = healthy, len(targets)
		if healthy < minH {
			return "", failf("Target group %s has %d healthy of %d registered targets; need at least %d healthy.", tg, healthy, len(targets), minH)
		}
		return fmt.Sprintf("Target group %s has %d healthy targets.", tg, healthy), nil
	case "all":
		var msgs []string
		for _, sub := range c.Checks {
			m, err := ev.check(sub, details)
			if err != nil {
				return "", err
			}
			msgs = append(msgs, m)
		}
		return strings.Join(msgs, " "), nil
	case "any":
		var errs []string
		for _, sub := range c.Checks {
			m, err := ev.check(sub, details)
			if err == nil {
				return m, nil
			}
			if _, ok := err.(*failure); !ok {
				return "", err
			}
			errs = append(errs, err.Error())
		}
		return "", failf("None of the alternatives passed: %s", strings.Join(errs, " | "))
	}
	return "", failf("unsupported validation type %s", c.Type)
}

func findTargets(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case map[string]any:
		for _, k := range []string{"targets", "items", "target_health_descriptions"} {
			if arr, ok := t[k].([]any); ok {
				return arr
			}
		}
	}
	return nil
}

func escKey(k string) string {
	parts := strings.Split(k, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
