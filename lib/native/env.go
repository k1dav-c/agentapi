package native

import (
	"os"
	"strings"
)

// parentSessionEnv are variables coding agents set for the processes they
// start (their shell tool, hooks, MCP servers). They describe that parent
// session, not the one AgentAPI starts, and Claude Code's messaging token
// is a secret. When AgentAPI itself runs inside an agent session they would
// leak into the agent it starts and into every command that agent runs: a
// nested claude refuses to start under CLAUDECODE, and Codex's sandbox
// markers make Codex believe it runs in a sandbox. Configuration such as
// CLAUDE_CONFIG_DIR, CODEX_HOME or ANTHROPIC_* is kept.
var parentSessionEnv = map[string]bool{
	// Claude Code: its session, pid, messaging socket and token, IDE
	// connection, effort level and SDK entrypoint.
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
	"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": true,
	"CLAUDE_CODE_SDK_READS_SESSION_STATE":   true,
	// Codex: its thread, sandbox and session recording.
	"CODEX_THREAD_ID":                    true,
	"CODEX_SANDBOX":                      true,
	"CODEX_SANDBOX_NETWORK_DISABLED":     true,
	"CODEX_CI":                           true,
	"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": true,
	"CODEX_TUI_RECORD_SESSION":           true,
	"CODEX_TUI_SESSION_LOG_PATH":         true,
	// Pi, and the marker several agents set.
	"PI_CODING_AGENT": true,
	"AI_AGENT":        true,
}

// AgentEnv returns env (the process environment if nil) without the
// variables a parent agent session leaves behind, followed by set: the
// "NAME=value" entries the adapter relies on.
func AgentEnv(env []string, set ...string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !parentSessionEnv[name] {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}
