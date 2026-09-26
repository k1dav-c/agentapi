package msgfmt

import (
	"regexp"
	"strings"
)

const (
	// inputBoxSearchLines bounds how far above the last non-empty line the
	// input box may sit. Claude Code renders a footer plus a background
	// agents/tasks panel below its input box, which can push the box well
	// above the bottom of the screen.
	inputBoxSearchLines = 40
	// maxInputBoxHeight bounds the number of lines between the top and
	// bottom border of the Claude Code input box (multi-line drafts).
	maxInputBoxHeight = 12
	// codexInputSearchLines bounds how far above the last non-empty line
	// the Codex composer may sit (footer, warnings, agent hints).
	codexInputSearchLines = 10
)

// numberedOptionRe matches selection-list entries such as "❯ 1. Yes" or
// "› 2. Reset usage". These share the prompt glyph with input boxes but
// belong to dialogs, so they must never be mistaken for an input box.
var numberedOptionRe = regexp.MustCompile(`^[❯›>]\s*\d+\.`)

// codexDialogHintRe matches the key hints Codex prints under its dialogs
// (approvals, plan confirmation, question tool). A "›" line above such a
// hint is a dialog row, e.g. the question tool's "› Add notes" field, not
// the composer.
var codexDialogHintRe = regexp.MustCompile(`(?i)enter\s+to\s+(submit|confirm)|press\s+enter|enter\s+select|esc\s+to\s+cancel|tab\s+or\s+esc\s+to\s+clear`)

// lastNonEmptyLine returns the index of the last line that contains
// non-whitespace characters, or -1 if there is none.
func lastNonEmptyLine(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

// findClaudeInputBox locates the Claude Code input box:
//
//	────────────────
//	❯ draft text
//	────────────────
//	  footer / background agents panel ...
//
// It returns the indices of the top and bottom borders, or -1, -1 when no
// input box is visible (e.g. a permission dialog replaced it). The
// bottom-most match wins.
func findClaudeInputBox(lines []string) (int, int) {
	last := lastNonEmptyLine(lines)
	for bottom := last; bottom >= max(last-inputBoxSearchLines, 1); bottom-- {
		if !containsHorizontalBorder(lines[bottom]) {
			continue
		}
		for top := bottom - 2; top >= max(bottom-1-maxInputBoxHeight, 0); top-- {
			if !containsHorizontalBorder(lines[top]) {
				continue
			}
			prompt := strings.TrimSpace(lines[top+1])
			if (strings.HasPrefix(prompt, "❯") || strings.HasPrefix(prompt, ">")) &&
				!numberedOptionRe.MatchString(prompt) {
				return top, bottom
			}
			// The nearest border above isn't the top of an input box.
			break
		}
	}
	return -1, -1
}

// findCodexInputLine locates the Codex composer line ("› Ask Codex to do
// anything" or a draft) and returns its index, or -1 if none is visible.
// Depending on the Codex version the composer is followed by one or more
// footer lines (model, shortcuts, warnings), so its offset from the bottom
// is not fixed.
func findCodexInputLine(lines []string) int {
	last := lastNonEmptyLine(lines)
	for i := last; i >= max(last-codexInputSearchLines, 0); i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "›") && !numberedOptionRe.MatchString(trimmed) {
			for _, below := range lines[i+1 : last+1] {
				if codexDialogHintRe.MatchString(below) {
					return -1
				}
			}
			return i
		}
	}
	return -1
}

// codexComposerEnd returns the index one past the last composer line that
// starts at inputIdx. Drafts can span several lines; the composer ends at the
// first blank line.
func codexComposerEnd(lines []string, inputIdx int) int {
	end := inputIdx + 1
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	return end
}

// HasInputBox reports whether the agent's input box is visible on the screen,
// meaning the agent is waiting for user input rather than showing a dialog.
// ok is false for agent types without input box detection.
func HasInputBox(agentType AgentType, screen string) (visible bool, ok bool) {
	if agentType == AgentTypeClaude {
		top, _ := findClaudeInputBox(strings.Split(screen, "\n"))
		return top != -1, true
	}
	if agentType == AgentTypeCodex {
		return findCodexInputLine(strings.Split(screen, "\n")) != -1, true
	}
	return false, false
}

// StabilityRegion returns the part of the screen that should be used to
// decide whether the agent is still working. Content below the input box
// (status footer, background agents/tasks panel with ticking timers) keeps
// changing while the agent is idle and ready for input, so it is excluded.
// The screen is returned unchanged when no input box is found.
func StabilityRegion(agentType AgentType, screen string) string {
	if agentType == AgentTypeClaude {
		lines := strings.Split(screen, "\n")
		if _, bottom := findClaudeInputBox(lines); bottom != -1 {
			return strings.Join(lines[:bottom+1], "\n")
		}
	}
	if agentType == AgentTypeCodex {
		lines := strings.Split(screen, "\n")
		if idx := findCodexInputLine(lines); idx != -1 {
			return strings.Join(lines[:codexComposerEnd(lines, idx)], "\n")
		}
	}
	return screen
}
