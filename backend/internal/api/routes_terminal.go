package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/auth"
	"cloudsim/cli/pkg/commands"
)

// Web terminal: a built-in interpreter for the cloudsim command grammar. Commands are parsed by
// cli/pkg/commands and executed in-process through s.Internal as the session's principal, so the
// normal IAM authorization, validation and audit path applies. No shell or process is ever spawned.

const (
	termCmdTimeout = 30 * time.Second
	termMaxOutput  = 256 << 10
	termMaxLine    = 4096
	termMaxHistory = 200
)

func init() {
	RegisterModule("terminal", 95, func(s *Server) {
		s.Ext.Store("terminal-keys", &termKeyCache{m: map[string][2]string{}})
		s.op("GET /api/v1/ws/terminal", "", nil, s.handleTerminalWS)
		s.op("POST /api/v1/terminal/exec", "", nil, s.handleTerminalExec)
	})
}

// inProcTransport adapts s.Internal to the CLI transport interface.
type inProcTransport struct {
	s *Server
	p *auth.Principal
}

func (t *inProcTransport) Do(ctx context.Context, method, path string, body []byte, headers map[string]string) (int, []byte, map[string]string, error) {
	var b any
	if body != nil {
		b = body
	}
	r := t.s.Internal(ctx, t.p, method, "/api/v1"+path, b, headers)
	h := map[string]string{}
	for k := range r.Header {
		h[k] = r.Header.Get(k)
	}
	return r.Status, r.Body, h, nil
}

// termKeyCache remembers access keys the terminal created for `profile use` (secrets are only
// kept in memory; the key id is also persisted so a stale key can be rotated after a restart).
type termKeyCache struct {
	mu sync.Mutex
	m  map[string][2]string
}

func (s *Server) termKeys() *termKeyCache {
	v, _ := s.Ext.Load("terminal-keys")
	return v.(*termKeyCache)
}

// terminalSession is the state of one terminal (websocket connection or exec call).
type terminalSession struct {
	s       *Server
	base    *auth.Principal // the authenticated caller
	cur     *auth.Principal // effective principal (base or an IAM user via `profile use`)
	history []string
}

func (s *Server) newTerminalSession(p *auth.Principal) *terminalSession {
	return &terminalSession{s: s, base: p, cur: p}
}

func (t *terminalSession) prompt() string {
	return "cloudsim@" + t.base.AccountID + ":~$ "
}

func apiErrMessage(r *InternalResponse) string {
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(r.Body, &e) == nil && e.Error.Code != "" {
		return e.Error.Code + ": " + e.Error.Message
	}
	return fmt.Sprintf("HTTP %d", r.Status)
}

// profilePrincipal returns a principal acting as IAM user `user`, creating (or reusing) a
// terminal-scoped access key. Only the account root may do this.
func (s *Server) profilePrincipal(ctx context.Context, base *auth.Principal, user string) (*auth.Principal, error) {
	if base.Kind != auth.KindRoot {
		return nil, apierr.Denied("only the account root user can switch terminal profiles (you are %s)", base.Display())
	}
	if user == "" || len(user) > 64 || strings.ContainsAny(user, "/?#% ") {
		return nil, apierr.Validation("invalid IAM user name %q", user)
	}
	kc := s.termKeys()
	ck := base.AccountID + "|" + user
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if c, ok := kc.m[ck]; ok {
		if p, err := s.Auth.AccessKeyPrincipal(ctx, c[0], c[1], ""); err == nil && p.Kind == auth.KindIAMUser && p.Name == user {
			return p, nil
		}
		delete(kc.m, ck)
	}
	kvKey := "terminal-key:" + base.AccountID + ":" + user
	path := "/api/v1/iam/users/" + url.PathEscape(user) + "/access-keys"
	r := s.Internal(ctx, base, "POST", path, nil, nil)
	if r.Status == http.StatusConflict {
		// key limit reached: rotate the key a previous terminal session created, if any
		if old, err := s.Store.KV.Get(ctx, kvKey); err == nil && old != "" {
			s.Internal(ctx, base, "DELETE", path+"/"+url.PathEscape(old), nil, nil)
			r = s.Internal(ctx, base, "POST", path, nil, nil)
		}
	}
	if r.Status >= 300 {
		return nil, fmt.Errorf("cannot create a terminal access key for %s: %s", user, apiErrMessage(r))
	}
	var k struct {
		ID     string `json:"access_key_id"`
		Secret string `json:"secret_access_key"`
	}
	if err := json.Unmarshal(r.Body, &k); err != nil || k.ID == "" {
		return nil, fmt.Errorf("unexpected access key response")
	}
	_ = s.Store.KV.Set(ctx, kvKey, k.ID)
	kc.m[ck] = [2]string{k.ID, k.Secret}
	return s.Auth.AccessKeyPrincipal(ctx, k.ID, k.Secret, "")
}

