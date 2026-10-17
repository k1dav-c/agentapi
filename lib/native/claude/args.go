package claude

import (
	"fmt"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// protocolFlags are always added: headless mode, the stream-json protocol in
// both directions, partial messages (live text and thinking), the echo of
// user messages (shows where a steered message landed) and permission
// prompts over the control protocol.
var protocolFlags = []string{
	"-p",
	"--output-format", "stream-json",
	"--input-format", "stream-json",
	"--verbose",
	"--include-partial-messages",
	"--replay-user-messages",
	"--permission-prompt-tool", "stdio",
}

// conflictingFlags change the protocol or the session the adapter manages,
// so they are rejected with the reason.
var conflictingFlags = map[string]string{
	"-p":                       "the native transport already runs Claude Code headless",
	"--print":                  "the native transport already runs Claude Code headless",
	"--output-format":          "the native transport speaks stream-json",
	"--input-format":           "the native transport speaks stream-json",
	"-r":                       "AgentAPI picks the session to resume",
	"--resume":                 "AgentAPI picks the session to resume",
	"-c":                       "AgentAPI picks the session to resume",
	"--continue":               "AgentAPI picks the session to resume",
	"--session-id":             "AgentAPI picks the session id",
	"--fork-session":           "AgentAPI picks the session to resume",
	"--from-pr":                "AgentAPI picks the session to resume",
	"--teleport":               "AgentAPI picks the session to resume",
	"--resume-session-at":      "AgentAPI picks the session to resume",
	"--resume-drops-turn":      "AgentAPI picks the session to resume",
	"--permission-prompt-tool": "permission prompts are answered through AgentAPI's requests",
	"--permission-prompts":     "permission prompts are answered through AgentAPI's requests",
	"--sdk-url":                "the native transport speaks stream-json on stdin and stdout",
	"--bg":                     "the native transport runs Claude Code in the foreground",
	"--background":             "the native transport runs Claude Code in the foreground",
	"--cloud":                  "the native transport runs Claude Code locally",
	"--remote":                 "the native transport runs Claude Code locally",
	"--desktop":                "the native transport runs Claude Code headless",
	"--remote-control":         "the native transport runs Claude Code headless",
	"--rc":                     "the native transport runs Claude Code headless",
}

// redundantFlags are protocol flags the adapter adds anyway; a user's copy is
// dropped.
var redundantFlags = map[string]bool{
	"--verbose":                  true,
	"--include-partial-messages": true,
	"--replay-user-messages":     true,
}

// valueFlags take a value as the next argument (unless given as
// --flag=value), so the scan doesn't mistake the value for a flag.
var valueFlags = map[string]bool{
	"--agent": true, "--agents": true, "--append-system-prompt": true, "--autocompact": true,
	"--debug-file": true, "--effort": true, "--environment": true, "--fallback-model": true,
	"--json-schema": true, "--max-budget-usd": true, "--model": true,
	"-n": true, "--name": true, "--permission-mode": true, "--plugin-dir": true,
	"--plugin-url": true, "--remote-control-session-name-prefix": true,
	"--setting-sources": true, "--settings": true, "--system-prompt": true,
	"--system-prompt-file": true, "--append-system-prompt-file": true,
	"--system-prompt-snapshot": true, "--thinking": true, "--thinking-display": true,
	"--max-thinking-tokens": true, "--max-turns": true,
}

// shortValueFlags are the short flags that take a value: -d [filter],
// -n <name>, -r [value] and -w [name].
var shortValueFlags = map[string]bool{"-d": true, "-n": true, "-r": true, "-w": true}

// variadicFlags take every following argument up to the next flag.
var variadicFlags = map[string]bool{
	"--add-dir": true, "--allowedTools": true, "--allowed-tools": true, "--betas": true,
	"--disallowedTools": true, "--disallowed-tools": true, "--file": true,
	"--mcp-config": true, "--tools": true,
}

// parsedArgs is what the adapter needs to know about the user's arguments.
type parsedArgs struct {
	// pass are the user's arguments to pass through.
	pass []string
	// mode is the permission mode the adapter adds because the user chose
	// none (bypassPermissions, see avoidRootBypass); empty when the user
	// chose one or to skip permissions.
	mode string
	// bypass: the session runs in bypassPermissions.
	bypass bool
	// allowBypass: the session can switch to bypassPermissions, so
	// --allow-dangerously-skip-permissions is added unless the user's
	// arguments already allow it (skipPermissions, allowFlag). Claude Code
	// refuses it as root outside a sandbox; see avoidRootBypass.
	allowBypass     bool
	skipPermissions bool
	allowFlag       bool
	// model is the user's --model.
	model        string
	thinking     bool
	thinkingOff  bool
	thinkingShow bool
	// noPersistence: sessions are not saved, so they can't be resumed.
	noPersistence bool
	// settings: the user passed --settings, which can hold credentials.
	settings bool
}

// Permission modes: bypassPermissions is used when the user chooses none;
// in default Claude Code asks before it runs a tool.
const (
	modeBypass  = "bypassPermissions"
	modeDefault = "default"
)

// flagNames returns the flags name stands for: name itself, or each flag
// of a group of short flags, which Claude Code splits (-pc is -p -c). A
// flag in a group that takes a value takes the rest of the group (-dapi is
// -d api).
func flagNames(name string) []string {
	if len(name) <= 2 || name[1] == '-' {
		return []string{name}
	}
	var flags []string
	for _, c := range name[1:] {
		flag := "-" + string(c)
		flags = append(flags, flag)
		if shortValueFlags[flag] {
			break
		}
	}
	return flags
}

func parseArgs(args []string) (parsedArgs, error) {
	p := parsedArgs{mode: modeBypass, bypass: true, allowBypass: true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			p.pass = append(p.pass, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			p.pass = append(p.pass, arg)
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		for _, flag := range flagNames(name) {
			reason, ok := conflictingFlags[flag]
			if !ok {
				continue
			}
			if flag != name {
				flag += " (in " + name + ")"
			}
			return parsedArgs{}, fmt.Errorf("%s can't be used with the native transport: %s", flag, reason)
		}
		if redundantFlags[name] {
			continue
		}
		p.pass = append(p.pass, arg)
		if valueFlags[name] && !hasValue && i+1 < len(args) {
			i++
			value = args[i]
			p.pass = append(p.pass, value)
		}
		if variadicFlags[name] && !hasValue {
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				p.pass = append(p.pass, args[i])
			}
		}
		switch name {
		case "--permission-mode":
			p.mode = ""
			p.bypass = value == modeBypass
		case "--dangerously-skip-permissions":
			p.mode = ""
			p.bypass = true
			p.skipPermissions = true
		case "--allow-dangerously-skip-permissions":
			p.allowFlag = true
		case "--model":
			p.model = value
		case "--thinking":
			p.thinking = true
			p.thinkingOff = value == "disabled"
		case "--max-thinking-tokens":
			p.thinking = true
		case "--thinking-display":
			p.thinkingShow = true
		case "--no-session-persistence":
			p.noPersistence = true
		case "--settings":
			p.settings = true
		}
	}
	return p, nil
}

// avoidRootBypass keeps bypassPermissions out of the session where Claude
// Code refuses it: run as root (uid 0) outside a sandbox it knows of
// (IS_SANDBOX=1, CLAUDE_CODE_BUBBLEWRAP), it exits at startup ("cannot be
// used with root/sudo privileges"). The mode the adapter adds becomes
// default, and the session can't switch to bypassPermissions. env is the
// session's environment and uid the user it runs as. It reports whether
// it changed the mode.
func (p *parsedArgs) avoidRootBypass(env []string, uid int) bool {
	if uid != 0 || envValue(env, "IS_SANDBOX") == "1" || envTrue(envValue(env, "CLAUDE_CODE_BUBBLEWRAP")) {
		return false
	}
	p.allowBypass = false
	if p.mode != modeBypass {
		return false
	}
	p.mode = modeDefault
	p.bypass = false
	return true
}

// envValue returns the value of the variable name in env, the last one if
// it is set more than once (as exec does).
func envValue(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			value = v
		}
	}
	return value
}

