// Package procenv is the environment AgentAPI gives the processes it
// starts: the agent and the terminal's shell.
package procenv

import (
	"os"
	"slices"
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

// agentAPISecret reports whether name is one of AgentAPI's secrets, which an
// agent and the commands it runs have no use for, and a prompt could have
// them print: AGENTAPI_API_TOKEN, AGENTAPI_WEBHOOK_SECRET, the hub's
// AGENTAPI_HUB_SECRET, AGENTAPI_HUB_TOKEN and AGENTAPI_HUB_INSTANCE_TOKEN,
// the generated token a restart in place keeps; any AGENTAPI_*_TOKEN or
// AGENTAPI_*_SECRET.
func agentAPISecret(name string) bool {
	return strings.HasPrefix(name, "AGENTAPI_") &&
		(strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_SECRET"))
}

// ForAgent returns env (the process environment if nil) without the
// variables a parent agent session leaves behind and AgentAPI's secrets,
// followed by set: the "NAME=value" entries the caller relies on. The rest
// of AgentAPI's variables stay (WithoutAgentAPI drops them too). With an
// HTTP proxy set, loopback is left out of it (bypassLoopback).
func ForAgent(env []string, set ...string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+len(set)+2)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !parentSessionEnv[name] && !agentAPISecret(name) {
			out = append(out, kv)
		}
	}
	return bypassLoopback(append(out, set...))
}

// proxyEnv are the variables that send an agent's HTTP through a proxy.
var proxyEnv = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}

// loopbackHosts are what a proxy must not get: the agent reaches its model
// provider through AgentAPI's proxy on 127.0.0.1, which a proxy elsewhere
// can't reach.
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// bypassLoopback returns env with loopbackHosts added to NO_PROXY and
// no_proxy, when a proxy variable is set; both, as tools read either.
func bypassLoopback(env []string) []string {
	proxied := false
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if value != "" && slices.Contains(proxyEnv, name) {
			proxied = true
		}
	}
	if !proxied {
		return env
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		i := slices.IndexFunc(env, func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
		var hosts []string
		if i >= 0 {
			for host := range strings.SplitSeq(strings.TrimPrefix(env[i], name+"="), ",") {
				if host = strings.TrimSpace(host); host != "" {
					hosts = append(hosts, host)
				}
			}
		}
		for _, host := range loopbackHosts {
			if !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
		kv := name + "=" + strings.Join(hosts, ",")
		if i >= 0 {
			env[i] = kv
		} else {
			env = append(env, kv)
		}
	}
	return env
}

// WithoutAgentAPI returns env without AgentAPI's own variables, which would
// configure an agentapi started from it (the terminal's shell).
func WithoutAgentAPI(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "AGENTAPI_") {
			out = append(out, kv)
		}
	}
	return out
}