const termHelp = `CloudSim web terminal - a built-in interpreter (no shell). Everything is simulated locally.

Built-ins:
  help                      this help
  clear                     clear the screen
  whoami                    show the identity commands run as
  echo <text>               print text
  history                   show command history
  profile use <iam-user>    run following commands as an IAM user (root only; uses a terminal access key)
  profile reset             go back to your own identity
  profile                   show the active profile

cloudsim commands (same grammar as the standalone CLI), e.g.:
  cloudsim ec2 describe-instances --output table
  cloudsim ec2 run-instances --image-id ami-0c1a2b3c4d5e6f701 --instance-type t3.micro --name web
  cloudsim s3 mb local://my-bucket
  cloudsim help
`

// exec runs one line and writes its output. It returns the exit code and whether the screen should be cleared.
func (t *terminalSession) exec(ctx context.Context, line string, out io.Writer) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	if len(t.history) == 0 || t.history[len(t.history)-1] != line {
		t.history = append(t.history, line)
		if len(t.history) > termMaxHistory {
			t.history = t.history[1:]
		}
	}
	args, err := commands.Split(line)
	if err != nil {
		fmt.Fprintf(out, "parse error: %v\n", err)
		return 2, false
	}
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case "help":
		fmt.Fprint(out, termHelp)
		return 0, false
	case "clear", "cls":
		return 0, true
	case "whoami":
		fmt.Fprintf(out, "%s (%s) account %s\n", t.cur.ARN, t.cur.Display(), t.cur.AccountID)
		return 0, false
	case "echo":
		fmt.Fprintln(out, strings.Join(args[1:], " "))
		return 0, false
	case "history":
		for i, h := range t.history {
			fmt.Fprintf(out, "%5d  %s\n", i+1, h)
		}
		return 0, false
	case "profile":
		switch {
		case len(args) == 1:
			fmt.Fprintf(out, "active profile: %s\n", t.cur.Display())
			return 0, false
		case len(args) == 2 && args[1] == "reset":
			t.cur = t.base
			fmt.Fprintf(out, "profile reset; commands run as %s\n", t.base.Display())
			return 0, false
		case len(args) == 3 && args[1] == "use":
			p, err := t.s.profilePrincipal(ctx, t.base, args[2])
			if err != nil {
				fmt.Fprintln(out, "profile:", apierr.As(err).Message)
				return 1, false
			}
			t.cur = p
			fmt.Fprintf(out, "now acting as %s (%s); IAM policies of this user apply. 'profile reset' to switch back.\n", p.Display(), p.ARN)
			return 0, false
		}
		fmt.Fprintln(out, "usage: profile use <iam-user> | profile reset")
		return 2, false
	case "cloudsim":
		cctx, cancel := context.WithTimeout(ctx, termCmdTimeout)
		defer cancel()
		env := &commands.Env{Transport: &inProcTransport{s: t.s, p: t.cur}, Out: out, Terminal: true, MaxOutput: termMaxOutput}
		code := commands.Run(cctx, env, args[1:])
		if cctx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(out, "command timed out after %s\n", termCmdTimeout)
		}
		return code, false
	case "aws":
		fmt.Fprintln(out, "aws: not available - this is the CloudSim simulator terminal. Use 'cloudsim' (same command grammar, simulated resources).")
		return 127, false
	}
	fmt.Fprintf(out, "%s: command not found. This terminal only runs 'cloudsim' commands and built-ins; type 'help'.\n", args[0])
	return 127, false
}

func (s *Server) handleTerminalExec(c *Ctx) error {
	var in struct {
		Command string `json:"command"`
		Profile string `json:"profile"`
	}
	if err := c.Decode(&in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Command) == "" || len(in.Command) > termMaxLine {
		return apierr.Validation("command is required (max %d chars)", termMaxLine)
	}
	t := s.newTerminalSession(c.P)
	if in.Profile != "" {
		p, err := s.profilePrincipal(c.Context(), c.P, in.Profile)
		if err != nil {
			return err
		}
		t.cur = p
	}
	var buf bytes.Buffer
	code, _ := t.exec(c.Context(), in.Command, &buf)
	return c.JSON(200, map[string]any{"output": buf.String(), "exit_code": code})
}

// ---- websocket line editor

type termConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (tc *termConn) send(data string) error {
	if data == "" {
		return nil
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	_ = tc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return tc.conn.WriteJSON(map[string]string{"type": "output", "data": data})
}

func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

type lineEditor struct {
	buf      []rune
	histIdx  int
	pending  string // incomplete escape sequence
	lastCR   bool
	t        *terminalSession
	tc       *termConn
	clearSeq string
}

func (e *lineEditor) redraw() string {
	return "\r\x1b[K" + e.t.prompt() + string(e.buf)
}

// feed processes raw keystrokes and returns false when the session should close.
func (e *lineEditor) feed(ctx context.Context, data string) bool {
	data = e.pending + data
	e.pending = ""
	var echo strings.Builder
	flush := func() { _ = e.tc.send(echo.String()); echo.Reset() }
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRuneInString(data[i:])
		if r == '\x1b' {
			rest := data[i:]
			if len(rest) < 3 {
				e.pending = rest
				break
			}
			if rest[1] == '[' || rest[1] == 'O' {
				switch rest[2] {
				case 'A', 'B':
					if len(e.t.history) > 0 {
						if rest[2] == 'A' && e.histIdx > 0 {
							e.histIdx--
						} else if rest[2] == 'B' && e.histIdx < len(e.t.history) {
							e.histIdx++
						}
						if e.histIdx < len(e.t.history) {
							e.buf = []rune(e.t.history[e.histIdx])
						} else {
							e.buf = nil
						}
						echo.WriteString(e.redraw())
					}
					i += 3
					continue
				}
				// skip other CSI sequences (arrows left/right, function keys)
				j := 2
				for j < len(rest) && !(rest[j] >= 0x40 && rest[j] <= 0x7e) {
					j++
				}
				i += min(j+1, len(rest))
				continue
			}
			i += 2
			continue
		}
		i += size
		wasCR := e.lastCR
		e.lastCR = r == '\r'
		switch {
		case r == '\n' && wasCR:
			// \r\n: already handled
		case r == '\r' || r == '\n':
			line := string(e.buf)
			e.buf = nil
			echo.WriteString("\r\n")
			flush()
			var out bytes.Buffer
			if strings.TrimSpace(line) == "exit" || strings.TrimSpace(line) == "logout" {
				_ = e.tc.send("logout\r\n")
				return false
			}
			_, clear := e.t.exec(ctx, line, &out)
			e.histIdx = len(e.t.history)
			if clear {
				echo.WriteString(e.clearSeq)
			}
			echo.WriteString(crlf(out.String()))
			echo.WriteString(e.t.prompt())
		case r == 0x7f || r == 0x08:
			if len(e.buf) > 0 {
				e.buf = e.buf[:len(e.buf)-1]
				echo.WriteString("\b \b")
			}
		case r == 0x03: // ctrl-c
			e.buf = nil
			e.histIdx = len(e.t.history)
			echo.WriteString("^C\r\n" + e.t.prompt())
		case r == 0x0c: // ctrl-l
			echo.WriteString(e.clearSeq + e.t.prompt() + string(e.buf))
		case r == 0x15: // ctrl-u
			e.buf = nil
			echo.WriteString(e.redraw())
		case r == 0x04: // ctrl-d on empty line
			if len(e.buf) == 0 {
				flush()
				_ = e.tc.send("logout\r\n")
				return false
			}
		case r == '\t':
		case unicode.IsPrint(r):
			if len(e.buf) < termMaxLine {
				e.buf = append(e.buf, r)
				echo.WriteRune(r)
			}
		}
	}
	flush()
	return true
}

func (s *Server) handleTerminalWS(c *Ctx) error {
	conn, ok := s.wsAuth(c.W, c.R)
	if !ok {
		return nil
	}
	defer conn.Close()
	tc := &termConn{conn: conn}
	t := s.newTerminalSession(c.P)
	ed := &lineEditor{t: t, tc: tc, clearSeq: "\x1b[2J\x1b[H"}
	ctx := c.R.Context()
	_ = tc.send(crlf("CloudSim terminal - simulated cloud, local only. Type 'help' for commands.\n") + t.prompt())
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(90 * time.Second)) })
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(25 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				tc.mu.Lock()
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				tc.mu.Unlock()
			}
		}
	}()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Minute))
		var msg struct {
			Type    string `json:"type"`
			Data    string `json:"data"`
			Command string `json:"command"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return nil
		}
		switch msg.Type {
		case "input":
			if len(msg.Data) > 64*1024 || !ed.feed(ctx, msg.Data) {
				return nil
			}
		case "exec":
			if len(msg.Command) > termMaxLine {
				_ = tc.send("command too long\r\n")
				continue
			}
			var out bytes.Buffer
			_ = tc.send(msg.Command + "\r\n")
			_, clear := t.exec(ctx, msg.Command, &out)
			pre := ""
			if clear {
				pre = ed.clearSeq
			}
			ed.histIdx = len(t.history)
			_ = tc.send(pre + crlf(out.String()) + t.prompt() + string(ed.buf))
		case "ping", "resize":
		default:
			_ = tc.send("unknown message type\r\n")
		}
	}
}
