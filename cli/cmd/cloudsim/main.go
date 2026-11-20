// Command cloudsim is the command line client for the CloudSim local cloud training simulator.
// It only talks to a CloudSim backend (default http://localhost:8080); all resources are simulated.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"cloudsim/cli/pkg/commands"
	"cloudsim/cli/pkg/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, config.DefaultStore()))
}

func run(ctx context.Context, args []string, in io.Reader, out, errw io.Writer, store config.Store) int {
	g, rest, err := commands.ExtractGlobals(args)
	if err != nil {
		fmt.Fprintln(errw, err)
		return commands.ExitUsage
	}
	if len(rest) > 0 && rest[0] == "configure" {
		return configure(rest[1:], g, in, out, errw, store)
	}
	prof := store.Resolve(g.Profile, g.Endpoint, os.Getenv)
	tr, err := commands.NewHTTPTransport(prof.Endpoint, prof.AccessKeyID, prof.SecretKey, prof.SessionToken)
	if err != nil {
		fmt.Fprintln(errw, err)
		return commands.ExitUsage
	}
	needsCreds := len(rest) > 0 && rest[0] != "help" && !g.Version && len(rest) > 1 && rest[len(rest)-1] != "help" && rest[len(rest)-1] != "--help"
	if needsCreds && prof.AccessKeyID == "" {
		fmt.Fprintf(errw, "Unable to locate credentials for profile %q. Run 'cloudsim configure' (create an access key for an IAM user in the CloudSim console first).\n", prof.Name)
		return commands.ExitError
	}
	env := &commands.Env{Transport: tr, Out: out, Err: errw, FS: commands.OSFS{}, DefaultOutput: prof.Output}
	return commands.Run(ctx, env, args)
}

func configure(args []string, g commands.Globals, in io.Reader, out, errw io.Writer, store config.Store) int {
	profile := g.Profile
	if profile == "" {
		profile = os.Getenv("CLOUDSIM_PROFILE")
	}
	if profile == "" {
		profile = "default"
	}
	if g.Endpoint != "" {
		if err := store.Set(profile, "endpoint_url", g.Endpoint); err != nil {
			fmt.Fprintln(errw, err)
			return commands.ExitError
		}
	}
	if g.Output != "" {
		_ = store.Set(profile, "output", g.Output)
	}
	if len(args) > 0 {
		switch args[0] {
		case "list":
			p := store.Resolve(profile, "", os.Getenv)
			fmt.Fprintf(out, "%-14s %s\n%-14s %s\n%-14s %s\n%-14s %s\n", "profile", p.Name, "endpoint_url", p.Endpoint, "access_key_id", mask(p.AccessKeyID), "output", defaultStr(p.Output, "json"))
			return 0
		case "get":
			if len(args) != 2 {
				fmt.Fprintln(errw, "usage: cloudsim configure get <key>")
				return commands.ExitUsage
			}
			fmt.Fprintln(out, store.Get(profile, args[1]))
			return 0
		case "set":
			if len(args) != 3 {
				fmt.Fprintln(errw, "usage: cloudsim configure set <key> <value>")
				return commands.ExitUsage
			}
			if err := store.Set(profile, args[1], args[2]); err != nil {
				fmt.Fprintln(errw, err)
				return commands.ExitUsage
			}
			return 0
		}
		// flag form: --access-key-id X --secret-access-key Y --session-token Z
		for i := 0; i < len(args); i++ {
			k := strings.ReplaceAll(strings.TrimPrefix(args[i], "--"), "-", "_")
			if !strings.HasPrefix(args[i], "--") || i+1 >= len(args) {
				fmt.Fprintf(errw, "unexpected argument %q; usage: cloudsim configure [list|get|set] [--access-key-id X --secret-access-key Y]\n", args[i])
				return commands.ExitUsage
			}
			if err := store.Set(profile, k, args[i+1]); err != nil {
				fmt.Fprintln(errw, err)
				return commands.ExitUsage
			}
			i++
		}
		return 0
	}
	// interactive
	r := bufio.NewReader(in)
	cur := store.Resolve(profile, "", func(string) string { return "" })
	for _, q := range []struct{ label, key, cur string }{
		{"CloudSim Access Key ID", "access_key_id", mask(cur.AccessKeyID)},
		{"CloudSim Secret Access Key", "secret_access_key", mask(cur.SecretKey)},
		{"CloudSim endpoint URL", "endpoint_url", cur.Endpoint},
		{"Default output format", "output", defaultStr(cur.Output, "json")},
	} {
		fmt.Fprintf(out, "%s [%s]: ", q.label, defaultStr(q.cur, "None"))
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if err := store.Set(profile, q.key, line); err != nil {
				fmt.Fprintln(errw, err)
				return commands.ExitError
			}
		}
		if err != nil {
			fmt.Fprintln(out)
			break
		}
	}
	return 0
}

func mask(s string) string {
	if len(s) <= 4 {
		return s
	}
	return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
