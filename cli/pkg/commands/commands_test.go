package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type call struct {
	Method, Path string
	Body         string
	Headers      map[string]string
}

type fakeTransport struct {
	calls []call
	resp  func(c call) (int, string)
}

func (f *fakeTransport) Do(_ context.Context, method, path string, body []byte, headers map[string]string) (int, []byte, map[string]string, error) {
	c := call{method, path, string(body), headers}
	f.calls = append(f.calls, c)
	if f.resp == nil {
		return 200, []byte(`{}`), nil, nil
	}
	st, b := f.resp(c)
	return st, []byte(b), map[string]string{"Content-Type": "application/json"}, nil
}

type memFS map[string][]byte

func (m memFS) ReadFile(n string) ([]byte, error) {
	if b, ok := m[n]; ok {
		return b, nil
	}
	return nil, errors.New("no such file")
}
func (m memFS) WriteFile(n string, d []byte) error { m[n] = d; return nil }
func (m memFS) IsDir(n string) bool {
	for k := range m {
		if strings.HasPrefix(k, n+"/") {
			return true
		}
	}
	return false
}
func (m memFS) Walk(root string, fn func(string) error) error {
	for k := range m {
		if strings.HasPrefix(k, root+"/") {
			if err := fn(strings.TrimPrefix(k, root+"/")); err != nil {
				return err
			}
		}
	}
	return nil
}

func runCmd(t *testing.T, ft *fakeTransport, fs LocalFS, line string) (int, string) {
	t.Helper()
	args, err := Split(line)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := Run(context.Background(), &Env{Transport: ft, Out: &out, FS: fs}, args)
	return code, out.String()
}

const instancesJSON = `{"items":[{"instance_id":"i-1","name":"web","state":"running","instance_type":"t3.micro","tags":{"Name":"web"},"private_ip":"10.0.0.5"},
{"instance_id":"i-2","name":"db","state":"stopped","instance_type":"t3.small","tags":{"Name":"db"}}],"total":2,"page":1,"page_size":200}`

func TestDescribeInstancesFiltersAndOutputs(t *testing.T) {
	ft := &fakeTransport{resp: func(c call) (int, string) { return 200, instancesJSON }}
	code, out := runCmd(t, ft, nil, "ec2 describe-instances --filters Name=instance-state-name,Values=running --output json")
	if code != 0 || !strings.Contains(out, "i-1") || strings.Contains(out, "i-2") {
		t.Fatalf("filter: %d %s", code, out)
	}
	if !strings.HasPrefix(ft.calls[0].Path, "/ec2/instances?") || ft.calls[0].Method != "GET" {
		t.Fatalf("path: %+v", ft.calls[0])
	}
	_, out = runCmd(t, ft, nil, "ec2 describe-instances --filters Name=tag:Name,Values=d* --query items[].instance_id --output text")
	if strings.TrimSpace(out) != "i-2" {
		t.Fatalf("query/text: %q", out)
	}
	_, out = runCmd(t, ft, nil, "ec2 describe-instances --instance-ids i-1 --output table")
	if !strings.Contains(out, "| instance_id |") || !strings.Contains(out, "| i-1 ") || strings.Contains(out, "i-2") {
		t.Fatalf("table: %s", out)
	}
	_, out = runCmd(t, ft, nil, "vpc describe-instances --query items[0].state")
	if strings.TrimSpace(out) != `"running"` {
		t.Fatalf("alias + json query: %q", out)
	}
}

func TestRunInstancesBody(t *testing.T) {
	ft := &fakeTransport{resp: func(c call) (int, string) { return 201, `{"instances":[{"instance_id":"i-9","state":"pending"}]}` }}
	fs := memFS{"boot.sh": []byte("#!/bin/bash\nyum install -y httpd\n")}
	code, out := runCmd(t, ft, fs, `ec2 run-instances --image-id ami-1 --count 2 --security-group-ids sg-1 sg-2 --user-data file://boot.sh `+
		`--tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=web},{Key=env,Value=dev}]' --no-associate-public-ip-address`)
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	var b map[string]any
	_ = json.Unmarshal([]byte(ft.calls[0].Body), &b)
	if b["name"] != "web" || b["instance_type"] != "t3.micro" || b["count"].(float64) != 2 || len(b["security_group_ids"].([]any)) != 2 ||
		!strings.Contains(b["user_data"].(string), "httpd") || b["tags"].(map[string]any)["env"] != "dev" || b["associate_public_ip"] != false {
		t.Fatalf("body: %v", b)
	}
	// file:// without filesystem (web terminal) is refused
	code, out = runCmd(t, ft, nil, "ec2 run-instances --image-id ami-1 --user-data file:///etc/passwd")
	if code == 0 || !strings.Contains(out, "not available in the web terminal") {
		t.Fatalf("terminal file access: %d %s", code, out)
	}
}

