package msgfmt

import (
	"regexp"
	"strings"
)

// While they work, agents redraw a status line every fraction of a second:
// a spinner glyph that cycles, seconds that tick, a token count. Kept in
// the message, it changed the message several times a second with nothing
// new to read, and the chat redrew the live screen each time (the clients
// show their own working state and elapsed time). These patterns match
// only those lines, from each agent's real screen.
var agentStatusLines = map[AgentType][]*regexp.Regexp{
	// "✻ Fermenting… (6s · ↓ 80 tokens)", "· Fermenting… (esc to interrupt)",
	// and before its timer shows, just "· Contemplating…"
	AgentTypeClaude: {
		regexp.MustCompile(`^\s*[·✢✳✶✻✽*]\s+\S[^()]*…\s*\((?:\d+[smh]\b|[^)]*esc to interrupt)[^)]*\)\s*$`),
		regexp.MustCompile(`^\s*[·✢✳✶✻✽*]\s+[A-Z][a-z]+…\s*$`),
	},
	// "◦ Working (19s • esc to interrupt) · 1 background terminal running · /ps to view…"
	AgentTypeCodex: {regexp.MustCompile(`^\s*[•◦]\s+Working\s+\(\d+[smh][^)]*\).*$`)},
	// "── ⠼ Working ────", and "Elapsed 2.0s" under a command that is running
	AgentTypePi: {
		regexp.MustCompile(`^\s*─*\s*[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]\s+Working\b.*$`),
		regexp.MustCompile(`^\s*Elapsed \d+(?:\.\d+)?s\s*$`),
	},
	// No status line patterns for these yet.
	AgentTypeGoose:    nil,
	AgentTypeAider:    nil,
	AgentTypeGemini:   nil,
	AgentTypeCopilot:  nil,
	AgentTypeAmp:      nil,
	AgentTypeCursor:   nil,
	AgentTypeAuggie:   nil,
	AgentTypeAmazonQ:  nil,
	AgentTypeOpencode: nil,
	AgentTypeKimi:     nil,
	AgentTypeCustom:   nil,
}

// claudeToolTimerRe matches the elapsed time Claude Code appends to a
// running tool's lines ("Waiting 8 seconds · 2s", "$ sleep 8 (3s)"),
// which ticks every second.
var claudeToolTimerRe = regexp.MustCompile(`(?: · \d+[smh]| \(\d+[smh]\))\s*$`)

// claudeSpinnerTipRe matches the tip Claude Code shows under its spinner,
// which goes with it.
var claudeSpinnerTipRe = regexp.MustCompile(`^\s*⎿\s+Tip:`)

// removeAgentStatusLines drops the agent's working status lines from a
// message.
func removeAgentStatusLines(agentType AgentType, message string) string {
	patterns := agentStatusLines[agentType]
	if len(patterns) == 0 {
		return message
	}
	lines := strings.Split(message, "\n")
	kept := lines[:0]
	droppedPrevious := false
	for _, line := range lines {
		drop := false
		for _, re := range patterns {
			if re.MatchString(line) {
				drop = true
				break
			}
		}
		if !drop && droppedPrevious && agentType == AgentTypeClaude && claudeSpinnerTipRe.MatchString(line) {
			drop = true
		}
		droppedPrevious = drop
		if drop {
			continue
		}
		if agentType == AgentTypeClaude {
			line = claudeToolTimerRe.ReplaceAllString(line, "")
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
