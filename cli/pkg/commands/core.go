// Package commands implements the `cloudsim` command grammar (AWS-CLI-like) on top of the
// CloudSim REST API (docs/api.md). It is transport-agnostic so the same code runs in the
// standalone binary (HTTP) and inside the backend's web terminal (in-process).
//
// CloudSim is a local training simulator: every command talks only to a CloudSim endpoint and
// manipulates simulated resources. There is no code path to real AWS.
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Version of the CLI.
var Version = "0.1.0"

// Transport executes one API call. path is relative to /api/v1 (e.g. "/ec2/instances?state=running").
type Transport interface {
	Do(ctx context.Context, method, path string, body []byte, headers map[string]string) (status int, respBody []byte, respHeaders map[string]string, err error)
}

// LocalFS gives commands access to the user's local files (file://, s3 cp). It is nil in the
// web terminal, where server-side files must never be reachable.
type LocalFS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte) error
	IsDir(name string) bool
	// Walk calls fn for every regular file below root with its slash-separated relative path.
	Walk(root string, fn func(rel string) error) error
}

// Env is everything a command execution needs.
type Env struct {
	Transport Transport
	Out       io.Writer
	Err       io.Writer
	FS        LocalFS
	// DefaultOutput is used when --output is not given (json when empty).
	DefaultOutput string
	// Terminal is true inside the web terminal: connection flags are rejected and help mentions built-ins.
	Terminal bool
	// MaxOutput truncates output (bytes, 0 = unlimited).
	MaxOutput int
}

// Exit codes (mirroring the AWS CLI conventions).
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 252
	ExitAPIError = 254
)

// APIError is an error response from the CloudSim API.
type APIError struct {
	Status  int
	Code    string
	Message string
	Op      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("An error occurred (%s) when calling the %s operation: %s", e.Code, e.Op, e.Message)
}

// UsageError is a command-line usage problem.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(f string, a ...any) error { return &UsageError{fmt.Sprintf(f, a...)} }

// FlagKind is the value type of a flag.
type FlagKind int

const (
	String FlagKind = iota
	Int
	Float
	Bool
	List
)

// Flag describes a command option.
type Flag struct {
	Name     string
	Kind     FlagKind
	Required bool
	Help     string
}

// Command is one `cloudsim <service> <command>`.
type Command struct {
	Name     string
	Summary  string
	Flags    []Flag
	Args     string // positional usage, e.g. "<LocalPath|LocalUri> <LocalPath|LocalUri>"
	MinArgs  int
	MaxArgs  int
	Columns  []string // default table/text columns
	Run      func(x *Exec) (any, error)
	Examples []string
}

// Service groups commands.
type Service struct {
	Name     string
	Summary  string
	Commands map[string]*Command
}

var registry = map[string]*Service{}
var aliases = map[string]string{"vpc": "ec2"}

func register(svc, summary string, cmds ...*Command) {
	s := registry[svc]
	if s == nil {
		s = &Service{Name: svc, Summary: summary, Commands: map[string]*Command{}}
		registry[svc] = s
	}
	for _, c := range cmds {
		s.Commands[c.Name] = c
	}
}

// Services returns registered service names (sorted).
func Services() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Lookup finds a command (service aliases are resolved).
func Lookup(service, name string) (*Command, bool) {
	if a, ok := aliases[service]; ok {
		service = a
	}
	s := registry[service]
	if s == nil {
		return nil, false
	}
	c, ok := s.Commands[name]
	return c, ok
}

// Globals are options accepted anywhere on the command line.
type Globals struct {
	Output   string
	Query    string
	Profile  string
	Endpoint string
	Region   string
	Debug    bool
	Version  bool
}

