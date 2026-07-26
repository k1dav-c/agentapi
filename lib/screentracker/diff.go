package screentracker

import (
	"strings"

	"github.com/coder/agentapi/lib/msgfmt"
)

// screenDiff compares two screen states and attempts to find latest message of the given agent type.
func screenDiff(oldScreen, newScreen string, agentType msgfmt.AgentType) string {
	oldLines := strings.Split(oldScreen, "\n")
	newLines := strings.Split(newScreen, "\n")
	oldLinesMap := make(map[string]bool)

	// -1 indicates no header
	dynamicHeaderEnd := -1

	// Skip header lines for Opencode agent type to avoid false positives
	// The header contains dynamic content (token count, context percentage, cost)
	// that changes between screens, causing line comparison mismatches:
	//
	// ┃  # Getting Started with Claude CLI                                   ┃
	// ┃  /share to create a shareable link                 12.6K/6% ($0.05)  ┃
	if len(newLines) >= 2 && agentType == msgfmt.AgentTypeOpencode {
		dynamicHeaderEnd = 2
	}

	for _, line := range oldLines {
		oldLinesMap[line] = true
	}
	firstNonMatchingLine := len(newLines)
	for i, line := range newLines[dynamicHeaderEnd+1:] {
		if !oldLinesMap[line] {
			firstNonMatchingLine = i
			break
		}
	}
	newSectionLines := newLines[firstNonMatchingLine:]

	// remove leading and trailing lines which are empty or have only whitespace
	startLine := 0
	endLine := len(newSectionLines) - 1
	for i := range newSectionLines {
		if strings.TrimSpace(newSectionLines[i]) != "" {
			startLine = i
			break
		}
	}
	for i := len(newSectionLines) - 1; i >= 0; i-- {
		if strings.TrimSpace(newSectionLines[i]) != "" {
			endLine = i
			break
		}
	}
	return strings.Join(newSectionLines[startLine:endLine+1], "\n")
}

// trimPreviousMessageOverlap removes leading lines of newMsg that exactly
// duplicate the trailing lines of prevMsg.
//
// screenDiff detects new content as everything below the first line of the
// screen that wasn't present in the baseline snapshot. TUI agents sometimes
// re-render already-finalized transcript lines (e.g. Codex redraws previous
// cells when starting a new turn), which makes the first mismatching line
// land above the previous turn's output and the previous message's tail
// leaks into the head of the new message. An agent legitimately starting a
// new response with an exact line-by-line copy of its previous message's
// trailing lines is practically impossible with terminal wrapping, so the
// overlap is treated as a diff artifact and removed.
//
// Lines are compared with trailing whitespace removed (terminal snapshots
// pad lines to the screen width). Overlaps consisting only of whitespace
// lines are ignored.
func trimPreviousMessageOverlap(prevMsg, newMsg string) string {
	if prevMsg == "" || newMsg == "" {
		return newMsg
	}
	prevLines := strings.Split(prevMsg, "\n")
	newLines := strings.Split(newMsg, "\n")
	norm := func(s string) string {
		return strings.TrimRight(s, " \t")
	}

	overlap := 0
	maxOverlap := min(len(prevLines), len(newLines))
	for n := maxOverlap; n > 0; n-- {
		match := true
		hasContent := false
		for i := 0; i < n; i++ {
			line := norm(prevLines[len(prevLines)-n+i])
			if line != norm(newLines[i]) {
				match = false
				break
			}
			if line != "" {
				hasContent = true
			}
		}
		if match && hasContent {
			overlap = n
			break
		}
	}
	if overlap == 0 {
		return newMsg
	}

	remaining := newLines[overlap:]
	// Drop whitespace-only lines left at the top after the trim.
	for len(remaining) > 0 && strings.TrimSpace(remaining[0]) == "" {
		remaining = remaining[1:]
	}
	return strings.Join(remaining, "\n")
}
