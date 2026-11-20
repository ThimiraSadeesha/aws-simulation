// Package labs loads training-lab definitions (YAML/JSON) and validates lab steps declaratively
// against the real simulator state of an account (through an in-process API caller).
package labs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lab is one training lab.
type Lab struct {
	ID             string   `yaml:"id" json:"id"`
	Title          string   `yaml:"title" json:"title"`
	Level          string   `yaml:"level" json:"level"`
	Service        string   `yaml:"service" json:"service"`
	DurationMin    int      `yaml:"duration_min" json:"duration_min"`
	Description    string   `yaml:"description" json:"description"`
	ResourcePrefix string   `yaml:"resource_prefix" json:"resource_prefix"`
	Objectives     []string `yaml:"objectives" json:"objectives"`
	Notes          string   `yaml:"notes" json:"notes,omitempty"`
	Refs           []Ref    `yaml:"refs" json:"-"`
	Steps          []Step   `yaml:"steps" json:"steps"`
	File           string   `yaml:"-" json:"-"`
}

// Step is one validated step of a lab.
type Step struct {
	Index          int      `yaml:"-" json:"index"`
	Title          string   `yaml:"title" json:"title"`
	InstructionsMD string   `yaml:"instructions_md" json:"instructions_md"`
	Hint           string   `yaml:"hint" json:"hint"`
	CLI            []string `yaml:"cli" json:"cli"`
	Console        string   `yaml:"console" json:"console"`
	Validation     Check    `yaml:"validation" json:"-"`
}

// Selector picks items from an API list endpoint.
type Selector struct {
	Resource string            `yaml:"resource"`
	Query    map[string]string `yaml:"query"`
	Match    []Matcher         `yaml:"match"`
	Field    string            `yaml:"field"`
}

// Ref is a named selector whose `field` value can be used as ${name} in later strings.
type Ref struct {
	Name     string `yaml:"name"`
	Selector `yaml:",inline"`
	Describe string `yaml:"describe"`
}

// Matcher tests a field of an item.
type Matcher struct {
	Field string    `yaml:"field"`
	Op    string    `yaml:"op"`
	Value any       `yaml:"value"`
	Match []Matcher `yaml:"match"` // for any_elem
}

// Check is a declarative validation.
type Check struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Refs        []Ref  `yaml:"refs"`
	Selector    `yaml:",inline"`
	Count       int `yaml:"count"`
	// reachability
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	Protocol    string `yaml:"protocol"`
	Port        int    `yaml:"port"`
	Reachable   *bool  `yaml:"reachable"`
	// iam_simulate
	Principal   string `yaml:"principal"`
	Action      string `yaml:"action"`
	ResourceARN string `yaml:"resource_arn"`
	Decision    string `yaml:"decision"`
	// s3_object_exists
	Bucket    string `yaml:"bucket"`
	Key       string `yaml:"key"`
	KeyPrefix string `yaml:"key_prefix"`
	// metric_above
	Namespace     string            `yaml:"namespace"`
	MetricName    string            `yaml:"metric_name"`
	Dimensions    map[string]string `yaml:"dimensions"`
	Stat          string            `yaml:"stat"`
	Threshold     float64           `yaml:"threshold"`
	WindowMinutes int               `yaml:"window_minutes"`
	// alarm_state
	Alarm string `yaml:"alarm"`
	State string `yaml:"state"`
	// target_health
	TargetGroup string `yaml:"target_group"`
	MinHealthy  int    `yaml:"min_healthy"`
	// all / any
	Checks []Check `yaml:"checks"`
}

// CheckTypes lists the supported validation types.
var CheckTypes = []string{"resource_exists", "count_at_least", "absent", "reachability", "iam_simulate", "s3_object_exists",
	"metric_above", "alarm_state", "target_health", "all", "any"}

