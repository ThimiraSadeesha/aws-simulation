package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HTTPTransport talks to a CloudSim backend over HTTP with access-key authentication.
type HTTPTransport struct {
	Endpoint     string // e.g. http://localhost:8080
	AccessKeyID  string
	SecretKey    string
	SessionToken string
	Client       *http.Client
}

// blockedHosts guards against pointing the CLI at real cloud provider APIs: CloudSim only ever
// talks to a CloudSim simulator endpoint.
var blockedHosts = []string{"amazonaws.com", "amazonaws.com.cn", "aws.amazon.com", "api.aws"}

// NewHTTPTransport validates the endpoint and builds a transport.
func NewHTTPTransport(endpoint, keyID, secret, token string) (*HTTPTransport, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint URL %q (expected e.g. http://localhost:8080)", endpoint)
	}
	h := strings.ToLower(u.Hostname())
	for _, b := range blockedHosts {
		if h == b || strings.HasSuffix(h, "."+b) {
			return nil, fmt.Errorf("refusing endpoint %q: the cloudsim CLI only talks to a CloudSim simulator, never to real cloud APIs", endpoint)
		}
	}
	return &HTTPTransport{Endpoint: u.String(), AccessKeyID: keyID, SecretKey: secret, SessionToken: token,
		Client: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (t *HTTPTransport) Do(ctx context.Context, method, path string, body []byte, headers map[string]string) (int, []byte, map[string]string, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.Endpoint+"/api/v1"+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "cloudsim-cli/"+Version)
	if t.AccessKeyID != "" {
		req.Header.Set("Authorization", "CloudSim "+t.AccessKeyID+":"+t.SecretKey)
		if t.SessionToken != "" {
			req.Header.Set("X-CloudSim-Session-Token", t.SessionToken)
		}
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return 0, nil, nil, err
	}
	rh := map[string]string{}
	for k := range resp.Header {
		rh[k] = resp.Header.Get(k)
	}
	return resp.StatusCode, b, rh, nil
}

// OSFS is the LocalFS of the standalone CLI.
type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(expandHome(name)) }

func (OSFS) WriteFile(name string, data []byte) error {
	name = expandHome(name)
	if dir := filepath.Dir(name); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(name, data, 0o644)
}

func (OSFS) IsDir(name string) bool {
	st, err := os.Stat(expandHome(name))
	return err == nil && st.IsDir()
}

func (OSFS) Walk(root string, fn func(rel string) error) error {
	root = expandHome(root)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel))
	})
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
