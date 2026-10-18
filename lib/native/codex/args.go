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
	// policyFromUser is set when the user chose the approval policy, the
	// sandbox or the approvals reviewer. Otherwise the thread starts in the
	// bypass mode, the native transport's default: no approvals and no
	// sandbox.
	policyFromUser bool
}

// policyKeys are the config keys of the policies.
var policyKeys = []string{"approval_policy", "sandbox_mode", "approvals_reviewer"}

// flagFunc applies the value of the flag name to opts; cwd resolves a
// relative path.
type flagFunc func(opts *launchOptions, name, value, cwd string) error

// valueFlags are the flags with a value that AgentAPI accepts: what each
// does with it.
var valueFlags = map[string]flagFunc{
	// app-server takes these itself.
	"-c":               passConfig,
	"--config":         passConfig,
	"--enable":         passThrough,
	"--disable":        passThrough,
	"--code-mode-host": passThrough,
	// The top-level codex flags become thread settings.
	"-m":                 setModel,
	"--model":            setModel,
	"-s":                 setSandbox,
	"--sandbox":          setSandbox,
	"-a":                 setApprovalPolicy,
	"--ask-for-approval": setApprovalPolicy,
	"-C":                 setCD,
	"--cd":               setCD,
}

// switchFlags are the flags without a value that AgentAPI accepts: what
// each does.
var switchFlags = map[string]func(opts *launchOptions, name string){
	// app-server takes these itself.
	"--strict-config":             passSwitch,
	"--analytics-default-enabled": passSwitch,
	// These only affect the terminal UI.
	"--no-alt-screen": dropSwitch,
	"--no-daemon":     dropSwitch,
	"--stdio":         dropSwitch,
	"--dangerously-bypass-approvals-and-sandbox": func(opts *launchOptions, _ string) {
		opts.approvalPolicy = "never"
		opts.sandbox = "danger-full-access"
		opts.policyFromUser = true
	},
}

func passConfig(opts *launchOptions, name, value, _ string) error {
	key, _, _ := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	opts.summaryFromUser = opts.summaryFromUser || key == "model_reasoning_summary"
	opts.policyFromUser = opts.policyFromUser || slices.Contains(policyKeys, key)
	opts.appServerArgs = append(opts.appServerArgs, name, value)
	return nil
}

func passThrough(opts *launchOptions, name, value, _ string) error {
	opts.appServerArgs = append(opts.appServerArgs, name, value)
	return nil
}

func passSwitch(opts *launchOptions, name string) {
	opts.appServerArgs = append(opts.appServerArgs, name)
}

func dropSwitch(*launchOptions, string) {}

func setModel(opts *launchOptions, _, value, _ string) error {
	opts.model = value
	return nil
}

func setSandbox(opts *launchOptions, name, value, _ string) error {
	if !slices.Contains(sandboxModes, value) {
		return fmt.Errorf("codex flag %s: unknown sandbox mode %q (one of %s)", name, value, strings.Join(sandboxModes, ", "))
	}
	opts.sandbox = value
	opts.policyFromUser = true
	return nil
}

func setApprovalPolicy(opts *launchOptions, name, value, _ string) error {
	if !slices.Contains(approvalPolicies, value) {
		return fmt.Errorf("codex flag %s: unknown approval policy %q (one of %s)", name, value, strings.Join(approvalPolicies, ", "))
	}
	opts.approvalPolicy = value
	opts.policyFromUser = true
	return nil
}

func setCD(opts *launchOptions, _, value, cwd string) error {
	if !filepath.IsAbs(value) && cwd != "" {
		value = filepath.Join(cwd, value)
	}
	opts.cwd = filepath.Clean(value)
	return nil
}

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
		name, value, hasValue := splitFlag(arg)
		if apply, ok := switchFlags[name]; ok {
			if hasValue {
				return opts, fmt.Errorf("codex flag %s takes no value", name)
			}
			apply(&opts, name)
			continue
		}
		if apply, ok := valueFlags[name]; ok {
			if !hasValue {
				if i+1 >= len(args) {
					return opts, fmt.Errorf("codex flag %s needs a value", name)
				}
				i++
				value = args[i]
			}
			if err := apply(&opts, name, value, cwd); err != nil {
				return opts, err
			}
			continue
		}
		switch {
		case rejectedFlags[name] != "":
			return opts, fmt.Errorf("codex flag %s is not supported with --transport native: %s", name, rejectedFlags[name])
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("codex flag %s is not supported with --transport native", name)
		default:
			return opts, fmt.Errorf("codex argument %q is not supported with --transport native: "+
				"subcommands and prompts can't be combined with app-server (use --initial-prompt for a first message)", arg)
		}
	}
	if !opts.policyFromUser {
		opts.approvalPolicy = "never"
		opts.sandbox = "danger-full-access"
	}
	return opts, nil
}

// splitFlag splits a value attached to a flag from it: --name=value, or a
// short flag's -mo3 or -m=o3, as clap allows.
func splitFlag(arg string) (name, value string, hasValue bool) {
	switch {
	case strings.HasPrefix(arg, "--"):
		return strings.Cut(arg, "=")
	case strings.HasPrefix(arg, "-") && len(arg) > 2:
		_, size := utf8.DecodeRuneInString(arg[1:])
		return arg[:1+size], strings.TrimPrefix(arg[1+size:], "="), true
	}
	return arg, "", false
}
