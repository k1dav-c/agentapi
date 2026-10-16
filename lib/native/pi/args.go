package pi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
)

// reservedFlags pick the output mode or the session, which the adapter
// does itself, or make Pi do something other than serve RPC and exit.
var reservedFlags = map[string]string{
	"--mode":        "the adapter runs Pi with --mode rpc",
	"--print":       "the adapter runs Pi with --mode rpc",
	"-p":            "the adapter runs Pi with --mode rpc",
	"--session":     "the adapter picks the session with --session-id",
	"--session-id":  "the adapter picks the session with --session-id",
	"--continue":    "the adapter picks the session with --session-id",
	"-c":            "the adapter picks the session with --session-id",
	"--resume":      "the adapter picks the session with --session-id",
	"-r":            "the adapter picks the session with --session-id",
	"--fork":        "the adapter picks the session with --session-id",
	"--no-session":  "the adapter needs a saved session to resume it",
	"--export":      "it exports a session and exits",
	"--list-models": "it lists models and exits",
	"--help":        "it prints help and exits",
	"-h":            "it prints help and exits",
	"--version":     "it prints the version and exits",
	"-v":            "it prints the version and exits",
}

// tuiFlags only affect the terminal UI, which RPC mode doesn't draw. They
// are dropped with their value.
var tuiFlags = []string{"--tui-mode", "--use-theme"}

// valueFlags are Pi's built-in options that take a value. Pi reads the
// "--flag=value" form only for extension flags, so the adapter splits it
// for these.
var valueFlags = []string{
	"--provider", "--model", "--api-key", "--system-prompt", "--append-system-prompt",
	"--mode", "--session", "--session-id", "--fork", "--session-dir", "--name", "-n",
	"--models", "--tools", "-t", "--exclude-tools", "-xt", "--thinking", "--extension", "-e",
	"--skill", "--prompt-template", "--theme", "--use-theme", "--export", "--tui-mode",
}

// subcommands are Pi's management commands, which don't start an agent.
var subcommands = []string{"install", "remove", "uninstall", "update", "list", "config", "auth", "mcp"}

// ValidateArgs checks the user's Pi arguments (without the program) for
// options the native transport can't pass through.
func ValidateArgs(args []string) error {
	_, _, err := filterArgs(args)
	return err
}

// filterArgs returns the user's arguments to pass to Pi and the TUI-only
// ones it dropped, or an error for arguments that conflict with RPC mode.
func filterArgs(args []string) (kept, dropped []string, err error) {
	if len(args) > 0 && slices.Contains(subcommands, args[0]) {
		return nil, nil, fmt.Errorf("pi %s is a management command, not an agent; the native transport needs the agent", args[0])
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "-") {
			if strings.HasPrefix(arg, "@") {
				return nil, nil, fmt.Errorf("pi argument %q: Pi's RPC mode doesn't accept @file arguments; mention the file in a message instead", arg)
			}
			kept = append(kept, arg)
			continue
		}
		if arg == "--" {
			return nil, nil, fmt.Errorf("pi messages after \"--\" are not supported by the native transport; use --initial-prompt or POST /message")
		}
		if reason, ok := reservedFlags[name]; ok {
			return nil, nil, fmt.Errorf("pi option %s can't be used with the native transport: %s", name, reason)
		}
		if slices.Contains(tuiFlags, name) {
			dropped = append(dropped, arg)
			if !hasValue && i+1 < len(args) {
				i++
				dropped = append(dropped, args[i])
			}
			continue
		}
		if !slices.Contains(valueFlags, name) {
			// A flag without a value, or an extension flag, which Pi
			// parses in either form.
			kept = append(kept, arg)
			continue
		}
		if hasValue {
			kept = append(kept, name, value)
			continue
		}
		kept = append(kept, arg)
		if i+1 < len(args) {
			i++
			kept = append(kept, args[i])
		}
	}
	return kept, dropped, nil
}

// buildArgs returns Pi's arguments: RPC mode, the session, then the
// user's arguments.
func buildArgs(userArgs []string, sessionID string) (args, dropped []string, err error) {
	kept, dropped, err := filterArgs(userArgs)
	if err != nil {
		return nil, nil, err
	}
	args = append([]string{"--mode", "rpc", "--session-id", sessionID}, kept...)
	return args, dropped, nil
}

// newSessionID returns a session ID in the format AgentAPI also uses for
// Pi in the terminal transport.
func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "agentapi-" + hex.EncodeToString(b[:])
}

// leakedEnv are variables that mark the environment as running inside an
// agent session. They leak into Pi, and into the commands its tools run,
// when AgentAPI itself is started from such a session.
var leakedEnv = []string{
	"PI_CODING_AGENT", "AI_AGENT",
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
	"CODEX_THREAD_ID",
}

// agentEnv returns env (the process environment if nil) without the
// variables leaked from a parent agent session.
func agentEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(leakedEnv, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
