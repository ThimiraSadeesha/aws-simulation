package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudsim/backend/internal/testutil"
)

func TestSecretsEncryptionRedactionAndVersions(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	const plain = "s3cr3t-db-password-value"
	for _, bad := range []map[string]any{{"name": "bad name!", "secret_string": "x"}, {"name": "ok", "secret_string": ""}} {
		if r := c.Do("POST", "/api/v1/secrets", bad); r.Status != 400 {
			t.Errorf("%v: %d", bad, r.Status)
		}
	}
	cr := c.Do("POST", "/api/v1/secrets", map[string]any{"name": "prod/db/password", "description": "db", "secret_string": plain})
	if cr.Status != 201 || strings.Contains(string(cr.Body), plain) {
		t.Fatalf("create must not echo the value: %d %s", cr.Status, cr.Body)
	}
	// Random key file generated next to the DB with 0600 perms (SECRETS_KEY unset in tests).
	if os.Getenv("SECRETS_KEY") == "" {
		st, err := os.Stat(filepath.Join(filepath.Dir(h.Cfg.DatabasePath), "secrets.key"))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("key file: %v %v", st, err)
		}
	}
	// Encrypted at rest.
	var ct []byte
	if err := h.DB.QueryRow(`SELECT ciphertext FROM secret_versions`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(plain)) || len(ct) < len(plain)+12 {
		t.Fatal("secret value must be encrypted at rest")
	}
	const path = "/api/v1/secrets/prod%2Fdb%2Fpassword"
	for _, p := range []string{"/api/v1/secrets", path} {
		r := c.Do("GET", p, nil)
		if r.Status != 200 || strings.Contains(string(r.Body), plain) || !strings.Contains(string(r.Body), "********") {
			t.Fatalf("GET %s must redact: %d %s", p, r.Status, r.Body)
		}
	}
	v := c.Must(200, "GET", path+"/value", nil)
	if v["secret_string"] != plain {
		t.Fatalf("value: %v", v)
	}
	c.Must(200, "PUT", path, map[string]any{"secret_string": "rotated"})
	if v := c.Must(200, "GET", path+"/value", nil); v["secret_string"] != "rotated" {
		t.Fatalf("rotated value: %v", v)
	}
	if v := c.Must(200, "GET", path+"/value?version_stage=AWSPREVIOUS", nil); v["secret_string"] != plain {
		t.Fatalf("previous value: %v", v)
	}
	d := c.Must(200, "GET", path, nil)
	if len(d["versions"].([]any)) != 2 {
		t.Fatalf("versions: %v", d)
	}
	// GetSecretValue is audited even though it is a read.
	h.Srv.FlushAudit()
	h.Eventually(3*time.Second, func() bool {
		a := c.Must(200, "GET", "/api/v1/audit?action=secretsmanager:GetSecretValue", nil)
		return a["total"].(float64) >= 3
	}, "GetSecretValue audited")
	if r := h.Signup("bob").Do("GET", path+"/value", nil); r.Status != 404 {
		t.Fatalf("cross-account: %d", r.Status)
	}
	c.Must(204, "DELETE", path, nil)
	if r := c.Do("GET", path, nil); r.Status != 404 {
		t.Fatalf("deleted: %d", r.Status)
	}
	var n int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM secret_versions`).Scan(&n)
	if n != 0 {
		t.Fatal("versions should be removed with the secret")
	}
}
