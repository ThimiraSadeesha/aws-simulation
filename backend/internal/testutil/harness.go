// Package testutil starts a full backend (real SQLite, fake engine) for integration tests.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"cloudsim/backend/internal/api"
	"cloudsim/backend/internal/config"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
	"cloudsim/backend/internal/simulator/simtest"
)

type Harness struct {
	T      testing.TB
	Srv    *api.Server
	HTTP   *httptest.Server
	Engine *simtest.Engine
	DB     *db.DB
	Cfg    *config.Config
	cancel context.CancelFunc
	done   chan struct{}
}

// New starts a backend. Background workers (event pump etc.) run unless noWorkers is set.
func New(t testing.TB, mutate ...func(*config.Config)) *Harness {
	t.Helper()
	dir := t.TempDir()
	eng := simtest.New("test-token")
	cfg := &config.Config{
		Addr: ":0", DatabasePath: filepath.Join(dir, "cloudsim.db"), ObjectStorageDir: filepath.Join(dir, "objects"),
		BackupDir: filepath.Join(dir, "backups"), LabsDir: filepath.Join("..", "..", "training-labs"), EngineURL: eng.URL, EngineToken: "test-token",
		AllowRegistration: true, SessionTTL: time.Hour, RateLimitRPS: 1000, RateLimitBurst: 2000, LoginPerMinute: 100000, MaxObjectSize: 10 << 20,
		LogLevel: "error", MetricsInterval: time.Hour, LifecycleInterval: time.Hour, ASGInterval: time.Hour, AlarmInterval: time.Hour,
		MetricsRetention: time.Hour, Version: "test",
	}
	for _, m := range mutate {
		m(cfg)
	}
	d, err := db.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out io.Writer = io.Discard
	if os.Getenv("TEST_LOG") != "" {
		out = os.Stderr
	}
	log := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv := api.New(cfg, repositories.NewStore(d), simulator.New(eng.URL, "test-token"), log)
	srv.Auth.BcryptCost = bcrypt.MinCost
	if err := srv.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := &Harness{T: t, Srv: srv, Engine: eng, DB: d, Cfg: cfg, done: make(chan struct{})}
	h.HTTP = httptest.NewServer(srv.Handler())
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { srv.Start(ctx); close(h.done) }()
	t.Cleanup(h.Close)
	return h
}

func (h *Harness) Close() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
	}
	h.HTTP.Close()
	h.Engine.Close()
	h.DB.Close()
}

// Client is an API client authenticated either by session cookie or access key.
type Client struct {
	h       *Harness
	http    *http.Client
	CSRF    string
	KeyID   string
	Secret  string
	Session string
	User    map[string]any
}

type Resp struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r *Resp) JSON(v any) error { return json.Unmarshal(r.Body, v) }

func (r *Resp) Map() map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(r.Body, &m)
	return m
}

func (r *Resp) ErrCode() string {
	m := r.Map()
	if e, ok := m["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (h *Harness) Anon() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{h: h, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// Do sends a request; body may be nil, []byte (raw) or any JSON-serialisable value.
func (c *Client) Do(method, path string, body any, headers ...string) *Resp {
	c.h.T.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
		ct = "application/json"
	}
	req, err := http.NewRequest(method, c.h.HTTP.URL+path, rd)
	if err != nil {
		c.h.T.Fatal(err)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if c.CSRF != "" {
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	if c.KeyID != "" {
		req.Header.Set("Authorization", "CloudSim "+c.KeyID+":"+c.Secret)
		if c.Session != "" {
			req.Header.Set("X-CloudSim-Session-Token", c.Session)
		}
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.T.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return &Resp{Status: resp.StatusCode, Body: b, Header: resp.Header}
}

// Must asserts the status code and returns the decoded JSON object.
func (c *Client) Must(status int, method, path string, body any) map[string]any {
	c.h.T.Helper()
	r := c.Do(method, path, body)
	if r.Status != status {
		c.h.T.Fatalf("%s %s: want %d got %d: %s", method, path, status, r.Status, r.Body)
	}
	return r.Map()
}

// Signup registers and logs in a new platform user.
func (h *Harness) Signup(username string) *Client {
	h.T.Helper()
	c := h.Anon()
	c.Must(201, "POST", "/api/v1/auth/register", map[string]string{"username": username, "password": "correct-horse-battery"})
	m := c.Must(200, "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": "correct-horse-battery"})
	c.CSRF, _ = m["csrf_token"].(string)
	c.User, _ = m["user"].(map[string]any)
	return c
}

// AccountID of the session user.
func (c *Client) AccountID() string { s, _ := c.User["account_id"].(string); return s }

// IAMUser creates an IAM user with an access key and returns a key-authenticated client.
func (c *Client) IAMUser(name string, policies ...string) *Client {
	c.h.T.Helper()
	c.Must(201, "POST", "/api/v1/iam/users", map[string]string{"user_name": name})
	for _, p := range policies {
		r := c.Do("POST", "/api/v1/iam/users/"+name+"/attached-policies/"+p, nil)
		if r.Status != 204 {
			c.h.T.Fatalf("attach %s: %d %s", p, r.Status, r.Body)
		}
	}
	k := c.Must(201, "POST", "/api/v1/iam/users/"+name+"/access-keys", nil)
	return &Client{h: c.h, http: &http.Client{Timeout: 30 * time.Second}, KeyID: k["access_key_id"].(string), Secret: k["secret_access_key"].(string)}
}

// Eventually polls cond until true or timeout.
func (h *Harness) Eventually(timeout time.Duration, cond func() bool, msg string) {
	h.T.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.T.Fatalf("condition not met within %s: %s", timeout, msg)
}
