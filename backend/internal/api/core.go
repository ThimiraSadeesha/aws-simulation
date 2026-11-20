// Package api contains HTTP routing, middleware and handlers. Handlers are thin: they
// decode/validate input, authorize via IAM, and delegate to internal/services.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/policy"
	"cloudsim/backend/internal/repositories"
)

type ctxKey int

const (
	keyPrincipal ctxKey = iota
	keyRequestID
	keyAudit
)

// auditInfo is filled by handlers so the audit middleware can record what happened.
type auditInfo struct {
	action   string
	resource string
	denied   bool
	detail   string
}

func principalFrom(r *http.Request) *auth.Principal {
	p, _ := r.Context().Value(keyPrincipal).(*auth.Principal)
	return p
}

func requestID(r *http.Request) string {
	s, _ := r.Context().Value(keyRequestID).(string)
	return s
}

// Ctx is passed to operation handlers.
type Ctx struct {
	W http.ResponseWriter
	R *http.Request
	P *auth.Principal
	S *Server
}

func (c *Ctx) Context() context.Context { return c.R.Context() }
func (c *Ctx) Path(name string) string  { return c.R.PathValue(name) }
func (c *Ctx) Query(name string) string { return c.R.URL.Query().Get(name) }

// Decode reads a JSON body (max 1 MiB) into v, rejecting unknown trailing data.
func (c *Ctx) Decode(v any) error {
	body := http.MaxBytesReader(c.W, c.R.Body, 1<<20)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return apierr.Validation("request body is required")
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apierr.Validation("request body too large")
		}
		return apierr.Validation("invalid JSON body: %v", err)
	}
	return nil
}

// JSON writes v with status.
func (c *Ctx) JSON(status int, v any) error {
	writeJSON(c.W, status, v)
	return nil
}

func (c *Ctx) NoContent() error {
	c.W.WriteHeader(http.StatusNoContent)
	return nil
}

// Authorize checks an additional action/resource (beyond the route's primary one).
func (c *Ctx) Authorize(action, resource string, resourcePolicies []policy.Named, extra map[string]string) error {
	return c.S.authorize(c.R, c.P, action, resource, resourcePolicies, extra)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := apierr.As(err)
	if e.Status >= 500 {
		s.log.Error("request failed", "err", err.Error(), "path", r.URL.Path, "request_id", requestID(r))
	}
	if e.Status == http.StatusTooManyRequests || e.Status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message, "request_id": requestID(r)}})
}

// ArnFn computes the resource ARN for an operation from the request.
type ArnFn func(c *Ctx) string

// Op is an operation handler.
type Op func(c *Ctx) error

// op registers an authenticated route. action may be "" for routes that need only authentication.
func (s *Server) op(pattern, action string, arn ArnFn, h Op) {
	s.mux.Handle(pattern, s.authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &Ctx{W: w, R: r, P: principalFrom(r), S: s}
		if action != "" {
			res := "*"
			if arn != nil {
				res = arn(c)
			}
			if err := s.authorize(r, c.P, action, res, nil, nil); err != nil {
				s.writeError(w, r, err)
				return
			}
		}
		if err := h(c); err != nil {
			s.writeError(w, r, err)
		}
	})))
}

// public registers an unauthenticated route.
func (s *Server) public(pattern string, h Op) {
	s.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &Ctx{W: w, R: r, S: s}
		if err := h(c); err != nil {
			s.writeError(w, r, err)
		}
	}))
}

func (s *Server) authorize(r *http.Request, p *auth.Principal, action, resource string, rp []policy.Named, extra map[string]string) error {
	ai, _ := r.Context().Value(keyAudit).(*auditInfo)
	if ai != nil && ai.action == "" {
		ai.action, ai.resource = action, resource
	}
	res, err := s.Auth.Authorize(r.Context(), p, action, resource, rp, extra)
	if err != nil {
		return err
	}
	if !res.Allowed() {
		if ai != nil {
			ai.action, ai.resource, ai.denied = action, resource, true
			ai.detail = string(res.Decision)
		}
		return apierr.Denied("User: %s is not authorized to perform: %s on resource: %s (%s)", p.ARN, action, resource, res.Decision)
	}
	return nil
}

