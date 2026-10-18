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
		"AGENTAPI_PORT=3284",
	}
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"CLAUDE_CONFIG_DIR=/home/u/.claude",
		"CODEX_HOME=/home/u/.codex",
		"ANTHROPIC_API_KEY=key",
		"AGENTAPI_PORT=3284",
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	}, procenv.ForAgent(env, "CLAUDE_CODE_ENTRYPOINT=sdk-cli"))
	assert.NotEmpty(t, procenv.ForAgent(nil), "nil means the process environment")
}

func TestWithoutAgentAPI(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"PATH=/usr/bin"}, procenv.WithoutAgentAPI([]string{"AGENTAPI_PORT=3284", "PATH=/usr/bin", "AGENTAPI_API_TOKEN=x"}))
}
