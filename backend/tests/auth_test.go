package tests

import (
	"testing"

	"cloudsim/backend/internal/config"
	"cloudsim/backend/internal/testutil"
)

func TestRegisterLoginLogout(t *testing.T) {
	h := testutil.New(t)
	c := h.Anon()
	if r := c.Do("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("anon /me: %d", r.Status)
	}
	m := c.Must(201, "POST", "/api/v1/auth/register", map[string]string{"username": "alice", "password": "s3cret-password"})
	if m["user"].(map[string]any)["role"] != "admin" {
		t.Fatal("first user must be admin")
	}
	m2 := h.Anon().Must(201, "POST", "/api/v1/auth/register", map[string]string{"username": "bob", "password": "another-password"})
	if m2["user"].(map[string]any)["role"] != "user" {
		t.Fatal("second user must be a regular user")
	}
	if r := h.Anon().Do("POST", "/api/v1/auth/register", map[string]string{"username": "alice", "password": "s3cret-password"}); r.Status != 409 {
		t.Fatalf("duplicate username: %d", r.Status)
	}
	if r := h.Anon().Do("POST", "/api/v1/auth/register", map[string]string{"username": "x", "password": "short"}); r.Status != 400 {
		t.Fatalf("validation: %d", r.Status)
	}
	if r := c.Do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong-password"}); r.Status != 401 {
		t.Fatalf("wrong password: %d", r.Status)
	}
	m = c.Must(200, "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "s3cret-password"})
	c.CSRF = m["csrf_token"].(string)
	me := c.Must(200, "GET", "/api/v1/auth/me", nil)
	if me["csrf_token"] != c.CSRF {
		t.Fatal("me must return csrf token")
	}
	c.Must(204, "POST", "/api/v1/auth/logout", nil)
	if r := c.Do("GET", "/api/v1/auth/me", nil); r.Status != 401 {
		t.Fatalf("after logout: %d", r.Status)
	}
}

func TestCSRFRequiredForCookieMutations(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	tok := c.CSRF
	c.CSRF = ""
	r := c.Do("POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"})
	if r.Status != 403 || r.ErrCode() != "CSRFTokenInvalid" {
		t.Fatalf("missing csrf: %d %s", r.Status, r.Body)
	}
	c.CSRF = "wrong"
	if r := c.Do("POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"}); r.Status != 403 {
		t.Fatalf("bad csrf: %d", r.Status)
	}
	c.CSRF = tok
	c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"})
	// GET does not need CSRF
	c.CSRF = ""
	c.Must(200, "GET", "/api/v1/vpc/vpcs", nil)
}

func TestLoginRateLimited(t *testing.T) {
	h := testutil.New(t, func(c *config.Config) { c.LoginPerMinute = 10 })
	h.Signup("alice")
	c := h.Anon()
	limited := false
	for i := 0; i < 15; i++ {
		if r := c.Do("POST", "/api/v1/auth/login", map[string]string{"username": "ratelimit-user", "password": "nope-nope-nope"}); r.Status == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected login attempts to be rate limited")
	}
}

func TestAccountIsolation(t *testing.T) {
	h := testutil.New(t)
	alice := h.Signup("alice")
	bob := h.Signup("bob")
	v := alice.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.1.0.0/16"})
	id := v["vpc_id"].(string)
	if r := bob.Do("GET", "/api/v1/vpc/vpcs/"+id, nil); r.Status != 404 {
		t.Fatalf("bob must not see alice's vpc: %d", r.Status)
	}
	if r := bob.Do("DELETE", "/api/v1/vpc/vpcs/"+id, nil); r.Status != 404 {
		t.Fatalf("bob must not delete alice's vpc: %d", r.Status)
	}
	l := bob.Must(200, "GET", "/api/v1/vpc/vpcs", nil)
	if l["total"].(float64) != 0 {
		t.Fatal("bob should have no vpcs")
	}
}

func TestAdminEndpointsRequireAdmin(t *testing.T) {
	h := testutil.New(t)
	admin := h.Signup("admin")
	user := h.Signup("user1")
	if r := user.Do("GET", "/api/v1/admin/users", nil); r.Status != 403 {
		t.Fatalf("non-admin: %d", r.Status)
	}
	admin.Must(200, "GET", "/api/v1/admin/users", nil)
	rep := admin.Must(200, "GET", "/api/v1/admin/integrity", nil)
	if rep["integrity_check"] != "ok" {
		t.Fatalf("integrity: %v", rep)
	}
	b := admin.Must(201, "POST", "/api/v1/admin/backups", nil)
	if b["size"].(float64) <= 0 {
		t.Fatal("backup should have size")
	}
	l := admin.Must(200, "GET", "/api/v1/admin/backups", nil)
	if len(l["items"].([]any)) != 1 {
		t.Fatalf("backups: %v", l)
	}
}
