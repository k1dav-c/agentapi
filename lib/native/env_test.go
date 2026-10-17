package native_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
)

func TestAgentEnv(t *testing.T) {
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
	}
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"CLAUDE_CONFIG_DIR=/home/u/.claude",
		"CODEX_HOME=/home/u/.codex",
		"ANTHROPIC_API_KEY=key",
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	}, native.AgentEnv(env, "CLAUDE_CODE_ENTRYPOINT=sdk-cli"))
	assert.NotEmpty(t, native.AgentEnv(nil), "nil means the process environment")
}
