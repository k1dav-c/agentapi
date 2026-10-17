package codex

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// launchOptions is what the user's command line asks for, split into the
// flags app-server takes itself and the settings that go into thread/start.
type launchOptions struct {
	// appServerArgs follow `app-server` on the command line.
	appServerArgs []string
	// Thread settings translated from top-level codex flags. Empty means
	// Codex's config decides.
	model          string
	sandbox        string
	approvalPolicy string
	cwd            string
	// summaryFromUser is set when the user chose model_reasoning_summary
	// with -c; then turns don't override it.
	summaryFromUser bool
}

// passThroughFlags take a value and are accepted by `codex app-server`.
var passThroughFlags = []string{"-c", "--config", "--enable", "--disable", "--code-mode-host"}

// passThroughSwitches are app-server flags without a value.
var passThroughSwitches = []string{"--strict-config", "--analytics-default-enabled"}

// droppedSwitches only affect the terminal UI and are ignored.
var droppedSwitches = []string{"--no-alt-screen", "--no-daemon", "--stdio"}

// rejectedFlags can't be honored with app-server; the value is why.
var rejectedFlags = map[string]string{
	"-p":                              "profiles aren't supported; pass the settings with -c instead",
	"--profile":                       "profiles aren't supported; pass the settings with -c instead",
	"-i":                              "attach images to a message instead",
	"--image":                         "attach images to a message instead",
	"--oss":                           "set the provider with -c model_provider=... instead",
	"--local-provider":                "set the provider with -c model_provider=... instead",
	"--search":                        "enable web search in Codex's config (or with -c) instead",
	"--add-dir":                       "set sandbox_workspace_write.writable_roots with -c instead",
	"--approve-for-me":                "set approvals_reviewer with -c instead",
	"--dangerously-bypass-hook-trust": "not available in app-server mode",
	"--remote":                        "AgentAPI runs its own app-server",
	"--remote-auth-token-env":         "AgentAPI runs its own app-server",
	"--worktree":                      "start AgentAPI in the worktree instead",
	"--listen":                        "AgentAPI talks to app-server over stdio",
	"-h":                              "not a server option",
	"--help":                          "not a server option",
	"-V":                              "not a server option",
	"--version":                       "not a server option",
}

var sandboxModes = []string{"read-only", "workspace-write", "danger-full-access"}

var approvalPolicies = []string{"untrusted", "on-request", "never"}

// parseArgs splits the user's codex arguments (after the program name).
// cwd resolves a relative -C.
func parseArgs(args []string, cwd string) (launchOptions, error) {
	var opts launchOptions
	if len(args) > 0 && args[0] == "app-server" {
		args = args[1:]
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := arg, "", false
		switch {
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue = strings.Cut(arg, "=")
		case strings.HasPrefix(arg, "-") && len(arg) > 2:
			// A short flag's value can be attached, as clap allows: -mo3
			// or -m=o3.
			_, size := utf8.DecodeRuneInString(arg[1:])
			name, value, hasValue = arg[:1+size], strings.TrimPrefix(arg[1+size:], "="), true
		}
		takeValue := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("codex flag %s needs a value", name)
			}
			i++
			return args[i], nil
		}
		isSwitch := slices.Contains(passThroughSwitches, name) || slices.Contains(droppedSwitches, name) ||
			name == "--dangerously-bypass-approvals-and-sandbox"
		if isSwitch && hasValue {
			return opts, fmt.Errorf("codex flag %s takes no value", name)
		}

		switch {
		case slices.Contains(passThroughFlags, name):
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			key, _, _ := strings.Cut(v, "=")
			if (name == "-c" || name == "--config") && strings.TrimSpace(key) == "model_reasoning_summary" {
				opts.summaryFromUser = true
			}
			opts.appServerArgs = append(opts.appServerArgs, name, v)
		case slices.Contains(passThroughSwitches, name):
			opts.appServerArgs = append(opts.appServerArgs, name)
		case slices.Contains(droppedSwitches, name):
		case name == "-m" || name == "--model":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			opts.model = v
		case name == "-s" || name == "--sandbox":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			if !slices.Contains(sandboxModes, v) {
				return opts, fmt.Errorf("codex flag %s: unknown sandbox mode %q (one of %s)", name, v, strings.Join(sandboxModes, ", "))
			}
			opts.sandbox = v
		case name == "-a" || name == "--ask-for-approval":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			if !slices.Contains(approvalPolicies, v) {
				return opts, fmt.Errorf("codex flag %s: unknown approval policy %q (one of %s)", name, v, strings.Join(approvalPolicies, ", "))
			}
			opts.approvalPolicy = v
		case name == "-C" || name == "--cd":
			v, err := takeValue()
			if err != nil {
				return opts, err
			}
			if !filepath.IsAbs(v) && cwd != "" {
				v = filepath.Join(cwd, v)
			}
			opts.cwd = filepath.Clean(v)
		case name == "--dangerously-bypass-approvals-and-sandbox":
			opts.approvalPolicy = "never"
			opts.sandbox = "danger-full-access"
		case rejectedFlags[name] != "":
			return opts, fmt.Errorf("codex flag %s is not supported with --transport native: %s", name, rejectedFlags[name])
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("codex flag %s is not supported with --transport native", name)
		default:
			return opts, fmt.Errorf("codex argument %q is not supported with --transport native: "+
				"subcommands and prompts can't be combined with app-server (use --initial-prompt for a first message)", arg)
		}
	}
	return opts, nil
}