// ---- middleware

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = 200
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController / websocket hijack reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// base wraps every request: request id, security headers, panic recovery, structured access log, audit.
func (s *Server) base(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := ids.Token()[:16]
		w.Header().Set("X-Request-Id", rid)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		ai := &auditInfo{}
		ctx := context.WithValue(r.Context(), keyRequestID, rid)
		ctx = context.WithValue(ctx, keyAudit, ai)
		ctx = context.WithValue(ctx, keyPrincipalStash, &principalStash{})
		r = r.WithContext(ctx)
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "value", fmt.Sprint(v), "stack", string(debug.Stack()), "request_id", rid)
				if rec.status == 0 {
					s.writeError(rec, r, fmt.Errorf("panic"))
				}
			}
			status := rec.status
			if status == 0 {
				status = 200
			}
			dur := time.Since(start)
			if r.URL.Path != "/healthz" {
				s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", status, "dur_ms", dur.Milliseconds(), "bytes", rec.bytes, "request_id", rid)
			}
			s.recordAudit(r, ai, status)
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) recordAudit(r *http.Request, ai *auditInfo, status int) {
	if ai.action == "" {
		return
	}
	// Reads are audited only when denied, to keep the log meaningful.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if !ai.denied {
			return
		}
	}
	p := principalFrom(r)
	if p == nil {
		// principal is attached to the inner request; recover it via the stash
		if v, ok := r.Context().Value(keyPrincipalStash).(*principalStash); ok {
			p = v.p
		}
	}
	e := repositories.AuditEntry{
		TS: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Action: ai.action, Resource: ai.resource,
		StatusCode: status, RequestID: requestID(r), SourceIP: clientIP(r), Detail: ai.detail,
	}
	if p != nil {
		e.AccountID, e.Principal = p.AccountID, p.Display()
	}
	switch {
	case ai.denied:
		e.Result = "denied"
	case status >= 400:
		e.Result = "error"
	default:
		e.Result = "allowed"
	}
	s.audit.Add(e)
}

type principalStash struct{ p *auth.Principal }

const keyPrincipalStash ctxKey = 99

// authenticated resolves the principal (session cookie or access key), enforces CSRF and rate limits.
func (s *Server) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p *auth.Principal
		var err error
		if ip, ok := r.Context().Value(keyInternalPrincipal).(*auth.Principal); ok && ip != nil {
			p = ip // in-process call (terminal, lab validators): principal already authenticated
		} else {
			p, err = s.resolvePrincipal(r)
		}
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if p.ViaSession && r.Context().Value(keyInternalPrincipal) == nil && !isSafeMethod(r.Method) && !strings.HasPrefix(r.URL.Path, "/api/v1/ws/") {
			tok := r.Header.Get("X-CSRF-Token")
			if tok == "" || !constantEq(tok, p.CSRFToken) {
				s.writeError(w, r, apierr.New(http.StatusForbidden, "CSRFTokenInvalid", "missing or invalid X-CSRF-Token header"))
				return
			}
		}
		if !s.limiter.allow(p.AccountID + "|" + p.Display()) {
			s.writeError(w, r, apierr.New(http.StatusTooManyRequests, "RateLimited", "rate limit exceeded; slow down"))
			return
		}
		if st, ok := r.Context().Value(keyPrincipalStash).(*principalStash); ok {
			st.p = p
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyPrincipal, p)))
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func constantEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

const sessionCookie = "cs_session"

func (s *Server) resolvePrincipal(r *http.Request) (*auth.Principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "CloudSim "
		if !strings.HasPrefix(h, prefix) {
			return nil, apierr.Unauthenticated("Authorization header must be 'CloudSim <access_key_id>:<secret_access_key>'")
		}
		id, secret, ok := strings.Cut(strings.TrimPrefix(h, prefix), ":")
		if !ok || id == "" || secret == "" {
			return nil, apierr.Unauthenticated("malformed CloudSim credentials")
		}
		return s.Auth.AccessKeyPrincipal(r.Context(), id, secret, r.Header.Get("X-CloudSim-Session-Token"))
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, apierr.Unauthenticated("authentication required")
	}
	return s.Auth.SessionPrincipal(r.Context(), c.Value)
}

// ---- rate limiting

type limiterSet struct {
	mu    sync.Mutex
	rps   rate.Limit
	burst int
	m     map[string]*limEntry
}

type limEntry struct {
	l    *rate.Limiter
	seen time.Time
}

func newLimiterSet(rps float64, burst int) *limiterSet {
	return &limiterSet{rps: rate.Limit(rps), burst: burst, m: map[string]*limEntry{}}
}

func (l *limiterSet) allow(key string) bool {
	if l.rps <= 0 {
		return true
	}
	l.mu.Lock()
	e, ok := l.m[key]
	if !ok {
		e = &limEntry{l: rate.NewLimiter(l.rps, l.burst)}
		l.m[key] = e
	}
	e.seen = time.Now()
	l.mu.Unlock()
	return e.l.Allow()
}

func (l *limiterSet) gc(maxIdle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.m {
		if time.Since(e.seen) > maxIdle {
			delete(l.m, k)
		}
	}
}

// ---- generic list handling (search, filter, sort, paginate)

type ListParams struct {
	Q        string
	Sort     string
	Desc     bool
	Page     int
	PageSize int
	Filters  map[string]string // field -> exact value; "tag:K" -> tag value
}