// MatchOps lists the supported matcher operators.
var MatchOps = []string{"equals", "not_equals", "prefix", "suffix", "contains", "exists", "not_exists", "gt", "gte", "lt", "lte", "in", "regex", "len_gte", "any_elem"}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	varRe    = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
	levels   = map[string]bool{"beginner": true, "intermediate": true, "advanced": true}
	builtins = map[string]bool{"account": true, "lab_id": true, "prefix": true}
)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// LoadDir loads every *.yaml|*.yml|*.json below dir. Any invalid file is an error (fail loudly).
func LoadDir(dir string) ([]*Lab, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml", ".json":
			if !d.IsDir() {
				files = append(files, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("labs dir %s: %w", dir, err)
	}
	sort.Strings(files)
	var out []*Lab
	seen := map[string]string{}
	var errs []error
	for _, f := range files {
		l, err := LoadFile(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if prev, dup := seen[l.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate lab id %q (also in %s)", f, l.ID, prev))
			continue
		}
		seen[l.ID] = f
		out = append(out, l)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	order := map[string]int{"beginner": 0, "intermediate": 1, "advanced": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if order[out[i].Level] != order[out[j].Level] {
			return order[out[i].Level] < order[out[j].Level]
		}
		return out[i].File < out[j].File
	})
	return out, nil
}

// LoadFile parses and validates one lab file (unknown keys are rejected).
func LoadFile(path string) (*Lab, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	l, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	l.File = path
	if lvl := filepath.Base(filepath.Dir(path)); levels[lvl] && lvl != l.Level {
		return nil, fmt.Errorf("%s: level %q does not match directory %q", path, l.Level, lvl)
	}
	return l, nil
}

// Parse decodes and validates a lab document.
func Parse(b []byte) (*Lab, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var l Lab
	if err := dec.Decode(&l); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty lab file")
		}
		return nil, err
	}
	for i := range l.Steps {
		l.Steps[i].Index = i
		if l.Steps[i].CLI == nil {
			l.Steps[i].CLI = []string{}
		}
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return &l, nil
}

// Validate checks the schema of a lab.
func (l *Lab) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if !idRe.MatchString(l.ID) {
		add("id %q must match %s", l.ID, idRe)
	}
	if strings.TrimSpace(l.Title) == "" {
		add("title is required")
	}
	if !levels[l.Level] {
		add("level must be beginner, intermediate or advanced")
	}
	if l.Service == "" {
		add("service is required")
	}
	if l.DurationMin <= 0 {
		add("duration_min must be > 0")
	}
	if l.Description == "" {
		add("description is required")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,20}-$`).MatchString(l.ResourcePrefix) {
		add("resource_prefix %q must be lowercase and end with '-' (e.g. lab1-)", l.ResourcePrefix)
	}
	if len(l.Steps) < 1 || len(l.Steps) > 10 {
		add("a lab needs 1-10 steps")
	}
	known := map[string]bool{}
	for k := range builtins {
		known[k] = true
	}
	for i, r := range l.Refs {
		validateRef(r, fmt.Sprintf("refs[%d]", i), known, add)
	}
	for i, s := range l.Steps {
		where := fmt.Sprintf("steps[%d]", i)
		if s.Title == "" || strings.TrimSpace(s.InstructionsMD) == "" {
			add("%s: title and instructions_md are required", where)
		}
		if s.Hint == "" {
			add("%s: hint is required", where)
		}
		k2 := map[string]bool{}
		for k := range known {
			k2[k] = true
		}
		validateCheck(s.Validation, where+".validation", k2, add)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func validateRef(r Ref, where string, known map[string]bool, add func(string, ...any)) {
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(r.Name) {
		add("%s: invalid ref name %q", where, r.Name)
	}
	if known[r.Name] {
		add("%s: duplicate ref name %q", where, r.Name)
	}
	if r.Field == "" {
		add("%s: ref %q needs field", where, r.Name)
	}
	validateSelector(r.Selector, where, known, add)
	known[r.Name] = true
}

func validateSelector(s Selector, where string, known map[string]bool, add func(string, ...any)) {
	if !strings.HasPrefix(s.Resource, "/") || strings.Contains(s.Resource, "?") {
		add("%s: resource must be an API path like /ec2/instances (got %q)", where, s.Resource)
	}
	checkVars(s.Resource, where, known, add)
	for _, v := range s.Query {
		checkVars(v, where, known, add)
	}
	validateMatchers(s.Match, where, known, add)
}

func validateMatchers(ms []Matcher, where string, known map[string]bool, add func(string, ...any)) {
	for i, m := range ms {
		w := fmt.Sprintf("%s.match[%d]", where, i)
		op := m.Op
		if op == "" {
			op = "equals"
		}
		if !contains(MatchOps, op) {
			add("%s: unknown op %q (valid: %s)", w, m.Op, strings.Join(MatchOps, ", "))
		}
		if m.Field == "" {
			add("%s: field is required", w)
		}
		switch op {
		case "exists", "not_exists":
		case "any_elem":
			if len(m.Match) == 0 {
				add("%s: any_elem needs nested match", w)
			}
			validateMatchers(m.Match, w, known, add)
		default:
			if m.Value == nil {
				add("%s: value is required for op %s", w, op)
			}
			if op == "regex" {
				if _, err := regexp.Compile(fmt.Sprint(m.Value)); err != nil {
					add("%s: bad regex: %v", w, err)
				}
			}
		}
		if s, ok := m.Value.(string); ok {
			checkVars(s, w, known, add)
		}
	}
}

func checkVars(s, where string, known map[string]bool, add func(string, ...any)) {
	for _, m := range varRe.FindAllStringSubmatch(s, -1) {
		if !known[m[1]] {
			add("%s: unknown variable ${%s} (define it in refs)", where, m[1])
		}
	}
}

func validateCheck(c Check, where string, known map[string]bool, add func(string, ...any)) {
	if !contains(CheckTypes, c.Type) {
		add("%s: unknown type %q (valid: %s)", where, c.Type, strings.Join(CheckTypes, ", "))
		return
	}
	for i, r := range c.Refs {
		validateRef(r, fmt.Sprintf("%s.refs[%d]", where, i), known, add)
	}
	vars := func(ss ...string) {
		for _, s := range ss {
			checkVars(s, where, known, add)
		}
	}
	need := func(ok bool, what string) {
		if !ok {
			add("%s: %s requires %s", where, c.Type, what)
		}
	}
	switch c.Type {
	case "resource_exists", "count_at_least", "absent":
		validateSelector(c.Selector, where, known, add)
		if c.Type == "count_at_least" {
			need(c.Count > 0, "count > 0")
		}
	case "reachability":
		need(c.Source != "" && c.Destination != "", "source and destination")
		need(c.Port >= 0 && c.Port <= 65535, "a valid port")
		vars(c.Source, c.Destination)
	case "iam_simulate":
		need(c.Principal != "" && c.Action != "", "principal and action")
		need(contains([]string{"allowed", "denied", "explicitDeny", "implicitDeny"}, c.Decision), "decision allowed|denied|explicitDeny|implicitDeny")
		vars(c.Principal, c.ResourceARN)
	case "s3_object_exists":
		need(c.Bucket != "", "bucket")
		vars(c.Bucket, c.Key, c.KeyPrefix)
	case "metric_above":
		need(c.Namespace != "" && c.MetricName != "", "namespace and metric_name")
		for _, v := range c.Dimensions {
			vars(v)
		}
	case "alarm_state":
		need(c.Alarm != "", "alarm")
		need(contains([]string{"OK", "ALARM", "INSUFFICIENT_DATA"}, c.State), "state OK|ALARM|INSUFFICIENT_DATA")
		vars(c.Alarm)
	case "target_health":
		need(c.TargetGroup != "", "target_group")
		vars(c.TargetGroup)
	case "all", "any":
		need(len(c.Checks) > 0, "checks")
		for i, sub := range c.Checks {
			validateCheck(sub, fmt.Sprintf("%s.checks[%d]", where, i), known, add)
		}
	}
}