// ExtractGlobals removes global options from args.
func ExtractGlobals(args []string) (Globals, []string, error) {
	var g Globals
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		var dst *string
		switch name {
		case "--output":
			dst = &g.Output
		case "--query":
			dst = &g.Query
		case "--profile":
			dst = &g.Profile
		case "--endpoint-url":
			dst = &g.Endpoint
		case "--region":
			dst = &g.Region
		case "--debug":
			g.Debug = true
			continue
		case "--version":
			if len(rest) == 0 {
				g.Version = true
				continue
			}
		}
		if dst == nil {
			rest = append(rest, a)
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return g, nil, usagef("argument %s: expected one argument", name)
			}
			i++
			val = args[i]
		}
		*dst = val
	}
	switch g.Output {
	case "", "json", "table", "text":
	default:
		return g, nil, usagef("argument --output: invalid choice: '%s' (choose from 'json', 'table', 'text')", g.Output)
	}
	return g, rest, nil
}

// Exec is the execution context of one command.
type Exec struct {
	Ctx     context.Context
	Env     *Env
	Service string
	Cmd     *Command
	Flags   map[string][]string
	Args    []string
	Globals Globals
}

// Run parses and executes a full command line (without the leading "cloudsim") and returns the exit code.
func Run(ctx context.Context, env *Env, args []string) int {
	out := env.Out
	if env.MaxOutput > 0 {
		out = &limitWriter{w: env.Out, n: env.MaxOutput}
	}
	errw := env.Err
	if errw == nil {
		errw = out
	}
	e := *env
	e.Out = out
	code := run(ctx, &e, args, errw)
	if env.Err == nil {
		errw = env.Out
	}
	if lw, ok := out.(*limitWriter); ok && lw.truncated {
		fmt.Fprintf(errw, "\n[output truncated at %d bytes]\n", env.MaxOutput)
	}
	return code
}

