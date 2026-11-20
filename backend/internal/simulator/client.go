// Package simulator is the Go client for the Rust simulation engine (protocol v1, docs/engine-protocol.md).
package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const ProtocolVersion = "1"

// Error is a non-2xx engine response.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("engine %d %s: %s", e.Status, e.Code, e.Message) }

// ErrUnavailable wraps transport failures (engine down).
var ErrUnavailable = errors.New("simulation engine unavailable")

type Client struct {
	base  string
	token string
	http  *http.Client
	long  *http.Client
}

func New(base, token string) *Client {
	return &Client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 10 * time.Second},
		long:  &http.Client{Timeout: 40 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Engine-Token", c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error Error `json:"error"`
		}
		_ = json.Unmarshal(data, &env)
		env.Error.Status = resp.StatusCode
		if env.Error.Code == "" {
			env.Error.Code = http.StatusText(resp.StatusCode)
		}
		return &env.Error
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// IsCode reports whether err is an engine error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

type Health struct {
	Status   string `json:"status"`
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
}

func (c *Client) Health(ctx context.Context) (*Health, error) {
	var h Health
	err := c.do(ctx, c.http, http.MethodGet, "/healthz", nil, &h)
	return &h, err
}

func (c *Client) Stats(ctx context.Context) (map[string]any, error) {
	var m map[string]any
	err := c.do(ctx, c.http, http.MethodGet, "/v1/stats", nil, &m)
	return m, err
}

type RegisterRequest struct {
	Kind       string         `json:"kind"`
	Owner      string         `json:"owner"`
	State      *string        `json:"state"`
	DependsOn  []string       `json:"depends_on"`
	Attributes map[string]any `json:"attributes"`
}

type Resource struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Owner      string         `json:"owner"`
	State      string         `json:"state"`
	DependsOn  []string       `json:"depends_on"`
	Dependents []string       `json:"dependents"`
	Attributes map[string]any `json:"attributes"`
	Version    int64          `json:"version"`
}

func (c *Client) Register(ctx context.Context, id string, r RegisterRequest) (*Resource, error) {
	if r.DependsOn == nil {
		r.DependsOn = []string{}
	}
	if r.Attributes == nil {
		r.Attributes = map[string]any{}
	}
	var out Resource
	err := c.do(ctx, c.http, http.MethodPut, "/v1/resources/"+url.PathEscape(id), r, &out)
	return &out, err
}

func (c *Client) Get(ctx context.Context, id string) (*Resource, error) {
	var out Resource
	err := c.do(ctx, c.http, http.MethodGet, "/v1/resources/"+url.PathEscape(id), nil, &out)
	return &out, err
}

func (c *Client) List(ctx context.Context, kind, owner string) ([]Resource, error) {
	var out struct {
		Items []Resource `json:"items"`
	}
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if owner != "" {
		q.Set("owner", owner)
	}
	err := c.do(ctx, c.http, http.MethodGet, "/v1/resources?"+q.Encode(), nil, &out)
	return out.Items, err
}

func (c *Client) Action(ctx context.Context, id, action, reason string) (*Resource, error) {
	var out Resource
	err := c.do(ctx, c.http, http.MethodPost, "/v1/resources/"+url.PathEscape(id)+"/actions",
		map[string]string{"action": action, "reason": reason}, &out)
	return &out, err
}

func (c *Client) Deregister(ctx context.Context, id string) error {
	err := c.do(ctx, c.http, http.MethodDelete, "/v1/resources/"+url.PathEscape(id), nil, nil)
	if IsCode(err, "not_found") {
		return nil
	}
	return err
}

type Workload struct {
	Profile      string   `json:"profile"`
	CPUTarget    *float64 `json:"cpu_target"`
	ExtraLoadRPS float64  `json:"extra_load_rps"`
}

func (c *Client) SetWorkload(ctx context.Context, id string, w Workload) error {
	return c.do(ctx, c.http, http.MethodPut, "/v1/resources/"+url.PathEscape(id)+"/workload", w, nil)
}

type Event struct {
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

type EventsPage struct {
	Events          []Event `json:"events"`
	Next            int64   `json:"next"`
	OldestAvailable int64   `json:"oldest_available"`
}

func (c *Client) Events(ctx context.Context, after int64, limit int, wait time.Duration) (*EventsPage, error) {
	var out EventsPage
	path := fmt.Sprintf("/v1/events?after=%d&limit=%d&wait_ms=%d", after, limit, wait.Milliseconds())
	err := c.do(ctx, c.long, http.MethodGet, path, nil, &out)
	return &out, err
}

type Sample struct {
	ResourceID      string  `json:"resource_id"`
	Owner           string  `json:"owner"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryPercent   float64 `json:"memory_percent"`
	NetworkInBytes  float64 `json:"network_in_bytes"`
	NetworkOutBytes float64 `json:"network_out_bytes"`
	DiskReadBytes   float64 `json:"disk_read_bytes"`
	DiskWriteBytes  float64 `json:"disk_write_bytes"`
}

type Snapshot struct {
	Tick    int64    `json:"tick"`
	TS      string   `json:"ts"`
	Samples []Sample `json:"samples"`
}

func (c *Client) MetricsSnapshot(ctx context.Context) (*Snapshot, error) {
	var out Snapshot
	err := c.do(ctx, c.http, http.MethodGet, "/v1/metrics/snapshot", nil, &out)
	return &out, err
}

// Raw performs an arbitrary JSON call (used for stateless simulation endpoints whose
// payloads are passed through: reachability, lb, autoscaling, lifecycle, faults, quotas, cidr).
func (c *Client) Raw(ctx context.Context, method, path string, in, out any) error {
	return c.do(ctx, c.http, method, path, in, out)
}
