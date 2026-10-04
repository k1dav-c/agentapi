package httpapi

import (
	"testing"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/quartz"
	"github.com/stretchr/testify/require"
)

func TestIsCodexUpdatePrompt(t *testing.T) {
	require.True(t, isCodexUpdatePrompt(readScreenFixture(t, "codex_update_prompt.txt")))
	// After skipping, Codex leaves the boxed update banner in the transcript
	// above the composer; that is not a dialog.
	require.False(t, isCodexUpdatePrompt(readScreenFixture(t, "codex_composer_after_update.txt")))
	for _, fixture := range []string{"codex_plan_prompt.txt", "codex_approval_prompt.txt", "codex_question.txt", "codex_trust_prompt.txt"} {
		require.False(t, isCodexUpdatePrompt(readScreenFixture(t, fixture)), fixture)
	}
}

func TestDismissCodexUpdatePrompt(t *testing.T) {
	s, c, terminal := handoffServer()
	clock := quartz.NewMock(t)
	s.clock = clock
	s.agentType = mf.AgentTypeCodex
	s.emitter.EmitScreen(readScreenFixture(t, "codex_update_prompt.txt"))

	// The dialog is still rendering.
	c.status = st.ConversationStatusChanging
	s.dismissCodexUpdatePrompt()
	require.Empty(t, terminal.writes)

	c.status = st.ConversationStatusStable
	s.dismissCodexUpdatePrompt()
	require.Equal(t, []string{"\x1b"}, terminal.writes)

	// Give Codex time to close the dialog before pressing Esc again.
	s.dismissCodexUpdatePrompt()
	require.Len(t, terminal.writes, 1)
	clock.Advance(codexUpdatePromptRetryInterval + time.Second)
	s.dismissCodexUpdatePrompt()
	require.Len(t, terminal.writes, 2)

	clock.Advance(codexUpdatePromptRetryInterval + time.Second)
	s.emitter.EmitScreen(readScreenFixture(t, "codex_composer_after_update.txt"))
	s.dismissCodexUpdatePrompt()
	require.Len(t, terminal.writes, 2)

	// Other agents' screens are never touched.
	s.agentType = mf.AgentTypeClaude
	s.emitter.EmitScreen(readScreenFixture(t, "codex_update_prompt.txt"))
	s.dismissCodexUpdatePrompt()
	require.Len(t, terminal.writes, 2)
}
