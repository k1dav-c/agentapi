package msgfmt

import (
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readInputBoxFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := testdataDir.ReadFile(path.Join("testdata/input_box", name))
	require.NoError(t, err)
	return string(b)
}

func TestStabilityRegion(t *testing.T) {
	t.Run("claude ignores ticking background agents panel", func(t *testing.T) {
		screen := readInputBoxFixture(t, "claude_background_agents.txt")
		ticked := strings.Replace(screen, "23s", "24s", 1)
		require.NotEqual(t, screen, ticked)

		region := StabilityRegion(AgentTypeClaude, screen)
		assert.Equal(t, region, StabilityRegion(AgentTypeClaude, ticked))
		assert.NotContains(t, region, "general-purpose")
		assert.NotContains(t, region, "bypass permissions")
		assert.Contains(t, region, "❯")
	})

	t.Run("claude keeps spinner above the input box", func(t *testing.T) {
		screen := readInputBoxFixture(t, "claude_working.txt")
		ticked := strings.Replace(screen, "1m 51s", "1m 52s", 1)

		region := StabilityRegion(AgentTypeClaude, screen)
		assert.Contains(t, region, "Boondoggling")
		assert.NotContains(t, region, "esc to interrupt")
		assert.NotEqual(t, region, StabilityRegion(AgentTypeClaude, ticked))
	})

	t.Run("codex ignores footer below composer", func(t *testing.T) {
		screen := readInputBoxFixture(t, "codex_idle.txt")
		changed := strings.Replace(screen, "⚠ 1 warning", "⚠ 2 warnings", 1)

		region := StabilityRegion(AgentTypeCodex, screen)
		assert.Equal(t, region, StabilityRegion(AgentTypeCodex, changed))
		assert.True(t, strings.HasSuffix(region, "› Ask Codex to do anything"), region)
	})

	t.Run("codex multi-line draft stays in region", func(t *testing.T) {
		screen := "history\n\n› first line\n  second line\n\n  GPT medium · ~\n  ? for shortcuts"
		assert.Equal(t, "history\n\n› first line\n  second line", StabilityRegion(AgentTypeCodex, screen))
	})

	t.Run("dialog without input box returns full screen", func(t *testing.T) {
		screen := readInputBoxFixture(t, "codex_usage_limit.txt")
		assert.Equal(t, screen, StabilityRegion(AgentTypeCodex, screen))
	})

	t.Run("other agents are unchanged", func(t *testing.T) {
		screen := readInputBoxFixture(t, "claude_background_agents.txt")
		assert.Equal(t, screen, StabilityRegion(AgentTypeGoose, screen))
	})
}

func TestHasInputBox(t *testing.T) {
	cases := []struct {
		name      string
		agentType AgentType
		fixture   string
		want      bool
	}{
		{"claude idle with agents panel", AgentTypeClaude, "claude_background_agents.txt", true},
		{"claude working", AgentTypeClaude, "claude_working.txt", true},
		{"codex idle", AgentTypeCodex, "codex_idle.txt", true},
		{"codex usage limit dialog", AgentTypeCodex, "codex_usage_limit.txt", false},
		{"codex question tool", AgentTypeCodex, "codex_question.txt", false},
		{"codex question tool below a user message", AgentTypeCodex, "codex_question_with_history.txt", false},
		{"codex question notes field", AgentTypeCodex, "codex_question_notes.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			visible, ok := HasInputBox(c.agentType, readInputBoxFixture(t, c.fixture))
			assert.True(t, ok)
			assert.Equal(t, c.want, visible)
		})
	}

	t.Run("claude selection list is not an input box", func(t *testing.T) {
		border := strings.Repeat("─", 40)
		screen := strings.Join([]string{border, " ❯ 1. Yes", "   2. No", border, " Enter to confirm · Esc to cancel"}, "\n")
		visible, ok := HasInputBox(AgentTypeClaude, screen)
		assert.True(t, ok)
		assert.False(t, visible)
	})

	t.Run("unsupported agent", func(t *testing.T) {
		_, ok := HasInputBox(AgentTypeAider, "> ")
		assert.False(t, ok)
	})
}

func TestFormatClaudeMessageStripsBackgroundAgentsPanel(t *testing.T) {
	screen := "● STARTED\n\n" + readInputBoxFixture(t, "claude_background_agents.txt")
	assert.Equal(t, "● STARTED", FormatAgentMessage(AgentTypeClaude, screen, ""))
}

func TestFormatCodexMessageStripsFooter(t *testing.T) {
	screen := "• PONG\n\n" + readInputBoxFixture(t, "codex_idle.txt")
	got := FormatAgentMessage(AgentTypeCodex, screen, "")
	assert.NotContains(t, got, "Ask Codex")
	assert.NotContains(t, got, "for shortcuts")
	assert.True(t, strings.HasPrefix(got, "• PONG"), got)
}
