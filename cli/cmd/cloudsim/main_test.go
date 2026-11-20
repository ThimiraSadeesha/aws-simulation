package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloudsim/cli/pkg/config"
)

func TestConfigureAndHTTPRoundTrip(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"account":"123456789012","arn":"arn:cloudsim:iam::123456789012:user/bob","user_id":"bob"}`))
	}))
	defer srv.Close()
	t.Setenv("CLOUDSIM_ENDPOINT", "")
	t.Setenv("CLOUDSIM_ACCESS_KEY_ID", "")
	store := config.Store{Dir: t.TempDir()}
	var out, errw bytes.Buffer
	in := strings.NewReader("AKIATEST\nsecret123\n" + srv.URL + "\ntable\n")
	if code := run(context.Background(), []string{"configure", "--profile", "dev"}, in, &out, &errw, store); code != 0 {
		t.Fatalf("configure: %d %s", code, errw.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"sts", "get-caller-identity", "--profile", "dev", "--output", "json"}, nil, &out, &errw, store); code != 0 {
		t.Fatalf("call: %d %s", code, errw.String())
	}
	if gotAuth != "CloudSim AKIATEST:secret123" || gotPath != "/api/v1/sts/caller-identity" || !strings.Contains(out.String(), "user/bob") {
		t.Fatalf("auth=%q path=%q out=%s", gotAuth, gotPath, out.String())
	}
	out.Reset()
	run(context.Background(), []string{"configure", "list", "--profile", "dev"}, nil, &out, &errw, store)
	if !strings.Contains(out.String(), "****TEST") || strings.Contains(out.String(), "secret123") {
		t.Fatalf("list must mask: %s", out.String())
	}
	errw.Reset()
	if code := run(context.Background(), []string{"ec2", "describe-instances", "--profile", "nobody"}, nil, &out, &errw, store); code == 0 || !strings.Contains(errw.String(), "Unable to locate credentials") {
		t.Fatalf("missing creds: %s", errw.String())
	}
}

func TestResolvePrecedence(t *testing.T) {
	s := config.Store{Dir: t.TempDir()}
	_ = s.Set("default", "endpoint_url", "http://file:1")
	_ = s.Set("default", "access_key_id", "AKFILE")
	env := map[string]string{"CLOUDSIM_ENDPOINT": "http://env:2", "CLOUDSIM_ACCESS_KEY_ID": "AKENV", "CLOUDSIM_SECRET_ACCESS_KEY": "s"}
	p := s.Resolve("", "", func(k string) string { return env[k] })
	if p.Endpoint != "http://env:2" || p.AccessKeyID != "AKENV" {
		t.Fatalf("%+v", p)
	}
	p = s.Resolve("", "http://flag:3", func(string) string { return "" })
	if p.Endpoint != "http://flag:3" || p.AccessKeyID != "AKFILE" {
		t.Fatalf("%+v", p)
	}
	if ini := config.ParseINI("[default]\na = 1\n# c\n[profile x]\nb=2\n"); ini["profile x"]["b"] != "2" || ini["default"]["a"] != "1" {
		t.Fatalf("%v", ini)
	}
}
