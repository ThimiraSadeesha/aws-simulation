package policy

import "testing"

func mustParse(t *testing.T, s string) *Document {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const s3Read = `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":["s3:Get*","s3:List*"],"Resource":"*"}}`

func TestWildcard(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"*", "anything", true},
		{"s3:Get*", "s3:GetObject", true},
		{"s3:Get*", "s3:PutObject", false},
		{"arn:cloudsim:s3:::b/*", "arn:cloudsim:s3:::b/x/y", true},
		{"arn:cloudsim:s3:::b/?", "arn:cloudsim:s3:::b/xy", false},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
	}
	for _, c := range cases {
		if got := wildcardMatch(c.p, c.s, false); got != c.want {
			t.Errorf("%q vs %q = %v", c.p, c.s, got)
		}
	}
	if !wildcardMatch("S3:getobject", "s3:GetObject", true) {
		t.Error("actions must be case-insensitive")
	}
}

func TestEvaluateAllowDenyImplicit(t *testing.T) {
	allow := Named{"read", mustParse(t, s3Read)}
	deny := Named{"deny-secret", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Sid":"NoSecret","Effect":"Deny","Action":"s3:*","Resource":"arn:cloudsim:s3:::secret*"}]}`)}
	req := Request{Action: "s3:GetObject", Resource: "arn:cloudsim:s3:::public/a.txt"}
	if r := Evaluate(req, []Named{allow, deny}, nil); r.Decision != Allowed {
		t.Fatalf("want allowed, got %v", r)
	}
	req.Resource = "arn:cloudsim:s3:::secret-bucket/a"
	if r := Evaluate(req, []Named{allow, deny}, nil); r.Decision != ExplicitDeny || r.Matched[0].Sid != "NoSecret" {
		t.Fatalf("want explicit deny, got %v", r)
	}
	req.Action = "s3:PutObject"
	req.Resource = "arn:cloudsim:s3:::public/a.txt"
	if r := Evaluate(req, []Named{allow}, nil); r.Decision != ImplicitDeny {
		t.Fatalf("want implicit deny, got %v", r)
	}
}

func TestNotActionNotResource(t *testing.T) {
	p := Named{"p", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotAction":"iam:*","NotResource":"arn:x:protected"}]}`)}
	if !Evaluate(Request{Action: "ec2:RunInstances", Resource: "arn:x:other"}, []Named{p}, nil).Allowed() {
		t.Fatal("NotAction should allow ec2")
	}
	if Evaluate(Request{Action: "iam:CreateUser", Resource: "arn:x:other"}, []Named{p}, nil).Allowed() {
		t.Fatal("NotAction should exclude iam")
	}
	if Evaluate(Request{Action: "ec2:RunInstances", Resource: "arn:x:protected"}, []Named{p}, nil).Allowed() {
		t.Fatal("NotResource should exclude protected")
	}
}

func TestConditionsAndVariables(t *testing.T) {
	p := Named{"home", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"arn:cloudsim:s3:::home/${aws:username}/*"},
	 {"Effect":"Allow","Action":"ec2:*","Resource":"*","Condition":{"StringEquals":{"cloudsim:RequestedRegion":"local-1"}}}]}`)}
	ctx := map[string]string{"cloudsim:username": "bob", "cloudsim:RequestedRegion": "local-1"}
	if !Evaluate(Request{Action: "s3:GetObject", Resource: "arn:cloudsim:s3:::home/bob/f", Context: ctx}, []Named{p}, nil).Allowed() {
		t.Fatal("variable substitution failed")
	}
	if Evaluate(Request{Action: "s3:GetObject", Resource: "arn:cloudsim:s3:::home/alice/f", Context: ctx}, []Named{p}, nil).Allowed() {
		t.Fatal("other user's home must be denied")
	}
	if !Evaluate(Request{Action: "ec2:StartInstances", Resource: "*", Context: ctx}, []Named{p}, nil).Allowed() {
		t.Fatal("condition should match")
	}
	ctx["cloudsim:RequestedRegion"] = "elsewhere"
	if Evaluate(Request{Action: "ec2:StartInstances", Resource: "*", Context: ctx}, []Named{p}, nil).Allowed() {
		t.Fatal("condition should fail")
	}
}

func TestResourcePolicyPrincipal(t *testing.T) {
	bucket := Named{"bucket-policy", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"CloudSim":"arn:cloudsim:iam::111122223333:user/bob"},"Action":"s3:GetObject","Resource":"arn:cloudsim:s3:::b/*"}]}`)}
	bob := Request{PrincipalARN: "arn:cloudsim:iam::111122223333:user/bob", AccountID: "111122223333", Action: "s3:GetObject", Resource: "arn:cloudsim:s3:::b/k"}
	if !Evaluate(bob, nil, []Named{bucket}).Allowed() {
		t.Fatal("bucket policy should allow bob")
	}
	eve := bob
	eve.PrincipalARN = "arn:cloudsim:iam::111122223333:user/eve"
	if Evaluate(eve, nil, []Named{bucket}).Allowed() {
		t.Fatal("bucket policy must not allow eve")
	}
	denyAll := Named{"deny", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:cloudsim:s3:::b/*"}]}`)}
	if Evaluate(bob, []Named{{"admin", mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)}}, []Named{denyAll}).Allowed() {
		t.Fatal("resource deny must override identity allow")
	}
}

func TestTrust(t *testing.T) {
	trust := mustParse(t, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"CloudSim":"arn:cloudsim:iam::111122223333:root"},"Action":"sts:AssumeRole"}]}`)
	if !EvaluateTrust(trust, "arn:cloudsim:iam::111122223333:user/bob", "111122223333") {
		t.Fatal("account root principal should trust users of the account")
	}
	if EvaluateTrust(trust, "arn:cloudsim:iam::999999999999:user/x", "999999999999") {
		t.Fatal("other account must not be trusted")
	}
}

func TestValidate(t *testing.T) {
	if errs := Validate(s3Read, Identity); len(errs) != 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	errs := Validate(`{"Version":"1","Statement":[{"Effect":"Maybe","Action":"bad action","Principal":"*"}]}`, Identity)
	if len(errs) < 4 {
		t.Fatalf("expected several errors, got %v", errs)
	}
	if errs := Validate(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.cloudsim.local"},"Action":"sts:AssumeRole","Resource":"*"}]}`, Trust); len(errs) != 1 {
		t.Fatalf("trust with Resource should fail once: %v", errs)
	}
	if errs := Validate(`not json`, Identity); len(errs) != 1 {
		t.Fatal("expected json error")
	}
}

func BenchmarkEvaluate(b *testing.B) {
	var pols []Named
	for i := 0; i < 20; i++ {
		d, _ := Parse(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:Describe*","s3:Get*"],"Resource":"arn:cloudsim:s3:::bucket-*/*"},{"Effect":"Deny","Action":"iam:*","Resource":"*"}]}`)
		pols = append(pols, Named{"p", d})
	}
	req := Request{Action: "s3:GetObject", Resource: "arn:cloudsim:s3:::bucket-42/some/key"}
	for i := 0; i < b.N; i++ {
		Evaluate(req, pols, nil)
	}
}
