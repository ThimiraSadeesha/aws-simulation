// Package userdata is a sandboxed interpreter for instance user-data scripts.
//
// Security model: scripts are NEVER handed to a shell or executed on the host. Instead a small,
// whitelisted subset of common bootstrap commands is interpreted against an in-memory model of
// the instance (packages, services, files, environment). Anything else is reported as
// "not supported in the simulator" and skipped. Execution is bounded in lines and output size.
package userdata

import (
	"fmt"
	"sort"
	"strings"
)

const (
	maxLines  = 500
	maxOutput = 64 * 1024
	maxFiles  = 100
	maxFile   = 16 * 1024
)

// Result is the observable state of the instance after boot scripts ran.
type Result struct {
	Ran      bool              `json:"ran"`
	ExitCode int               `json:"exit_code"`
	Output   []string          `json:"output"`
	Packages []string          `json:"packages"`
	Services map[string]int    `json:"services"` // running service -> listening port (0 = none)
	Enabled  []string          `json:"enabled_services"`
	Files    map[string]string `json:"files"`
	Env      map[string]string `json:"env"`
	Skipped  []string          `json:"skipped_commands"`
}

// Ports opened by well-known services.
var servicePorts = map[string]int{
	"httpd": 80, "apache2": 80, "nginx": 80, "sshd": 22, "mysqld": 3306, "mariadb": 3306,
	"postgresql": 5432, "redis": 6379, "redis-server": 6379, "docker": 0, "crond": 0, "node-app": 3000, "tomcat": 8080,
}

// Packages that provide a service of the same (or mapped) name.
var packageService = map[string]string{
	"httpd": "httpd", "apache2": "apache2", "nginx": "nginx", "mariadb-server": "mariadb", "mysql-server": "mysqld",
	"postgresql-server": "postgresql", "postgresql": "postgresql", "redis": "redis", "redis-server": "redis-server",
	"docker": "docker", "openssh-server": "sshd", "cronie": "crond", "tomcat": "tomcat", "nodejs": "", "git": "", "python3": "", "curl": "", "wget": "", "jq": "", "unzip": "", "htop": "", "stress": "",
}

// Run interprets script. It always returns a Result (errors are reflected in ExitCode/Output).
func Run(script string) Result {
	r := Result{Services: map[string]int{"sshd": 22}, Files: map[string]string{}, Env: map[string]string{}, Packages: []string{}, Output: []string{}, Skipped: []string{}}
	script = strings.ReplaceAll(script, "\r\n", "\n")
	if strings.TrimSpace(script) == "" {
		return r
	}
	r.Ran = true
	installed := map[string]bool{}
	enabled := map[string]bool{}
	size := 0
	out := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		if size+len(line) > maxOutput {
			return
		}
		size += len(line)
		r.Output = append(r.Output, line)
	}
	lines := strings.Split(script, "\n")
	if len(lines) > maxLines {
		out("user-data: script truncated to %d lines", maxLines)
		lines = lines[:maxLines]
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "#!") && !strings.HasPrefix(strings.TrimSpace(lines[0]), "#cloud-config") {
		out("user-data: no shebang found; treating as /bin/bash script")
	}
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimSpace(lines[i])
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		// heredoc: cat > FILE <<EOF ... EOF  (also tee)
		if strings.Contains(raw, "<<") && (strings.HasPrefix(raw, "cat ") || strings.HasPrefix(raw, "tee ")) {
			marker := strings.Trim(strings.TrimSpace(raw[strings.Index(raw, "<<")+2:]), `'"-`)
			target := ""
			if j := strings.Index(raw, ">"); j >= 0 {
				target = strings.Fields(raw[j+1:])[0]
			} else if f := strings.Fields(raw); len(f) > 1 && strings.HasPrefix(raw, "tee ") {
				target = f[1]
			}
			var body []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != marker; i++ {
				body = append(body, lines[i])
			}
			if target != "" {
				writeFile(&r, target, strings.Join(body, "\n")+"\n", false)
				out("+ wrote %s (%d bytes)", target, len(strings.Join(body, "\n"))+1)
			}
			continue
		}
		for _, cmd := range splitChain(raw) {
			code := exec(&r, cmd, installed, enabled, out)
			if code != 0 {
				r.ExitCode = code
				out("user-data: command failed with exit code %d: %s", code, cmd)
				if strings.Contains(script, "set -e") {
					finish(&r, installed, enabled)
					return r
				}
			}
		}
	}
	finish(&r, installed, enabled)
	return r
}

func finish(r *Result, installed, enabled map[string]bool) {
	for p := range installed {
		r.Packages = append(r.Packages, p)
	}
	sort.Strings(r.Packages)
	for s := range enabled {
		r.Enabled = append(r.Enabled, s)
	}
	sort.Strings(r.Enabled)
	if r.Enabled == nil {
		r.Enabled = []string{}
	}
}

