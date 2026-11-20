package tests

import (
	"encoding/json"
	"strings"
	"testing"

	"cloudsim/backend/internal/testutil"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func TestRoute53ZonesRecordsAndResolution(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	vpc := c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"})["vpc_id"].(string)
	other := c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.1.0.0/16"})["vpc_id"].(string)
	for _, bad := range []map[string]any{
		{"name": "bad_-.com."}, {"name": "-x.com"}, {"name": "corp.internal", "private": true},
		{"name": "example.com", "vpc_id": vpc}, {"name": "corp.internal", "private": true, "vpc_id": "vpc-00000000000000000"},
	} {
		if r := c.Do("POST", "/api/v1/route53/zones", bad); r.Status != 400 && r.Status != 404 {
			t.Errorf("%v: %d %s", bad, r.Status, r.Body)
		}
	}
	priv := c.Must(201, "POST", "/api/v1/route53/zones", map[string]any{"name": "Corp.Internal.", "private": true, "vpc_id": vpc})
	pid := priv["zone_id"].(string)
	if priv["name"] != "corp.internal" || priv["record_count"].(float64) != 2 || !strings.HasPrefix(pid, "Z") {
		t.Fatalf("zone: %v", priv)
	}
	pub := c.Must(201, "POST", "/api/v1/route53/zones", map[string]any{"name": "example.com"})
	zid := pub["zone_id"].(string)
	recs := "/api/v1/route53/zones/" + pid + "/records"
	for _, bad := range []map[string]any{
		{"name": "db.other.com", "type": "A", "ttl": 60, "values": []string{"10.0.1.5"}},
		{"name": "db.corp.internal", "type": "A", "ttl": 60, "values": []string{"not-an-ip"}},
		{"name": "db.corp.internal", "type": "A", "ttl": 9999999, "values": []string{"10.0.1.5"}},
		{"name": "db.corp.internal", "type": "AAAA", "ttl": 60, "values": []string{"10.0.1.5"}},
		{"name": "db.corp.internal", "type": "MX", "ttl": 60, "values": []string{"10 mail"}},
		{"name": "corp.internal", "type": "CNAME", "ttl": 60, "values": []string{"x.corp.internal"}},
		{"name": "db.corp.internal", "type": "A", "ttl": 60, "values": []string{}},
	} {
		if r := c.Do("POST", recs, bad); r.Status != 400 {
			t.Errorf("record %v: %d %s", bad, r.Status, r.Body)
		}
	}
	c.Must(201, "POST", recs, map[string]any{"name": "db.corp.internal", "type": "A", "ttl": 60, "values": []string{"10.0.1.5"}})
	c.Must(201, "POST", recs, map[string]any{"name": "api.corp.internal", "type": "CNAME", "ttl": 30, "values": []string{"db.corp.internal"}})
	c.Must(201, "POST", recs, map[string]any{"name": "@", "type": "TXT", "ttl": 300, "values": []string{"v=spf1 -all"}})
	if r := c.Do("POST", recs, map[string]any{"name": "api.corp.internal", "type": "A", "ttl": 60, "values": []string{"10.0.1.6"}}); r.Status != 409 {
		t.Fatalf("A alongside CNAME: %d", r.Status)
	}
	l := c.Must(200, "GET", recs, nil)
	if l["total"].(float64) != 5 {
		t.Fatalf("records: %v", l)
	}

	resolve := func(name, vpcID string) map[string]any {
		return c.Must(200, "GET", "/api/v1/vpc/dns/resolve?name="+name+"&vpc_id="+vpcID, nil)
	}
	r := resolve("db.corp.internal", vpc)
	if r["source"] != "route53" || r["answers"].([]any)[0].(map[string]any)["value"] != "10.0.1.5" {
		t.Fatalf("private A: %v", r)
	}
	if r := resolve("db.corp.internal", other); r["status"] != "NXDOMAIN" {
		t.Fatalf("private zone must not answer other VPCs: %v", r)
	}
	r = resolve("api.corp.internal.", vpc)
	ans := r["answers"].([]any)
	if len(ans) != 2 || ans[0].(map[string]any)["type"] != "CNAME" || ans[1].(map[string]any)["value"] != "10.0.1.5" {
		t.Fatalf("CNAME one hop: %v", r)
	}
	c.Must(201, "POST", "/api/v1/route53/zones/"+zid+"/records", map[string]any{"name": "www.example.com", "type": "A", "ttl": 60, "values": []string{"54.0.0.10"}})
	if r := resolve("www.example.com", ""); r["answers"].([]any)[0].(map[string]any)["value"] != "54.0.0.10" {
		t.Fatalf("public zone: %v", r)
	}
	if r := h.Signup("bob").Do("GET", "/api/v1/vpc/dns/resolve?name=www.example.com", nil); !strings.Contains(string(r.Body), "NXDOMAIN") {
		t.Fatalf("zones are account-scoped: %s", r.Body)
	}

	if r := c.Do("DELETE", "/api/v1/route53/zones/"+pid, nil); r.Status != 409 {
		t.Fatalf("delete non-empty zone: %d", r.Status)
	}
	for _, x := range [][2]string{{"db.corp.internal", "A"}, {"api.corp.internal", "CNAME"}, {"corp.internal", "TXT"}} {
		c.Must(204, "DELETE", recs+"?name="+x[0]+"&type="+x[1], nil)
	}
	if r := c.Do("DELETE", recs+"?name=corp.internal&type=NS", nil); r.Status != 400 {
		t.Fatalf("delete NS: %d", r.Status)
	}
	c.Must(204, "DELETE", "/api/v1/route53/zones/"+pid, nil)
	if h.Engine.Count("hosted_zone") != 1 {
		t.Fatal("zone deregistered from engine")
	}
}

func TestNotImplementedAndReadOnlyIAM(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	for _, p := range []string{"/api/v1/lambda/functions", "/api/v1/rds", "/api/v1/cloudformation/stacks/x"} {
		r := c.Do("GET", p, nil)
		if r.Status != 501 || r.ErrCode() != "NotImplemented" {
			t.Fatalf("%s: %d %s", p, r.Status, r.Body)
		}
	}
	if r := c.Do("POST", "/api/v1/lambda/functions", map[string]string{}); r.Status != 501 {
		t.Fatalf("POST lambda: %d", r.Status)
	}
	if r := h.Anon().Do("GET", "/api/v1/lambda/functions", nil); r.Status != 401 {
		t.Fatalf("unauthenticated: %d", r.Status)
	}

	c.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "jobs"})
	ro := c.IAMUser("viewer", "ReadOnlyAccess")
	ro.Must(200, "GET", "/api/v1/sqs/queues", nil)
	ro.Must(200, "GET", "/api/v1/dynamodb/tables", nil)
	for _, x := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/sqs/queues", map[string]any{"name": "x"}},
		{"POST", "/api/v1/sqs/queues/jobs/messages", map[string]any{"body": "x"}},
		{"POST", "/api/v1/sns/topics", map[string]any{"name": "x"}},
		{"POST", "/api/v1/secrets", map[string]any{"name": "x", "secret_string": "y"}},
		{"POST", "/api/v1/dynamodb/tables", map[string]any{"table_name": "xyz", "partition_key": map[string]string{"name": "id", "type": "S"}}},
		{"POST", "/api/v1/route53/zones", map[string]any{"name": "example.com"}},
		{"DELETE", "/api/v1/sqs/queues/jobs", nil},
	} {
		if r := ro.Do(x.method, x.path, x.body); r.Status != 403 || r.ErrCode() != "AccessDenied" {
			t.Errorf("%s %s as read-only: %d %s", x.method, x.path, r.Status, r.Body)
		}
	}
	full := c.IAMUser("ops", "AdministratorAccess")
	full.Must(201, "POST", "/api/v1/sqs/queues", map[string]any{"name": "ops-q"})
}
