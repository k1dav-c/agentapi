package msgfmt

import (
	"regexp"
	"strings"
)

// Detection of the dialogs agents show in their terminal (selection lists,
// confirmations) instead of their input box, which a message must not be
// typed into.

var (
	terminalSelection    = regexp.MustCompile(`(?m)^\s*[❯›>]\s*(\d+)\.\s+`)
	terminalConfirmation = regexp.MustCompile(`(?i)(enter\s+to\s+confirm|enter\s+to\s+submit|press\s+enter|esc\s+to\s+cancel|enter\s+select|would\s+you\s+like\s+to\s+proceed|do\s+you\s+want\s+to\s+proceed)`)
	terminalOption       = regexp.MustCompile(`(?m)^\s*[❯›>]?\s*(\d+)\.\s+`)
)

// terminalQuestionKeywords must appear (case-insensitively) next to a
// numbered list for it to count as a prompt.
var terminalQuestionKeywords = []string{"choose", "select", "option", "continue", "cancel", "allow", "deny", "approve", "permission"}

// Prompt detection runs on every screen change. Go's regexps are slow on
// the case-insensitive alternations above, and agent output almost never
// contains these words, so each regexp is only tried once a plain substring
// check shows it could match.
var terminalConfirmationWords = []string{"enter", "esc", "proceed"}

func containsAny(s string, words []string) bool {
	for _, word := range words {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

// hasConfirmationHint reports whether content (lower is its lowercase form)
// shows a confirmation hint such as "Enter to confirm · Esc to cancel".
func hasConfirmationHint(content, lower string) bool {
	return containsAny(lower, terminalConfirmationWords) && terminalConfirmation.MatchString(content)
}

// TerminalQuestionTailLines bounds how much of the bottom of the screen is
// inspected for an interactive prompt. The PTY screen includes scrollback, so
// scanning all of it would match numbered lists in earlier agent output and
// block the message queue indefinitely.
const TerminalQuestionTailLines = 40

// IsTerminalQuestionScreen reports whether the agent is currently showing an
// interactive terminal prompt (selection list, confirmation) that a queued
// message must not be typed into.
func IsTerminalQuestionScreen(agentType AgentType, screen string) bool {
	// Only the bottom of the screen matters, and the screen can be a
	// thousand lines; this runs on every screen change.
	screen = ScreenTail(screen, max(InputBoxScanLines, TerminalQuestionTailLines))
	visible, ok := HasInputBox(agentType, screen)
	if ok && visible {
		// The agent's own input box is showing, so it's waiting for a
		// message rather than an answer to a dialog.
		return false
	}
	tail := ScreenTail(screen, TerminalQuestionTailLines)
	lower := strings.ToLower(tail)
	if ok && hasConfirmationHint(tail, lower) {
		// The input box is gone and a confirmation hint is showing, e.g.
		// Claude Code's folder trust dialog, whose options aren't numbered.
		return true
	}
	return isTerminalQuestionText(tail, lower)
}

// ScreenTail returns the last n lines of screen, ignoring trailing blank lines.
// It scans back from the end instead of splitting the whole screen.
func ScreenTail(screen string, n int) string {
	screen = strings.TrimRight(screen, " \t\r\n")
	start := len(screen)
	for range n {
		i := strings.LastIndexByte(screen[:start], '\n')
		if i < 0 {
			return screen
		}
		start = i
	}
	return screen[start+1:]
}

func isTerminalQuestionText(content, lower string) bool {
	if hasConfirmationHint(content, lower) && strings.ContainsAny(content, "❯›>") &&
		terminalSelection.MatchString(content) {
		return true
	}
	return containsAny(lower, terminalQuestionKeywords) && len(terminalOption.FindAllString(content, -1)) >= 2
}

// TerminalOptions returns the numbers of the options a terminal dialog in
// content lists ("1", "2", ...), in the order they show.
func TerminalOptions(content string) []string {
	var numbers []string
	for _, match := range terminalOption.FindAllStringSubmatch(content, -1) {
		numbers = append(numbers, match[1])
	}
	return numbers
}
