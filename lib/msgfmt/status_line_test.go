package msgfmt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemoveAgentStatusLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		agentType AgentType
		in, want  string
	}{
		{
			"claude spinner and its tip",
			AgentTypeClaude,
			"● Running the command.\n\n✻ Fermenting… (6s · ↓ 80 tokens)\n  ⎿  Tip: Use ctrl+v to paste images from your clipboard",
			"● Running the command.\n",
		},
		{
			"claude spinner glyphs and esc hint",
			AgentTypeClaude,
			"· Fermenting… (7s · ↓ 80 tokens)\n✢ Thinking… (esc to interrupt)\nkept",
			"kept",
		},
		{
			"claude spinner before its timer shows",
			AgentTypeClaude,
			"● Running 1 shell command…\n· Contemplating…\n✢ Contemplating…",
			"● Running 1 shell command…",
		},
		{
			"claude tool timers",
			AgentTypeClaude,
			"● Waiting 8 seconds then printing a · 2s\n  ⎿  $ sleep 8 && echo a (3s)\n  Took 1m (2 tools)",
			"● Waiting 8 seconds then printing a\n  ⎿  $ sleep 8 && echo a\n  Took 1m (2 tools)",
		},
		{
			"claude keeps the finished line and ordinary bullets",
			AgentTypeClaude,
			"✻ Brewed for 22s · done 2:44 PM\n* a list item… (see below)\n● Tip: not a spinner tip",
			"✻ Brewed for 22s · done 2:44 PM\n* a list item… (see below)\n● Tip: not a spinner tip",
		},
		{
			"codex working line with background note",
			AgentTypeCodex,
			"• The first command printed a.\n\n◦ Working (19s • esc to interrupt) · 1 background terminal running · /ps to view…\n• Working (2s • esc to interrupt)",
			"• The first command printed a.\n",
		},
		{
			"codex keeps a bullet that mentions working",
			AgentTypeCodex,
			"• Working tree is clean.",
			"• Working tree is clean.",
		},
		{
			"pi spinner rule and elapsed time",
			AgentTypePi,
			" $ sleep 12 (timeout 20s)\n Elapsed 2.0s\n── ⠼ Working ───────────────────",
			" $ sleep 12 (timeout 20s)",
		},
		{
			"other agents are untouched",
			AgentTypeAider,
			"◦ Working (19s • esc to interrupt)",
			"◦ Working (19s • esc to interrupt)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, removeAgentStatusLines(tc.agentType, tc.in))
		})
	}
}
