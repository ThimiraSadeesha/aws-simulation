package tests

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudsim/backend/internal/testutil"
)

func instanceState(c *testutil.Client, id string) string {
	m := c.Do("GET", "/api/v1/ec2/instances/"+id, nil).Map()
	s, _ := m["state"].(string)
	return s
}

func TestEC2LifecycleWithDefaultVPC(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	out := c.Must(201, "POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "name": "web",
		"user_data": "#!/bin/bash\nyum install -y httpd\nsystemctl start httpd\n"})
	inst := out["instances"].([]any)[0].(map[string]any)
	id := inst["instance_id"].(string)
	if !strings.HasPrefix(id, "i-") || inst["private_ip"] == "" || inst["public_ip"] == "" {
		t.Fatalf("instance: %v", inst)
	}
	if !strings.HasPrefix(inst["private_ip"].(string), "172.31.") {
		t.Fatalf("default VPC addressing expected: %v", inst["private_ip"])
	}
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "running" }, "instance should become running via engine events")
	c.Must(202, "POST", "/api/v1/ec2/instances/"+id+"/stop", nil)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "stopped" }, "stopped")
	r := c.Do("POST", "/api/v1/ec2/instances/"+id+"/stop", nil)
	if r.Status != 409 || r.ErrCode() != "InvalidStateTransition" {
		t.Fatalf("stop twice: %d %s", r.Status, r.Body)
	}
	c.Must(202, "POST", "/api/v1/ec2/instances/"+id+"/start", nil)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "running" }, "restarted")
	co := c.Must(200, "GET", "/api/v1/ec2/instances/"+id+"/console-output", nil)
	if !strings.Contains(co["output"].(string), "Started httpd.service") {
		t.Fatalf("console output should include sandboxed user-data: %s", co["output"])
	}
	md := c.Must(200, "GET", "/api/v1/ec2/instances/"+id+"/metadata", nil)
	if md["instance-id"] != id || md["tags/instance/Name"] != "web" {
		t.Fatalf("metadata: %v", md)
	}
	c.Must(200, "PUT", "/api/v1/ec2/instances/"+id+"/tags", map[string]any{"tags": map[string]string{"Name": "web", "env": "dev"}})
	l := c.Must(200, "GET", "/api/v1/ec2/instances?tag:env=dev", nil)
	if l["total"].(float64) != 1 {
		t.Fatalf("tag filter: %v", l)
	}
	c.Must(202, "POST", "/api/v1/ec2/instances/"+id+"/terminate", nil)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "terminated" }, "terminated")
	got := c.Must(200, "GET", "/api/v1/ec2/instances/"+id, nil)
	if got["public_ip"] != "" {
		t.Fatal("public ip should be released on termination")
	}
	if r := c.Do("POST", "/api/v1/ec2/instances/"+id+"/start", nil); r.Status != 409 {
		t.Fatalf("start terminated: %d", r.Status)
	}
	hist := c.Must(200, "GET", "/api/v1/ec2/instances/"+id+"/history", nil)
	if len(hist["items"].([]any)) < 6 {
		t.Fatalf("history should record transitions: %v", hist)
	}
}

func TestEC2Validation(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	cases := []map[string]any{
		{"image_id": "ami-nope", "instance_type": "t3.micro"},
		{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "z9.mega"},
		{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "count": 50},
		{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "key_name": "missing"},
		{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "subnet_id": "subnet-000"},
	}
	for i, in := range cases {
		if r := c.Do("POST", "/api/v1/ec2/instances", in); r.Status < 400 || r.Status > 404 {
			t.Errorf("case %d: expected client error, got %d %s", i, r.Status, r.Body)
		}
	}
	k := c.Must(201, "POST", "/api/v1/ec2/key-pairs", map[string]string{"name": "dev"})
	if !strings.Contains(k["private_key_pem"].(string), "OPENSSH PRIVATE KEY") || !strings.HasPrefix(k["fingerprint"].(string), "SHA256:") {
		t.Fatalf("key pair: %v", k)
	}
	kl := c.Must(200, "GET", "/api/v1/ec2/key-pairs", nil)
	if strings.Contains(fmt.Sprint(kl), "PRIVATE KEY") {
		t.Fatal("private key must never be listed")
	}
}

