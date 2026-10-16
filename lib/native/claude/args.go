package claude

import (
	"fmt"
	"strings"
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
	"--permission-prompt-tool": "permission prompts are answered through AgentAPI's requests",
	"--permission-prompts":     "permission prompts are answered through AgentAPI's requests",
	"--bg":                     "the native transport runs Claude Code in the foreground",
	"--background":             "the native transport runs Claude Code in the foreground",
	"--cloud":                  "the native transport runs Claude Code locally",
	"--desktop":                "the native transport runs Claude Code headless",
	"--remote-control":         "the native transport runs Claude Code headless",
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
	"--input-format": true, "--json-schema": true, "--max-budget-usd": true, "--model": true,
	"-n": true, "--name": true, "--output-format": true, "--permission-mode": true,
	"--permission-prompts": true, "--permission-prompt-tool": true, "--plugin-dir": true,
	"--plugin-url": true, "--remote-control-session-name-prefix": true, "--session-id": true,
	"--setting-sources": true, "--settings": true, "--system-prompt": true,
	"--system-prompt-file": true, "--append-system-prompt-file": true,
	"--system-prompt-snapshot": true, "--thinking": true, "--thinking-display": true,
	"--max-thinking-tokens": true, "--max-turns": true,
}

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
	// permissionMode: the user chose a permission mode (or to skip
	// permissions).
	permissionMode bool
	// bypass: the session runs in bypassPermissions, the default when the
	// user chooses no mode.
	bypass       bool
	thinking     bool
	thinkingOff  bool
	thinkingShow bool
	// noPersistence: sessions are not saved, so they can't be resumed.
	noPersistence bool
}

// ValidateArgs checks the user's Claude Code arguments (after the program)
// for flags that conflict with the native transport.
func ValidateArgs(args []string) error {
	_, err := parseArgs(args)
	return err
}

// modeBypass is the permission mode used when the user chooses none.
const modeBypass = "bypassPermissions"

func parseArgs(args []string) (parsedArgs, error) {
	p := parsedArgs{bypass: true}
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
		if reason, ok := conflictingFlags[name]; ok {
			return parsedArgs{}, fmt.Errorf("%s can't be used with the native transport: %s", name, reason)
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
			p.permissionMode = true
			p.bypass = value == modeBypass
		case "--dangerously-skip-permissions":
			p.permissionMode = true
			p.bypass = true
		case "--thinking":
			p.thinking = true
			p.thinkingOff = value == "disabled"
		case "--max-thinking-tokens":
			p.thinking = true
		case "--thinking-display":
			p.thinkingShow = true
		case "--no-session-persistence":
			p.noPersistence = true
		}
	}
	return p, nil
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
	if !p.permissionMode {
		// Tools run without asking unless the user picks a mode (e.g.
		// --permission-mode default to approve them from the chat).
		// Questions and plans still come as requests.
		args = append(args, "--permission-mode", modeBypass)
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

// leakedEnv are variables a running Claude Code session sets for the
// processes it starts (its Bash tool, hooks, MCP servers). They describe
// that parent session, not the one AgentAPI starts: its session id and pid,
// its messaging socket and token, its IDE connection, its effort level, the
// marker that the process runs inside Claude Code and the SDK entrypoint.
// Configuration such as CLAUDE_CONFIG_DIR or ANTHROPIC_* is kept.
var leakedEnv = map[string]bool{
	"CLAUDECODE":                            true,
	"CLAUDE_CODE_ENTRYPOINT":                true,
	"CLAUDE_CODE_SESSION_ID":                true,
	"CLAUDE_CODE_CHILD_SESSION":             true,
	"CLAUDE_CODE_SESSION_ATTENDED":          true,
	"CLAUDE_CODE_EXECPATH":                  true,
	"CLAUDE_CODE_MESSAGING_SOCKET":          true,
	"CLAUDE_CODE_MESSAGING_TOKEN":           true,
	"CLAUDE_CODE_SSE_PORT":                  true,
	"CLAUDE_PID":                            true,
	"CLAUDE_EFFORT":                         true,
	"CLAUDE_AGENT_SDK_VERSION":              true,
	"AI_AGENT":                              true,
	"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": true,
	"CLAUDE_CODE_SDK_READS_SESSION_STATE":   true,
}

// buildEnv filters env and adds the variables the adapter relies on.
func buildEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if leakedEnv[name] {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		// Reports session state changes; "idle" is the turn-over signal.
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1",
		// The entrypoint for the CLI driven by another program.
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	)
}