var reserved = map[string]bool{"q": true, "sort": true, "order": true, "page": true, "page_size": true}

func parseList(r *http.Request) ListParams {
	q := r.URL.Query()
	p := ListParams{Q: strings.TrimSpace(q.Get("q")), Sort: q.Get("sort"), Desc: strings.EqualFold(q.Get("order"), "desc"), Page: 1, PageSize: 20, Filters: map[string]string{}}
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 0 {
		p.Page = n
	}
	if n, err := strconv.Atoi(q.Get("page_size")); err == nil && n > 0 {
		p.PageSize = min(n, 200)
	}
	for k, v := range q {
		if !reserved[k] && len(v) > 0 && v[0] != "" {
			p.Filters[k] = v[0]
		}
	}
	return p
}

type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// paginate applies q/filters/sort/pagination generically using each item's JSON representation.
// Collections are per-account and quota-bounded, so in-memory processing is cheap and uniform.
func paginate[T any](items []T, p ListParams, allowedFilters ...string) Page[T] {
	allowed := map[string]bool{}
	for _, f := range allowedFilters {
		allowed[f] = true
	}
	type row struct {
		item T
		m    map[string]any
	}
	rows := make([]row, 0, len(items))
	needle := strings.ToLower(p.Q)
	for _, it := range items {
		b, _ := json.Marshal(it)
		m := map[string]any{}
		_ = json.Unmarshal(b, &m)
		if needle != "" && !strings.Contains(strings.ToLower(string(b)), needle) {
			continue
		}
		ok := true
		for k, v := range p.Filters {
			if strings.HasPrefix(k, "tag:") {
				tags, _ := m["tags"].(map[string]any)
				if fmt.Sprint(tags[strings.TrimPrefix(k, "tag:")]) != v {
					ok = false
				}
				continue
			}
			if !allowed[k] {
				continue
			}
			if fmt.Sprint(m[k]) != v {
				ok = false
			}
		}
		if ok {
			rows = append(rows, row{it, m})
		}
	}
	if p.Sort != "" {
		sort.SliceStable(rows, func(i, j int) bool {
			less := compareAny(rows[i].m[p.Sort], rows[j].m[p.Sort])
			if p.Desc {
				return less > 0
			}
			return less < 0
		})
	}
	total := len(rows)
	start := (p.Page - 1) * p.PageSize
	if start > total {
		start = total
	}
	end := min(start+p.PageSize, total)
	out := make([]T, 0, end-start)
	for _, r := range rows[start:end] {
		out = append(out, r.item)
	}
	return Page[T]{Items: out, Total: total, Page: p.Page, PageSize: p.PageSize}
}

func compareAny(a, b any) int {
	if a == nil || b == nil {
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return -1
		default:
			return 1
		}
	}
	if fa, ok := a.(float64); ok {
		if fb, ok := b.(float64); ok {
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		}
	}
	if reflect.TypeOf(a).Kind() == reflect.Bool {
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	}
	return strings.Compare(strings.ToLower(fmt.Sprint(a)), strings.ToLower(fmt.Sprint(b)))
}

// ---- async audit writer

type auditWriter struct {
	ch   chan repositories.AuditEntry
	repo repositories.AuditRepo
	log  *slog.Logger
	done chan struct{}
}

func newAuditWriter(repo repositories.AuditRepo, log *slog.Logger) *auditWriter {
	return &auditWriter{ch: make(chan repositories.AuditEntry, 4096), repo: repo, log: log, done: make(chan struct{})}
}

func (a *auditWriter) Add(e repositories.AuditEntry) {
	select {
	case a.ch <- e:
	default:
		a.log.Warn("audit buffer full; dropping entry", "action", e.Action)
	}
}

// Run batches entries and flushes every 500ms or 200 entries, and drains on ctx cancel.
func (a *auditWriter) Run(ctx context.Context) {
	defer close(a.done)
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var buf []repositories.AuditEntry
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := a.repo.InsertBatch(context.Background(), buf); err != nil {
			a.log.Error("audit flush failed", "err", err)
		}
		buf = buf[:0]
	}
	for {
		select {
		case e := <-a.ch:
			buf = append(buf, e)
			if len(buf) >= 200 {
				flush()
			}
		case <-t.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case e := <-a.ch:
					buf = append(buf, e)
				default:
					flush()
					return
				}
			}
		}
	}
}

// Flush blocks until queued entries are written (used by tests).
func (a *auditWriter) Flush() {
	deadline := time.Now().Add(2 * time.Second)
	for len(a.ch) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
}

// Hijack supports websocket upgrades through the recorder.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	s.status = http.StatusSwitchingProtocols
	return h.Hijack()
}
