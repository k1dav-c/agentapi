package procenv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/procenv"
)

func TestForAgent(t *testing.T) {
	t.Parallel()
	env := []string{
		"PATH=/usr/bin",
		"CLAUDECODE=1",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"CLAUDE_CONFIG_DIR=/home/u/.claude",
		"CODEX_SANDBOX=seatbelt",
		"CODEX_HOME=/home/u/.codex",
		"PI_CODING_AGENT=true",
		"AI_AGENT=claude-code",
		"ANTHROPIC_API_KEY=key",
		"AGENTAPI_API_TOKEN=secret",
		"AGENTAPI_WEBHOOK_SECRET=secret",
		"AGENTAPI_HUB_SECRET=secret",
		"AGENTAPI_HUB_TOKEN=secret",
		"AGENTAPI_HUB_INSTANCE_TOKEN=secret",
		"AGENTAPI_GENERATED_API_TOKEN=secret",
		"AGENTAPI_TOKEN=secret",
		"AGENTAPI_PORT=3284",
		"AGENTAPI_WEBHOOK_URL=http://hub",
		"GITHUB_TOKEN=kept",
	}
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"CLAUDE_CONFIG_DIR=/home/u/.claude",
		"CODEX_HOME=/home/u/.codex",
		"ANTHROPIC_API_KEY=key",
		"AGENTAPI_PORT=3284",
		"AGENTAPI_WEBHOOK_URL=http://hub",
		"GITHUB_TOKEN=kept",
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	}, procenv.ForAgent(env, "CLAUDE_CODE_ENTRYPOINT=sdk-cli"))
	assert.NotEmpty(t, procenv.ForAgent(nil), "nil means the process environment")
}

func TestWithoutAgentAPI(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"PATH=/usr/bin"}, procenv.WithoutAgentAPI([]string{"AGENTAPI_PORT=3284", "PATH=/usr/bin", "AGENTAPI_API_TOKEN=x"}))
}

// An agent behind an HTTP proxy reaches AgentAPI's provider proxy on
// 127.0.0.1 directly: loopback joins NO_PROXY and no_proxy, after what they
// had. Without a proxy nothing is added.
func TestForAgentBypassesLoopback(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"PATH=/usr/bin"}, procenv.ForAgent([]string{"PATH=/usr/bin"}))
	assert.Equal(t, []string{"HTTPS_PROXY=", "PATH=/usr/bin"}, procenv.ForAgent([]string{"HTTPS_PROXY=", "PATH=/usr/bin"}), "an empty proxy is none")
	assert.Equal(t, []string{
		"https_proxy=http://proxy:3128",
		"NO_PROXY=.corp,127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}, procenv.ForAgent([]string{"https_proxy=http://proxy:3128", "NO_PROXY=.corp, 127.0.0.1"}))
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://proxy:3128",
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}, procenv.ForAgent([]string{"PATH=/usr/bin"}, "HTTP_PROXY=http://proxy:3128"), "a proxy the caller sets counts too")
}