func TestVPCBuildPublicPrivate(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	vpc := c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16", "name": "lab"})
	vid := vpc["vpc_id"].(string)
	if r := c.Do("POST", "/api/v1/vpc/subnets", map[string]string{"vpc_id": vid, "cidr_block": "10.1.0.0/24"}); r.Status != 400 {
		t.Fatalf("subnet outside VPC: %d", r.Status)
	}
	pub := c.Must(201, "POST", "/api/v1/vpc/subnets", map[string]any{"vpc_id": vid, "cidr_block": "10.0.1.0/24", "name": "public", "map_public_ip_on_launch": true})
	if pub["available_ip_count"].(float64) != 251 {
		t.Fatalf("available IPs: %v", pub["available_ip_count"])
	}
	if r := c.Do("POST", "/api/v1/vpc/subnets", map[string]string{"vpc_id": vid, "cidr_block": "10.0.1.128/25"}); r.Status != 409 {
		t.Fatalf("overlapping subnet: %d", r.Status)
	}
	priv := c.Must(201, "POST", "/api/v1/vpc/subnets", map[string]string{"vpc_id": vid, "cidr_block": "10.0.2.0/24", "name": "private"})
	igw := c.Must(201, "POST", "/api/v1/vpc/internet-gateways", map[string]string{"name": "igw"})
	gid := igw["internet_gateway_id"].(string)
	rt := c.Must(201, "POST", "/api/v1/vpc/route-tables", map[string]string{"vpc_id": vid, "name": "public-rt"})
	rtid := rt["route_table_id"].(string)
	if r := c.Do("POST", "/api/v1/vpc/route-tables/"+rtid+"/routes", map[string]string{"destination_cidr": "0.0.0.0/0", "target_id": gid}); r.Status != 400 {
		t.Fatalf("route to detached igw: %d", r.Status)
	}
	c.Must(200, "POST", "/api/v1/vpc/internet-gateways/"+gid+"/attach", map[string]string{"vpc_id": vid})
	c.Must(201, "POST", "/api/v1/vpc/route-tables/"+rtid+"/routes", map[string]string{"destination_cidr": "0.0.0.0/0", "target_id": gid})
	c.Must(200, "POST", "/api/v1/vpc/route-tables/"+rtid+"/associations", map[string]string{"subnet_id": pub["subnet_id"].(string)})
	p := c.Must(200, "GET", "/api/v1/vpc/subnets/"+pub["subnet_id"].(string), nil)
	q := c.Must(200, "GET", "/api/v1/vpc/subnets/"+priv["subnet_id"].(string), nil)
	if p["is_public"] != true || q["is_public"] != false {
		t.Fatalf("public/private detection: %v / %v", p["is_public"], q["is_public"])
	}
	nat := c.Must(201, "POST", "/api/v1/vpc/nat-gateways", map[string]string{"subnet_id": pub["subnet_id"].(string)})
	nid := nat["nat_gateway_id"].(string)
	h.Eventually(3*time.Second, func() bool {
		return c.Do("GET", "/api/v1/vpc/nat-gateways/"+nid, nil).Map()["state"] == "available"
	}, "NAT available")
	main := c.Must(200, "GET", "/api/v1/vpc/route-tables?vpc_id="+vid+"&is_main=true", nil)
	mainID := main["items"].([]any)[0].(map[string]any)["route_table_id"].(string)
	c.Must(201, "POST", "/api/v1/vpc/route-tables/"+mainID+"/routes", map[string]string{"destination_cidr": "0.0.0.0/0", "target_id": nid})
	sg := c.Must(201, "POST", "/api/v1/vpc/security-groups", map[string]string{"group_name": "web", "description": "web", "vpc_id": vid})
	sgid := sg["group_id"].(string)
	c.Must(201, "POST", "/api/v1/vpc/security-groups/"+sgid+"/rules", map[string]any{"direction": "ingress", "protocol": "tcp", "from_port": 80, "to_port": 80, "cidr": "0.0.0.0/0"})
	if r := c.Do("POST", "/api/v1/vpc/security-groups/"+sgid+"/rules", map[string]any{"direction": "ingress", "protocol": "tcp", "from_port": 80, "to_port": 80, "cidr": "0.0.0.0/0"}); r.Status != 409 {
		t.Fatalf("duplicate rule: %d", r.Status)
	}
	if r := c.Do("POST", "/api/v1/vpc/security-groups/"+sgid+"/rules", map[string]any{"direction": "ingress", "protocol": "tcp", "from_port": 90, "to_port": 80, "cidr": "0.0.0.0/0"}); r.Status != 400 {
		t.Fatalf("bad ports: %d", r.Status)
	}
	in := c.Must(201, "POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.micro", "subnet_id": pub["subnet_id"], "security_group_ids": []string{sgid}})
	inst := in["instances"].([]any)[0].(map[string]any)
	if inst["private_ip"] != "10.0.1.5" || nat["private_ip"] != "10.0.1.4" {
		t.Fatalf("addresses are allocated from .4 upward (AWS reserves 4): nat=%v inst=%v", nat["private_ip"], inst["private_ip"])
	}
	topo := c.Must(200, "GET", "/api/v1/vpc/vpcs/"+vid+"/topology", nil)
	if len(topo["subnets"].([]any)) != 2 || len(topo["instances"].([]any)) != 1 || len(topo["nat_gateways"].([]any)) != 1 {
		t.Fatalf("topology: %v", topo)
	}
	reach := c.Must(200, "POST", "/api/v1/vpc/reachability", map[string]any{"source": "internet", "destination": inst["instance_id"], "protocol": "tcp", "port": 80})
	if _, ok := reach["reachable"]; !ok {
		t.Fatalf("reachability passthrough: %v", reach)
	}
	dns := c.Must(200, "GET", "/api/v1/vpc/dns/resolve?name="+inst["private_dns"].(string), nil)
	if dns["status"] != "NOERROR" {
		t.Fatalf("dns: %v", dns)
	}
	// dependency checks
	if r := c.Do("DELETE", "/api/v1/vpc/vpcs/"+vid, nil); r.Status != 409 || r.ErrCode() != "DependencyViolation" {
		t.Fatalf("delete vpc with deps: %d %s", r.Status, r.Body)
	}
	if r := c.Do("DELETE", "/api/v1/vpc/security-groups/"+sgid, nil); r.Status != 409 {
		t.Fatalf("delete in-use sg: %d", r.Status)
	}
	if r := c.Do("POST", "/api/v1/vpc/internet-gateways/"+gid+"/detach", nil); r.Status != 409 {
		t.Fatalf("detach igw with public instances: %d %s", r.Status, r.Body)
	}
}

