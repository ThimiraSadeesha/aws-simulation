package userdata

import "testing"

func TestWebServerScript(t *testing.T) {
	r := Run(`#!/bin/bash
set -e
yum update -y
yum install -y httpd
systemctl enable httpd && systemctl start httpd
echo "<h1>Hello from $(hostname)</h1>" > /var/www/html/index.html
export APP_ENV=prod
echo $APP_ENV >> /etc/app.env
`)
	if !r.Ran || r.ExitCode != 0 {
		t.Fatalf("unexpected result %+v", r)
	}
	if r.Services["httpd"] != 80 {
		t.Fatalf("httpd should listen on 80: %v", r.Services)
	}
	if r.Files["/etc/app.env"] != "prod\n" {
		t.Fatalf("env expansion failed: %q", r.Files["/etc/app.env"])
	}
	if len(r.Packages) != 1 || r.Packages[0] != "httpd" {
		t.Fatalf("packages %v", r.Packages)
	}
}

func TestFailureStopsWithSetE(t *testing.T) {
	r := Run("#!/bin/bash\nset -e\nsystemctl start nginx\necho after > /tmp/x\n")
	if r.ExitCode == 0 {
		t.Fatal("starting an uninstalled service should fail")
	}
	if _, ok := r.Files["/tmp/x"]; ok {
		t.Fatal("set -e should stop execution")
	}
}

func TestUnsupportedCommandsAreSkippedNotExecuted(t *testing.T) {
	r := Run("#!/bin/bash\nrm -rf / --no-preserve-root\nnc -l 4444\npython3 -c 'import os'\n")
	if len(r.Skipped) != 2 {
		t.Fatalf("expected nc and python3 to be skipped: %v", r.Skipped)
	}
}

func TestHeredoc(t *testing.T) {
	r := Run("#!/bin/bash\ncat > /etc/motd <<'EOF'\nwelcome\nline2\nEOF\ncat /etc/motd\n")
	if r.Files["/etc/motd"] != "welcome\nline2\n" {
		t.Fatalf("heredoc: %q", r.Files["/etc/motd"])
	}
}

func TestLimits(t *testing.T) {
	big := "#!/bin/bash\n"
	for i := 0; i < 2000; i++ {
		big += "echo line\n"
	}
	r := Run(big)
	if len(r.Output) > 2*maxLines+5 {
		t.Fatalf("line limit not enforced: %d", len(r.Output))
	}
}