// splitChain splits "a && b ; c" into commands (no pipes/subshell semantics are modeled).
func splitChain(s string) []string {
	var out []string
	for _, part := range strings.Split(s, "&&") {
		for _, p := range strings.Split(part, ";") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func expand(r *Result, s string) string {
	for k, v := range r.Env {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
		s = strings.ReplaceAll(s, "$"+k, v)
	}
	return s
}

func unquote(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func writeFile(r *Result, path, content string, appendMode bool) {
	if len(r.Files) >= maxFiles {
		return
	}
	if appendMode {
		content = r.Files[path] + content
	}
	if len(content) > maxFile {
		content = content[:maxFile]
	}
	r.Files[path] = content
}

func exec(r *Result, cmd string, installed, enabled map[string]bool, out func(string, ...any)) int {
	cmd = expand(r, cmd)
	out("+ %s", cmd)
	cmd = strings.TrimPrefix(cmd, "sudo ")
	// output redirection for echo/printf
	redirect, appendMode := "", false
	if j := strings.Index(cmd, ">>"); j >= 0 {
		redirect, appendMode = strings.TrimSpace(cmd[j+2:]), true
		cmd = strings.TrimSpace(cmd[:j])
	} else if j := strings.Index(cmd, ">"); j >= 0 && !strings.Contains(cmd[:j], "<") {
		redirect = strings.TrimSpace(cmd[j+1:])
		cmd = strings.TrimSpace(cmd[:j])
	}
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return 0
	}
	switch f[0] {
	case "set", "true", ":", "sleep", "cd", "chmod", "chown", "ulimit", "sync", "hostnamectl":
		return 0
	case "false":
		return 1
	case "export":
		for _, kv := range f[1:] {
			if k, v, ok := strings.Cut(kv, "="); ok {
				r.Env[k] = unquote(v)
			}
		}
		return 0
	case "echo", "printf":
		text := unquote(strings.TrimSpace(strings.TrimPrefix(cmd, f[0])))
		text = strings.TrimPrefix(text, "-e ")
		text = unquote(text)
		if redirect != "" {
			writeFile(r, redirect, text+"\n", appendMode)
		} else {
			out("%s", text)
		}
		return 0
	case "mkdir", "touch":
		for _, p := range f[1:] {
			if !strings.HasPrefix(p, "-") && f[0] == "touch" {
				writeFile(r, p, "", true)
			}
		}
		return 0
	case "cat":
		if len(f) > 1 {
			if c, ok := r.Files[f[1]]; ok {
				out("%s", strings.TrimRight(c, "\n"))
				return 0
			}
			out("cat: %s: No such file or directory", f[1])
			return 1
		}
		return 0
	case "rm":
		for _, p := range f[1:] {
			delete(r.Files, p)
		}
		return 0
	case "yum", "dnf", "apt", "apt-get":
		if len(f) < 2 {
			return 0
		}
		sub := f[1]
		if sub == "update" || sub == "upgrade" || sub == "makecache" || sub == "clean" {
			out("%s %s: metadata refreshed (simulated, no network access)", f[0], sub)
			return 0
		}
		if sub != "install" {
			out("%s %s: not supported in the simulator", f[0], sub)
			r.Skipped = append(r.Skipped, cmd)
			return 0
		}
		for _, p := range f[2:] {
			if strings.HasPrefix(p, "-") {
				continue
			}
			if _, known := packageService[p]; !known {
				out("No package %s available.", p)
				return 1
			}
			installed[p] = true
			out("Installed: %s (simulated)", p)
		}
		return 0
	case "systemctl", "service":
		var action, unit string
		if f[0] == "systemctl" && len(f) >= 3 {
			action, unit = f[1], f[len(f)-1]
			if f[2] == "--now" && action == "enable" && len(f) >= 4 {
				unit = f[3]
				enabled[strings.TrimSuffix(unit, ".service")] = true
				action = "start"
			}
		} else if f[0] == "service" && len(f) >= 3 {
			unit, action = f[1], f[2]
		} else {
			return 0
		}
		unit = strings.TrimSuffix(unit, ".service")
		switch action {
		case "enable":
			enabled[unit] = true
			return 0
		case "disable":
			delete(enabled, unit)
			return 0
		case "start", "restart", "reload":
			if !serviceAvailable(unit, installed) {
				out("Failed to start %s.service: Unit %s.service not found.", unit, unit)
				return 5
			}
			r.Services[unit] = servicePorts[unit]
			out("Started %s.service (listening on port %d)", unit, servicePorts[unit])
			return 0
		case "stop":
			delete(r.Services, unit)
			return 0
		case "status", "is-active":
			if _, ok := r.Services[unit]; ok {
				out("%s.service: active (running)", unit)
				return 0
			}
			out("%s.service: inactive (dead)", unit)
			return 3
		}
		return 0
	case "useradd", "groupadd", "usermod", "timedatectl", "curl", "wget", "pip", "pip3", "npm", "git":
		out("%s: simulated (no external effects, no network access)", f[0])
		return 0
	}
	out("user-data: '%s' is not supported in the simulator sandbox; skipped", f[0])
	r.Skipped = append(r.Skipped, cmd)
	return 0
}

func serviceAvailable(unit string, installed map[string]bool) bool {
	if unit == "sshd" || unit == "crond" {
		return true
	}
	for pkg, svc := range packageService {
		if installed[pkg] && svc == unit {
			return true
		}
	}
	return false
}