func TestConcurrentLaunches(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	sn := c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.9.0.0/16"})
	sub := c.Must(201, "POST", "/api/v1/vpc/subnets", map[string]string{"vpc_id": sn["vpc_id"].(string), "cidr_block": "10.9.0.0/24"})
	var wg sync.WaitGroup
	errs := make(chan string, 20)
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := c.Do("POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano", "subnet_id": sub["subnet_id"]})
			if r.Status != 201 {
				errs <- fmt.Sprintf("%d %s", r.Status, r.Body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	l := c.Must(200, "GET", "/api/v1/ec2/instances?page_size=100", nil)
	seen := map[string]bool{}
	for _, it := range l["items"].([]any) {
		ip := it.(map[string]any)["private_ip"].(string)
		if seen[ip] {
			t.Fatalf("duplicate private IP allocated under concurrency: %s", ip)
		}
		seen[ip] = true
	}
	if len(seen) != 15 {
		t.Fatalf("expected 15 instances, got %d", len(seen))
	}
}

func TestQuotaFromEngine(t *testing.T) {
	h := testutil.New(t)
	h.Engine.Quota["instance"] = 2
	c := h.Signup("alice")
	for i := 0; i < 2; i++ {
		c.Must(201, "POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano"})
	}
	r := c.Do("POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano"})
	if r.Status != 429 || r.ErrCode() != "QuotaExceeded" {
		t.Fatalf("quota: %d %s", r.Status, r.Body)
	}
	l := c.Must(200, "GET", "/api/v1/ec2/instances", nil)
	if l["total"].(float64) != 2 {
		t.Fatalf("rejected launch must not persist: %v", l["total"])
	}
}

func TestEngineRestartRecovery(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	out := c.Must(201, "POST", "/api/v1/ec2/instances", map[string]any{"image_id": "ami-0c1a2b3c4d5e6f701", "instance_type": "t3.nano"})
	id := out["instances"].([]any)[0].(map[string]any)["instance_id"].(string)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "running" }, "running")
	c.Must(202, "POST", "/api/v1/ec2/instances/"+id+"/stop", nil)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "stopped" }, "stopped")
	before := h.Engine.Count("vpc")
	h.Engine.Kill() // engine loses all runtime state
	if h.Engine.Count("instance") != 0 {
		t.Fatal("kill should clear engine")
	}
	h.Eventually(30*time.Second, func() bool { return h.Engine.Count("instance") == 1 && h.Engine.Count("vpc") == before }, "backend should re-register resources after engine restart")
	// persisted state is preserved (stopped stays stopped) and lifecycle continues to work
	if s := instanceState(c, id); s != "stopped" {
		t.Fatalf("state after recovery: %s", s)
	}
	c.Must(202, "POST", "/api/v1/ec2/instances/"+id+"/start", nil)
	h.Eventually(3*time.Second, func() bool { return instanceState(c, id) == "running" }, "running after recovery")
}

func TestListPaginationSortSearch(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	for i := 0; i < 5; i++ {
		c.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": fmt.Sprintf("10.%d.0.0/16", 50+i), "name": fmt.Sprintf("vpc-%c", 'e'-i)})
	}
	p := c.Must(200, "GET", "/api/v1/vpc/vpcs?page=2&page_size=2&sort=name&order=asc", nil)
	items := p["items"].([]any)
	if p["total"].(float64) != 5 || len(items) != 2 || items[0].(map[string]any)["name"] != "vpc-c" {
		t.Fatalf("pagination/sort: %v", p)
	}
	s := c.Must(200, "GET", "/api/v1/vpc/vpcs?q=10.52.", nil)
	if s["total"].(float64) != 1 {
		t.Fatalf("search: %v", s)
	}
}