func run(ctx context.Context, env *Env, args []string, errw io.Writer) int {
	g, rest, err := ExtractGlobals(args)
	if err != nil {
		fmt.Fprintln(errw, err)
		return ExitUsage
	}
	if env.Terminal && (g.Profile != "" || g.Endpoint != "") {
		fmt.Fprintln(errw, "--profile and --endpoint-url are not available in the web terminal; use the built-in 'profile use <iam-user>' instead")
		return ExitUsage
	}
	if g.Version {
		fmt.Fprintf(env.Out, "cloudsim-cli/%s (CloudSim local training simulator - simulated resources only)\n", Version)
		return ExitOK
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		if len(rest) > 1 {
			return run(ctx, env, append(rest[1:], "help"), errw)
		}
		writeTopHelp(env.Out, env.Terminal)
		return ExitOK
	}
	svcName := rest[0]
	canonical := svcName
	if a, ok := aliases[svcName]; ok {
		canonical = a
	}
	svc := registry[canonical]
	if svc == nil {
		fmt.Fprintf(errw, "cloudsim: error: argument command: Invalid choice '%s'\n", svcName)
		if s := suggest(svcName, append(Services(), keys(aliases)...)); len(s) > 0 {
			fmt.Fprintf(errw, "Did you mean: %s?\n", strings.Join(s, ", "))
		}
		fmt.Fprintln(errw, "Run 'cloudsim help' to list services.")
		return ExitUsage
	}
	if len(rest) == 1 || rest[1] == "help" || rest[1] == "--help" || rest[1] == "-h" {
		writeServiceHelp(env.Out, svcName, svc)
		return ExitOK
	}
	cmd := svc.Commands[rest[1]]
	if cmd == nil {
		fmt.Fprintf(errw, "cloudsim: error: argument operation: Invalid choice '%s' for service '%s'\n", rest[1], svcName)
		if s := suggest(rest[1], keys(svc.Commands)); len(s) > 0 {
			fmt.Fprintf(errw, "Did you mean: %s?\n", strings.Join(s, ", "))
		}
		fmt.Fprintf(errw, "Run 'cloudsim %s help' to list its commands.\n", svcName)
		return ExitUsage
	}
	for _, a := range rest[2:] {
		if a == "help" || a == "--help" || a == "-h" {
			writeCommandHelp(env.Out, svcName, cmd)
			return ExitOK
		}
	}
	flags, pos, err := parseFlags(cmd, rest[2:])
	if err != nil {
		fmt.Fprintf(errw, "cloudsim %s %s: error: %v\n", svcName, cmd.Name, err)
		fmt.Fprintf(errw, "usage: %s\n", usageLine(svcName, cmd))
		return ExitUsage
	}
	if g.Output == "" {
		g.Output = env.DefaultOutput
	}
	if g.Output == "" {
		g.Output = "json"
	}
	x := &Exec{Ctx: ctx, Env: env, Service: canonical, Cmd: cmd, Flags: flags, Args: pos, Globals: g}
	res, err := cmd.Run(x)
	if err != nil {
		var ue *UsageError
		var ae *APIError
		switch {
		case errors.As(err, &ue):
			fmt.Fprintf(errw, "cloudsim %s %s: error: %v\n", svcName, cmd.Name, err)
			return ExitUsage
		case errors.As(err, &ae):
			fmt.Fprintln(errw, "\n"+ae.Error())
			return ExitAPIError
		default:
			fmt.Fprintln(errw, err)
			return ExitError
		}
	}
	if res == nil {
		return ExitOK
	}
	if err := Format(env.Out, res, g.Output, g.Query, cmd.Columns); err != nil {
		fmt.Fprintln(errw, err)
		return ExitUsage
	}
	return ExitOK
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type limitWriter struct {
	w         io.Writer
	n         int
	written   int
	truncated bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.written >= l.n {
		l.truncated = true
		return len(p), nil
	}
	room := l.n - l.written
	if len(p) > room {
		l.truncated = true
		_, err := l.w.Write(p[:room])
		l.written = l.n
		return len(p), err
	}
	l.written += len(p)
	_, err := l.w.Write(p)
	return len(p), err
}

// ---- flag parsing

func parseFlags(cmd *Command, args []string) (map[string][]string, []string, error) {
	spec := map[string]Flag{}
	for _, f := range cmd.Flags {
		spec[f.Name] = f
	}
	flags := map[string][]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") || a == "--" {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		f, ok := spec[name]
		if !ok && strings.HasPrefix(name, "no-") {
			if bf, ok2 := spec[strings.TrimPrefix(name, "no-")]; ok2 && bf.Kind == Bool && !hasVal {
				flags[bf.Name] = []string{"false"}
				continue
			}
		}
		if !ok {
			names := make([]string, 0, len(spec))
			for k := range spec {
				names = append(names, k)
			}
			msg := fmt.Sprintf("unknown option: --%s", name)
			if s := suggest(name, names); len(s) > 0 {
				msg += " (did you mean --" + strings.Join(s, ", --") + "?)"
			}
			return nil, nil, errors.New(msg)
		}
		switch f.Kind {
		case Bool:
			if !hasVal {
				val = "true"
			}
			if _, err := strconv.ParseBool(val); err != nil {
				return nil, nil, fmt.Errorf("--%s expects true or false", name)
			}
			flags[name] = []string{val}
		case List:
			if hasVal {
				flags[name] = append(flags[name], val)
				continue
			}
			n := 0
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				flags[name] = append(flags[name], args[i])
				n++
			}
			if n == 0 {
				return nil, nil, fmt.Errorf("argument --%s: expected at least one argument", name)
			}
		default:
			if !hasVal {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("argument --%s: expected one argument", name)
				}
				i++
				val = args[i]
			}
			switch f.Kind {
			case Int:
				if _, err := strconv.Atoi(val); err != nil {
					return nil, nil, fmt.Errorf("argument --%s: invalid integer value: '%s'", name, val)
				}
			case Float:
				if _, err := strconv.ParseFloat(val, 64); err != nil {
					return nil, nil, fmt.Errorf("argument --%s: invalid number: '%s'", name, val)
				}
			}
			flags[name] = []string{val}
		}
	}
	for _, f := range cmd.Flags {
		if f.Required && len(flags[f.Name]) == 0 {
			return nil, nil, fmt.Errorf("the following arguments are required: --%s", f.Name)
		}
	}
	if len(pos) < cmd.MinArgs {
		return nil, nil, fmt.Errorf("expected %s", cmd.Args)
	}
	if len(pos) > cmd.MaxArgs {
		return nil, nil, fmt.Errorf("unexpected argument: %s", pos[cmd.MaxArgs])
	}
	return flags, pos, nil
}