func TestUsageErrorsAndSuggestions(t *testing.T) {
	ft := &fakeTransport{}
	code, out := runCmd(t, ft, nil, "ec2 describe-instanse")
	if code != ExitUsage || !strings.Contains(out, "describe-instances") {
		t.Fatalf("suggestion: %d %s", code, out)
	}
	code, out = runCmd(t, ft, nil, "ecc describe-instances")
	if code != ExitUsage || !strings.Contains(out, "Did you mean: ec2") {
		t.Fatalf("service suggestion: %s", out)
	}
	code, out = runCmd(t, ft, nil, "ec2 run-instances --instance-type t3.micro")
	if code != ExitUsage || !strings.Contains(out, "--image-id") {
		t.Fatalf("required: %s", out)
	}
	code, out = runCmd(t, ft, nil, "ec2 run-instances --image-id a --cuont 2")
	if code != ExitUsage || !strings.Contains(out, "--count") {
		t.Fatalf("unknown flag suggestion: %s", out)
	}
	code, _ = runCmd(t, ft, nil, "ec2 run-instances --image-id a --count two")
	if code != ExitUsage {
		t.Fatal("int validation")
	}
	if len(ft.calls) != 0 {
		t.Fatalf("no API calls on usage errors: %v", ft.calls)
	}
	_, out = runCmd(t, ft, nil, "--version")
	if !strings.Contains(out, "cloudsim-cli/") {
		t.Fatal(out)
	}
	_, out = runCmd(t, ft, nil, "help")
	if !strings.Contains(out, "simulated") || !strings.Contains(out, "elbv2") {
		t.Fatal(out)
	}
	_, out = runCmd(t, ft, nil, "s3 cp help")
	if !strings.Contains(out, "local://") {
		t.Fatal(out)
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	ft := &fakeTransport{resp: func(c call) (int, string) {
		return 403, `{"error":{"code":"AccessDenied","message":"User: x is not authorized to perform: ec2:RunInstances","request_id":"r"}}`
	}}
	code, out := runCmd(t, ft, nil, "ec2 run-instances --image-id ami-1")
	if code != ExitAPIError || !strings.Contains(out, "An error occurred (AccessDenied) when calling the RunInstances operation") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestS3Commands(t *testing.T) {
	ft := &fakeTransport{resp: func(c call) (int, string) {
		switch {
		case strings.Contains(c.Path, "/objects?"):
			return 200, `{"items":[{"key":"docs/a.txt","size":3,"last_modified":"2026-11-15T10:00:00.000Z","etag":"\"x\""}],"common_prefixes":["docs/sub/"],"total":1}`
		case c.Method == "GET" && strings.Contains(c.Path, "/objects/"):
			return 200, "hello"
		}
		return 200, `{}`
	}}
	fs := memFS{"site/index.html": []byte("<h1>hi</h1>"), "site/css/a.css": []byte("b{}"), "note.txt": []byte("abc")}
	if code, out := runCmd(t, ft, fs, "s3 mb local://my-bucket"); code != 0 || !strings.Contains(out, "make_bucket: my-bucket") {
		t.Fatal(out)
	}
	if ft.calls[0].Path != "/s3/buckets" || !strings.Contains(ft.calls[0].Body, `"my-bucket"`) {
		t.Fatalf("%+v", ft.calls[0])
	}
	if code, out := runCmd(t, ft, fs, "s3 mb s3://real-bucket"); code == 0 || !strings.Contains(out, "local://") {
		t.Fatalf("s3:// must be refused: %s", out)
	}
	runCmd(t, ft, fs, "s3 cp note.txt local://my-bucket/dir/")
	last := ft.calls[len(ft.calls)-1]
	if last.Method != "PUT" || last.Path != "/s3/buckets/my-bucket/objects/dir/note.txt" || last.Body != "abc" || last.Headers["Content-Type"] != "text/plain; charset=utf-8" {
		t.Fatalf("upload: %+v", last)
	}
	n := len(ft.calls)
	runCmd(t, ft, fs, "s3 cp site local://my-bucket/web --recursive")
	if len(ft.calls)-n != 2 {
		t.Fatalf("recursive upload: %v", ft.calls[n:])
	}
	runCmd(t, ft, fs, "s3 cp local://my-bucket/dir/note.txt out.txt")
	if string(fs["out.txt"]) != "hello" {
		t.Fatalf("download: %q", fs["out.txt"])
	}
	_, out := runCmd(t, ft, fs, "s3 ls local://my-bucket/docs/")
	if !strings.Contains(out, "PRE sub/") || !strings.Contains(out, "a.txt") {
		t.Fatalf("ls: %s", out)
	}
	runCmd(t, ft, fs, "s3 rb local://my-bucket --force")
	if l := ft.calls[len(ft.calls)-1]; l.Method != "DELETE" || l.Path != "/s3/buckets/my-bucket?force=true" {
		t.Fatalf("rb: %+v", l)
	}
	runCmd(t, ft, nil, "s3api put-bucket-versioning --bucket b --versioning-configuration Status=Enabled")
	if l := ft.calls[len(ft.calls)-1]; l.Path != "/s3/buckets/b/versioning" || !strings.Contains(l.Body, `"Enabled"`) {
		t.Fatalf("versioning: %+v", l)
	}
	_, out = runCmd(t, ft, nil, "s3api get-object --bucket b --key dir/note.txt -")
	if !strings.Contains(out, "hello") {
		t.Fatal(out)
	}
}

func TestIAMAndELBMapping(t *testing.T) {
	ft := &fakeTransport{resp: func(c call) (int, string) {
		if c.Path == "/iam/simulate" {
			return 200, `{"decision":"implicitDeny","matched_statements":[]}`
		}
		if strings.HasPrefix(c.Path, "/elb/target-groups?") {
			return 200, `{"items":[{"target_group_id":"tg-1","arn":"arn:cloudsim:elasticloadbalancing:local:1:targetgroup/web/tg-1"}],"total":1}`
		}
		return 200, `{}`
	}}
	runCmd(t, ft, nil, "iam attach-user-policy --user-name bob --policy-arn arn:cloudsim:iam::aws:policy/CloudSimEC2FullAccess")
	if l := ft.calls[0]; l.Method != "POST" || l.Path != "/iam/users/bob/attached-policies/CloudSimEC2FullAccess" {
		t.Fatalf("%+v", l)
	}
	_, out := runCmd(t, ft, nil, "iam simulate-principal-policy --policy-source-arn arn:u --action-names s3:GetObject ec2:RunInstances --output text")
	if strings.Count(out, "implicitDeny") != 2 {
		t.Fatalf("simulate: %s", out)
	}
	runCmd(t, ft, nil, "elbv2 register-targets --target-group-arn arn:cloudsim:elasticloadbalancing:local:1:targetgroup/web/tg-1 --targets Id=i-1 Id=i-2,Port=8080")
	l := ft.calls[len(ft.calls)-1]
	if l.Path != "/elb/target-groups/tg-1/targets" || !strings.Contains(l.Body, `"port":8080`) || !strings.Contains(l.Body, `"i-1"`) {
		t.Fatalf("register: %+v", l)
	}
	runCmd(t, ft, nil, "autoscaling put-scaling-policy --auto-scaling-group-name g --policy-name cpu --target-tracking-configuration TargetValue=50")
	if l := ft.calls[len(ft.calls)-1]; l.Path != "/autoscaling/groups/g/policies" || !strings.Contains(l.Body, `"target_value":50`) {
		t.Fatalf("policy: %+v", l)
	}
	runCmd(t, ft, nil, "cloudwatch get-metric-statistics --namespace CloudSim/EC2 --metric-name CPUUtilization --dimensions Name=InstanceId,Value=i-1 --statistics Maximum")
	if l := ft.calls[len(ft.calls)-1]; !strings.Contains(l.Path, "dimension=InstanceId%3Ai-1") || !strings.Contains(l.Path, "stat=Maximum") {
		t.Fatalf("metrics: %+v", l)
	}
}

func TestSplitAndQuery(t *testing.T) {
	got, err := Split(`a "b c" 'd "e"' f\ g ""`)
	if err != nil || len(got) != 5 || got[1] != "b c" || got[2] != `d "e"` || got[3] != "f g" || got[4] != "" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := Split(`"open`); err == nil {
		t.Fatal("unterminated quote")
	}
	v, _ := decode([]byte(`{"items":[{"a":{"b":[1,2]}},{"a":{"b":[3]}}]}`))
	r, _ := Query(v, "items[].a.b[0]")
	if b, _ := json.Marshal(r); string(b) != "[1,3]" {
		t.Fatalf("%s", b)
	}
	r, _ = Query(v, "items[-1].a")
	if b, _ := json.Marshal(r); string(b) != `{"b":[3]}` {
		t.Fatalf("%s", b)
	}
	if _, err := Query(v, "items[x]"); err == nil {
		t.Fatal("bad index")
	}
}

func TestTerminalRestrictionsAndOutputLimit(t *testing.T) {
	var out bytes.Buffer
	ft := &fakeTransport{resp: func(c call) (int, string) { return 200, instancesJSON }}
	code := Run(context.Background(), &Env{Transport: ft, Out: &out, Terminal: true}, []string{"--endpoint-url", "http://x", "ec2", "describe-instances"})
	if code != ExitUsage || len(ft.calls) != 0 {
		t.Fatal("endpoint override must be refused in terminal")
	}
	out.Reset()
	Run(context.Background(), &Env{Transport: ft, Out: &out, MaxOutput: 50}, []string{"ec2", "describe-instances"})
	if !strings.Contains(out.String(), "output truncated") {
		t.Fatalf("limit: %s", out.String())
	}
}

func TestHTTPTransportRefusesRealCloud(t *testing.T) {
	if _, err := NewHTTPTransport("https://ec2.us-east-1.amazonaws.com", "", "", ""); err == nil {
		t.Fatal("real AWS endpoint must be refused")
	}
	if _, err := NewHTTPTransport("http://localhost:8080", "", "", ""); err != nil {
		t.Fatal(err)
	}
}