// envTrue reports whether Claude Code reads value as an enabled flag.
func envTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// buildArgs returns the full argument list (without the program): the
// protocol flags, the session flag, the defaults the user didn't override,
// then the user's own arguments.
func buildArgs(p parsedArgs, sessionID, resumeRef string) []string {
	args := append([]string(nil), protocolFlags...)
	if resumeRef != "" {
		// The = form keeps a value starting with "-" from being read as a flag.
		args = append(args, "--resume="+resumeRef)
	} else if sessionID != "" {
		args = append(args, "--session-id="+sessionID)
	}
	if p.mode != "" {
		// Tools run without asking unless the user picks a mode (e.g.
		// --permission-mode default to approve them from the chat).
		// Questions and plans still come as requests.
		args = append(args, "--permission-mode", p.mode)
	}
	if p.allowBypass && !p.skipPermissions && !p.allowFlag {
		// Without it, a switch to bypassPermissions is ignored.
		args = append(args, "--allow-dangerously-skip-permissions")
	}
	if !p.thinking {
		args = append(args, "--thinking", "adaptive")
	}
	if !p.thinkingShow && !p.thinkingOff {
		// Without it thinking blocks arrive empty (only a signature).
		args = append(args, "--thinking-display", "summarized")
	}
	return append(args, p.pass...)
}

// buildEnv returns env (the process environment if nil) without the
// variables a parent agent session leaves behind, and adds the ones the
// adapter relies on.
func buildEnv(env []string) []string {
	return native.AgentEnv(env,
		// Reports session state changes; "idle" is the turn-over signal.
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1",
		// The entrypoint for the CLI driven by another program.
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	)
}