// Str returns a flag value ("" when absent).
func (x *Exec) Str(name string) string {
	if v := x.Flags[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Has reports whether a flag was given.
func (x *Exec) Has(name string) bool { return len(x.Flags[name]) > 0 }

// Int returns an integer flag (def when absent; values were validated during parsing).
func (x *Exec) Int(name string, def int) int {
	if v := x.Str(name); v != "" {
		n, _ := strconv.Atoi(v)
		return n
	}
	return def
}

// Float returns a float flag.
func (x *Exec) Float(name string, def float64) float64 {
	if v := x.Str(name); v != "" {
		n, _ := strconv.ParseFloat(v, 64)
		return n
	}
	return def
}

// Bool returns a tri-state bool flag.
func (x *Exec) Bool(name string) (bool, bool) {
	v := x.Str(name)
	if v == "" {
		return false, false
	}
	b, _ := strconv.ParseBool(v)
	return b, true
}

// List returns list values; comma separated single values are split too.
func (x *Exec) List(name string) []string {
	var out []string
	for _, v := range x.Flags[name] {
		if strings.HasPrefix(v, "[") {
			var arr []string
			if json.Unmarshal([]byte(v), &arr) == nil {
				out = append(out, arr...)
				continue
			}
		}
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// Value reads a flag that may be a file:// reference.
func (x *Exec) Value(name string) (string, error) {
	return x.resolveValue(x.Str(name))
}

func (x *Exec) resolveValue(v string) (string, error) {
	for _, p := range []string{"file://", "fileb://"} {
		if strings.HasPrefix(v, p) {
			b, err := x.readLocal(strings.TrimPrefix(v, p))
			return string(b), err
		}
	}
	return v, nil
}

func (x *Exec) readLocal(name string) ([]byte, error) {
	if x.Env.FS == nil {
		return nil, errors.New("local file access is not available in the web terminal (the terminal runs on the simulator server); use the console or the standalone cloudsim CLI")
	}
	b, err := x.Env.FS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("unable to read %s: %v", name, err)
	}
	return b, nil
}

// ---- API calls

func opName(cmd string) string {
	var b strings.Builder
	for _, p := range strings.Split(cmd, "-") {
		if p != "" {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

// Raw performs a call and returns the raw response; non-2xx responses become *APIError.
func (x *Exec) Raw(method, path string, body []byte, headers map[string]string) ([]byte, map[string]string, error) {
	if x.Env.Transport == nil {
		return nil, nil, errors.New("no transport configured")
	}
	if headers == nil {
		headers = map[string]string{}
	}
	if body != nil && headers["Content-Type"] == "" {
		headers["Content-Type"] = "application/json"
	}
	if x.Globals.Debug {
		fmt.Fprintf(x.Env.errw(), "DEBUG %s %s\n", method, path)
	}
	status, rb, rh, err := x.Env.Transport.Do(x.Ctx, method, path, body, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to the CloudSim endpoint: %v", err)
	}
	if status >= 400 {
		ae := &APIError{Status: status, Code: "HTTP" + strconv.Itoa(status), Message: strings.TrimSpace(string(rb)), Op: opName(x.Cmd.Name)}
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(rb, &e) == nil && e.Error.Code != "" {
			ae.Code, ae.Message = e.Error.Code, e.Error.Message
		}
		return nil, nil, ae
	}
	return rb, rh, nil
}

func (e *Env) errw() io.Writer {
	if e.Err != nil {
		return e.Err
	}
	return e.Out
}

// Call performs a JSON call and decodes the response (nil for empty bodies).
func (x *Exec) Call(method, path string, body any) (any, error) {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	rb, _, err := x.Raw(method, path, b, nil)
	if err != nil {
		return nil, err
	}
	return decode(rb)
}

func decode(b []byte) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %v", err)
	}
	return v, nil
}

// ListAll fetches every page of a list endpoint (raw arrays are accepted too).
func (x *Exec) ListAll(path string, q url.Values) ([]any, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("page_size", "200")
	var all []any
	for page := 1; page <= 50; page++ {
		q.Set("page", strconv.Itoa(page))
		v, err := x.Call("GET", path+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		switch t := v.(type) {
		case []any:
			return t, nil
		case map[string]any:
			items, _ := t["items"].([]any)
			all = append(all, items...)
			total := toInt(t["total"])
			if len(items) == 0 || len(all) >= total {
				return all, nil
			}
		default:
			return all, nil
		}
	}
	return all, nil
}

func toInt(v any) int {
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case float64:
		return int(t)
	case int:
		return t
	}
	return 0
}

func esc(s string) string { return url.PathEscape(s) }

// escKey escapes an object key segment by segment (keeps "/").
func escKey(k string) string {
	parts := strings.Split(k, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// ---- suggestions

func suggest(in string, cands []string) []string {
	type sc struct {
		s string
		d int
	}
	var out []sc
	for _, c := range cands {
		d := levenshtein(in, c)
		if d <= max(2, len(in)/3) || (len(in) >= 3 && strings.Contains(c, in)) {
			out = append(out, sc{c, d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].d < out[j].d || (out[i].d == out[j].d && out[i].s < out[j].s) })
	res := []string{}
	for i := 0; i < len(out) && i < 3; i++ {
		res = append(res, out[i].s)
	}
	return res
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// ---- help

func writeTopHelp(w io.Writer, terminal bool) {
	fmt.Fprintln(w, "cloudsim - command line for the CloudSim local cloud training simulator")
	fmt.Fprintln(w, "All resources are simulated locally; nothing is created in any real cloud account.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "usage: cloudsim [global options] <service> <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Services:")
	for _, n := range Services() {
		fmt.Fprintf(w, "  %-12s %s\n", n, registry[n].Summary)
	}
	fmt.Fprintf(w, "  %-12s %s\n", "vpc", "alias of ec2 for networking commands (e.g. cloudsim vpc describe-vpcs)")
	if !terminal {
		fmt.Fprintf(w, "  %-12s %s\n", "configure", "set credentials, endpoint and output format (~/.cloudsim)")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global options:")
	fmt.Fprintln(w, "  --output json|table|text   output format")
	fmt.Fprintln(w, "  --query <path>             select part of the result, e.g. items[0].instance_id or items[].name")
	if !terminal {
		fmt.Fprintln(w, "  --profile <name>           named profile from ~/.cloudsim/config")
		fmt.Fprintln(w, "  --endpoint-url <url>       CloudSim backend URL (default http://localhost:8080)")
	}
	fmt.Fprintln(w, "  --version                  print version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'cloudsim <service> help' for the commands of a service.")
}

func writeServiceHelp(w io.Writer, name string, s *Service) {
	fmt.Fprintf(w, "cloudsim %s - %s\n\nCommands:\n", name, s.Summary)
	for _, k := range keys(s.Commands) {
		fmt.Fprintf(w, "  %-34s %s\n", k, s.Commands[k].Summary)
	}
	fmt.Fprintf(w, "\nRun 'cloudsim %s <command> help' for details.\n", name)
}

func usageLine(svc string, c *Command) string {
	var b strings.Builder
	b.WriteString("cloudsim " + svc + " " + c.Name)
	for _, f := range c.Flags {
		v := " <value>"
		switch f.Kind {
		case Bool:
			v = ""
		case List:
			v = " <value> [<value>...]"
		}
		if f.Required {
			b.WriteString(" --" + f.Name + v)
		} else {
			b.WriteString(" [--" + f.Name + v + "]")
		}
	}
	if c.Args != "" {
		b.WriteString(" " + c.Args)
	}
	return b.String()
}

func writeCommandHelp(w io.Writer, svc string, c *Command) {
	fmt.Fprintf(w, "%s\n\n%s\n\nusage: %s\n", c.Name, c.Summary, usageLine(svc, c))
	if len(c.Flags) > 0 {
		fmt.Fprintln(w, "\nOptions:")
		for _, f := range c.Flags {
			req := ""
			if f.Required {
				req = " (required)"
			}
			fmt.Fprintf(w, "  --%-28s %s%s\n", f.Name, f.Help, req)
		}
	}
	if len(c.Examples) > 0 {
		fmt.Fprintln(w, "\nExamples:")
		for _, e := range c.Examples {
			fmt.Fprintln(w, "  "+e)
		}
	}
}
