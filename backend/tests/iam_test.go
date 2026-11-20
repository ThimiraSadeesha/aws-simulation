package tests

import (
	"strings"
	"testing"
	"time"

	"cloudsim/backend/internal/testutil"
)

func TestIAMPolicyEnforcementViaAccessKeys(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	ro := root.IAMUser("reader", "CloudSimEC2ReadOnlyAccess")
	// read allowed
	ro.Must(200, "GET", "/api/v1/ec2/instances", nil)
	// write denied (implicit)
	r := ro.Do("POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"})
	if r.Status != 403 || r.ErrCode() != "AccessDenied" || !strings.Contains(string(r.Body), "implicitDeny") {
		t.Fatalf("expected implicit deny, got %d %s", r.Status, r.Body)
	}
	// IAM itself denied
	if r := ro.Do("GET", "/api/v1/iam/users", nil); r.Status != 403 {
		t.Fatalf("iam list should be denied: %d", r.Status)
	}
	// inline allow for CreateVpc, then an explicit deny overrides
	root.Must(204, "PUT", "/api/v1/iam/users/reader/inline-policies/vpc", map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": "ec2:CreateVpc", "Resource": "*"}}})
	time.Sleep(10 * time.Millisecond)
	ro.Must(201, "POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.0.0.0/16"})
	root.Must(204, "PUT", "/api/v1/iam/users/reader/inline-policies/deny", map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Deny", "Action": "ec2:Create*", "Resource": "*"}}})
	r = ro.Do("POST", "/api/v1/vpc/vpcs", map[string]string{"cidr_block": "10.2.0.0/16"})
	if r.Status != 403 || !strings.Contains(string(r.Body), "explicitDeny") {
		t.Fatalf("expected explicit deny: %d %s", r.Status, r.Body)
	}
	// denied calls are audited
	h.Srv.FlushAudit()
	a := root.Must(200, "GET", "/api/v1/audit?action=ec2:CreateVpc", nil)
	found := false
	for _, it := range a["items"].([]any) {
		e := it.(map[string]any)
		if e["result"] == "denied" && e["principal"] == "iam-user/reader" {
			found = true
		}
	}
	if !found {
		t.Fatalf("denied request not audited: %v", a)
	}
}

func TestInvalidAccessKey(t *testing.T) {
	h := testutil.New(t)
	h.Signup("alice")
	c := h.Anon()
	r := c.Do("GET", "/api/v1/ec2/instances", nil, "Authorization", "CloudSim AKIAFAKE:nope")
	if r.Status != 401 {
		t.Fatalf("fake key: %d", r.Status)
	}
	r = c.Do("GET", "/api/v1/ec2/instances", nil, "Authorization", "Bearer x")
	if r.Status != 401 {
		t.Fatalf("bad scheme: %d", r.Status)
	}
}

func TestInactiveAndDeletedKeys(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	u := root.IAMUser("bob", "AdministratorAccess")
	u.Must(200, "GET", "/api/v1/iam/users", nil)
	root.Must(200, "PATCH", "/api/v1/iam/users/bob/access-keys/"+u.KeyID, map[string]string{"status": "Inactive"})
	if r := u.Do("GET", "/api/v1/iam/users", nil); r.Status != 401 {
		t.Fatalf("inactive key: %d", r.Status)
	}
	root.Must(204, "DELETE", "/api/v1/iam/users/bob/access-keys/"+u.KeyID, nil)
	if r := u.Do("GET", "/api/v1/iam/users", nil); r.Status != 401 {
		t.Fatalf("deleted key: %d", r.Status)
	}
}

func TestGroupPoliciesApply(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	u := root.IAMUser("carol")
	if r := u.Do("GET", "/api/v1/ec2/instances", nil); r.Status != 403 {
		t.Fatalf("no policy: %d", r.Status)
	}
	root.Must(201, "POST", "/api/v1/iam/groups", map[string]string{"group_name": "ops"})
	root.Must(204, "POST", "/api/v1/iam/groups/ops/attached-policies/CloudSimEC2ReadOnlyAccess", nil)
	root.Must(204, "POST", "/api/v1/iam/groups/ops/members/carol", nil)
	u.Must(200, "GET", "/api/v1/ec2/instances", nil)
	d := root.Must(200, "GET", "/api/v1/iam/users/carol", nil)
	if len(d["groups"].([]any)) != 1 {
		t.Fatalf("user detail groups: %v", d)
	}
	if r := root.Do("DELETE", "/api/v1/iam/groups/ops", nil); r.Status != 409 {
		t.Fatalf("delete non-empty group: %d", r.Status)
	}
}

