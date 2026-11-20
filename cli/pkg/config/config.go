// Package config reads and writes ~/.cloudsim/config and ~/.cloudsim/credentials (INI profiles).
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultEndpoint is the local CloudSim backend.
const DefaultEndpoint = "http://localhost:8080"

// INI is section -> key -> value.
type INI map[string]map[string]string

// ParseINI parses a simple INI document (comments with # or ;).
func ParseINI(s string) INI {
	out := INI{}
	sec := ""
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec = strings.TrimSpace(line[1 : len(line)-1])
			if out[sec] == nil {
				out[sec] = map[string]string{}
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || sec == "" {
			continue
		}
		out[sec][strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// String renders the INI deterministically.
func (i INI) String() string {
	var b strings.Builder
	secs := make([]string, 0, len(i))
	for s := range i {
		secs = append(secs, s)
	}
	sort.Slice(secs, func(a, c int) bool {
		if secs[a] == "default" || secs[c] == "default" {
			return secs[a] == "default"
		}
		return secs[a] < secs[c]
	})
	for _, s := range secs {
		fmt.Fprintf(&b, "[%s]\n", s)
		ks := make([]string, 0, len(i[s]))
		for k := range i[s] {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			fmt.Fprintf(&b, "%s = %s\n", k, i[s][k])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Store is the on-disk configuration directory.
type Store struct{ Dir string }

// DefaultStore uses $CLOUDSIM_CONFIG_DIR or ~/.cloudsim.
func DefaultStore() Store {
	if d := os.Getenv("CLOUDSIM_CONFIG_DIR"); d != "" {
		return Store{d}
	}
	h, _ := os.UserHomeDir()
	return Store{filepath.Join(h, ".cloudsim")}
}

func (s Store) read(name string) INI {
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return INI{}
	}
	return ParseINI(string(b))
}

func (s Store) write(name string, i INI) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, name), []byte(i.String()), 0o600)
}

// configSection follows the AWS convention: [default] and [profile name].
func configSection(profile string) string {
	if profile == "default" {
		return "default"
	}
	return "profile " + profile
}

// Profile is the resolved connection settings.
type Profile struct {
	Name         string
	Endpoint     string
	Output       string
	AccessKeyID  string
	SecretKey    string
	SessionToken string
}

// Resolve merges flags > environment > files > defaults.
func (s Store) Resolve(profile, endpointFlag string, getenv func(string) string) Profile {
	if profile == "" {
		profile = getenv("CLOUDSIM_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	cfg := s.read("config")[configSection(profile)]
	cred := s.read("credentials")[profile]
	p := Profile{Name: profile, Endpoint: cfg["endpoint_url"], Output: cfg["output"],
		AccessKeyID: cred["access_key_id"], SecretKey: cred["secret_access_key"], SessionToken: cred["session_token"]}
	if v := getenv("CLOUDSIM_ENDPOINT"); v != "" {
		p.Endpoint = v
	}
	if endpointFlag != "" {
		p.Endpoint = endpointFlag
	}
	if p.Endpoint == "" {
		p.Endpoint = DefaultEndpoint
	}
	if v := getenv("CLOUDSIM_ACCESS_KEY_ID"); v != "" {
		p.AccessKeyID, p.SecretKey, p.SessionToken = v, getenv("CLOUDSIM_SECRET_ACCESS_KEY"), getenv("CLOUDSIM_SESSION_TOKEN")
	}
	if v := getenv("CLOUDSIM_OUTPUT"); v != "" {
		p.Output = v
	}
	return p
}

// Set stores one key for a profile. Credential keys go to the credentials file.
func (s Store) Set(profile, key, value string) error {
	switch key {
	case "access_key_id", "secret_access_key", "session_token":
		c := s.read("credentials")
		if c[profile] == nil {
			c[profile] = map[string]string{}
		}
		if value == "" {
			delete(c[profile], key)
		} else {
			c[profile][key] = value
		}
		return s.write("credentials", c)
	case "endpoint_url", "output":
		c := s.read("config")
		sec := configSection(profile)
		if c[sec] == nil {
			c[sec] = map[string]string{}
		}
		if value == "" {
			delete(c[sec], key)
		} else {
			c[sec][key] = value
		}
		return s.write("config", c)
	}
	return fmt.Errorf("unknown setting %q (valid: access_key_id, secret_access_key, session_token, endpoint_url, output)", key)
}

// Get returns one stored key for a profile.
func (s Store) Get(profile, key string) string {
	if v, ok := s.read("credentials")[profile][key]; ok {
		return v
	}
	return s.read("config")[configSection(profile)][key]
}