func TestPolicyValidationAndSimulation(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	r := root.Do("POST", "/api/v1/iam/policies", map[string]any{"policy_name": "bad", "policy_document": map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Perhaps", "Action": "s3:*"}}}})
	if r.Status != 400 || r.ErrCode() != "MalformedPolicyDocument" {
		t.Fatalf("malformed policy: %d %s", r.Status, r.Body)
	}
	root.Must(201, "POST", "/api/v1/iam/policies", map[string]any{"policy_name": "S3Home", "policy_document": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"arn:cloudsim:s3:::home/${aws:username}/*"}]}`})
	root.Must(201, "POST", "/api/v1/iam/users", map[string]string{"user_name": "dave"})
	root.Must(204, "POST", "/api/v1/iam/users/dave/attached-policies/S3Home", nil)
	acct := root.AccountID()
	sim := root.Must(200, "POST", "/api/v1/iam/simulate", map[string]string{"principal_arn": "arn:cloudsim:iam::" + acct + ":user/dave", "action": "s3:GetObject", "resource_arn": "arn:cloudsim:s3:::home/dave/notes.txt"})
	if sim["decision"] != "allowed" {
		t.Fatalf("simulate allowed: %v", sim)
	}
	sim = root.Must(200, "POST", "/api/v1/iam/simulate", map[string]string{"principal_arn": "arn:cloudsim:iam::" + acct + ":user/dave", "action": "s3:GetObject", "resource_arn": "arn:cloudsim:s3:::home/erin/notes.txt"})
	if sim["decision"] != "implicitDeny" {
		t.Fatalf("simulate deny: %v", sim)
	}
	if r := root.Do("DELETE", "/api/v1/iam/policies/S3Home", nil); r.Status != 409 {
		t.Fatalf("delete attached policy: %d", r.Status)
	}
	if r := root.Do("PUT", "/api/v1/iam/policies/AdministratorAccess", map[string]any{"Version": "2012-10-17", "Statement": []any{}}); r.Status != 403 {
		t.Fatalf("builtin policy modification: %d", r.Status)
	}
	v := root.Must(200, "POST", "/api/v1/iam/validate-policy", map[string]any{"policy_document": map[string]any{"Version": "2012-10-17"}})
	if v["valid"] != false {
		t.Fatal("expected invalid")
	}
}

func TestAssumeRole(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	acct := root.AccountID()
	u := root.IAMUser("eve") // no permissions of her own
	trust := map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": map[string]any{"CloudSim": "arn:cloudsim:iam::" + acct + ":user/eve"}, "Action": "sts:AssumeRole"}}}
	root.Must(201, "POST", "/api/v1/iam/roles", map[string]any{"role_name": "Admin", "assume_role_policy_document": trust})
	root.Must(204, "POST", "/api/v1/iam/roles/Admin/attached-policies/AdministratorAccess", nil)
	roleARN := "arn:cloudsim:iam::" + acct + ":role/Admin"
	// eve needs sts:AssumeRole permission herself
	if r := u.Do("POST", "/api/v1/sts/assume-role", map[string]string{"role_arn": roleARN, "role_session_name": "s1"}); r.Status != 403 {
		t.Fatalf("assume without permission: %d %s", r.Status, r.Body)
	}
	root.Must(204, "PUT", "/api/v1/iam/users/eve/inline-policies/assume", map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": "sts:AssumeRole", "Resource": roleARN}}})
	time.Sleep(10 * time.Millisecond)
	out := u.Must(200, "POST", "/api/v1/sts/assume-role", map[string]string{"role_arn": roleARN, "role_session_name": "s1"})
	cr := out["credentials"].(map[string]any)
	tmp := h.Anon()
	tmp.KeyID, tmp.Secret = cr["access_key_id"].(string), cr["secret_access_key"].(string)
	if r := tmp.Do("GET", "/api/v1/iam/users", nil); r.Status != 401 {
		t.Fatalf("temporary creds without session token must fail: %d", r.Status)
	}
	tmp.Session = cr["session_token"].(string)
	tmp.Must(200, "GET", "/api/v1/iam/users", nil)
	id := tmp.Must(200, "GET", "/api/v1/sts/caller-identity", nil)
	if !strings.Contains(id["arn"].(string), "assumed-role/Admin/s1") {
		t.Fatalf("caller identity: %v", id)
	}
	// a role whose trust policy does not include frank cannot be assumed by frank
	f := root.IAMUser("frank", "AdministratorAccess")
	if r := f.Do("POST", "/api/v1/sts/assume-role", map[string]string{"role_arn": roleARN, "role_session_name": "s2"}); r.Status != 403 {
		t.Fatalf("untrusted principal: %d %s", r.Status, r.Body)
	}
}
