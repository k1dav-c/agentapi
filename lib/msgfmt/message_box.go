package msgfmt

import (
	"strings"
)

// containsHorizontalBorder reports whether the line contains a
// horizontal border made of box-drawing characters (─ or ╌).
func containsHorizontalBorder(line string) bool {
	return strings.Contains(line, "───────────────") ||
		strings.Contains(line, "╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌")
}

// Usually something like
// ───────────────  (or ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌)
// >
// ───────────────  (or ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌)
// Used by Claude Code, Goose, and Aider.
func findGreaterThanMessageBox(lines []string) int {
	for i := len(lines) - 1; i >= max(len(lines)-6, 0); i-- {
		if strings.Contains(lines[i], ">") {
			if i > 0 && containsHorizontalBorder(lines[i-1]) {
				return i - 1
			}
			return i
		}
	}
	return -1
}

// Usually something like
// ───────────────  (or ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌)
// |
// ───────────────  (or ╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌)
func findGenericSlimMessageBox(lines []string) int {
	for i := len(lines) - 3; i >= max(len(lines)-9, 0); i-- {
		if containsHorizontalBorder(lines[i]) &&
			(strings.Contains(lines[i+1], "|") || strings.Contains(lines[i+1], "│") || strings.Contains(lines[i+1], "❯")) &&
			containsHorizontalBorder(lines[i+2]) {
			return i
		}
	}
	return -1
}

func removeMessageBox(msg string) string {
	lines := strings.Split(msg, "\n")

	messageBoxStartIdx := findGreaterThanMessageBox(lines)
	if messageBoxStartIdx == -1 {
		messageBoxStartIdx = findGenericSlimMessageBox(lines)
	}

	if messageBoxStartIdx != -1 {
		lines = lines[:messageBoxStartIdx]
	}

	return strings.Join(lines, "\n")
}

// removeClaudeMessageBox removes the Claude Code input box together with
// everything rendered below it (footer, background agents/tasks panel).
func removeClaudeMessageBox(msg string) string {
	lines := strings.Split(msg, "\n")
	if top, _ := findClaudeInputBox(lines); top != -1 {
		return strings.Join(lines[:top], "\n")
	}
	return removeMessageBox(msg)
}

// removeCodexMessageBox removes the Codex composer and the footer lines
// rendered below it.
func removeCodexMessageBox(msg string) string {
	lines := strings.Split(msg, "\n")
	if idx := findCodexInputLine(lines); idx != -1 {
		lines = lines[:idx]
	}
	return strings.Join(lines, "\n")
}

func removeOpencodeMessageBox(msg string) string {
	lines := strings.Split(msg, "\n")
	//
	//  ┃
	//  ┃
	//  ┃
	//  ┃  Build  Anthropic Claude Sonnet 4
	//  ╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
	//                                tab switch agent  ctrl+p commands
	//
	for i := len(lines) - 1; i >= 4; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀") {
			lines = lines[:i-4]
			break
		}
	}
	return strings.Join(lines, "\n")
}

func removeAmpMessageBox(msg string) string {
	lines := strings.Split(msg, "\n")
	msgBoxEndFound := false
	msgBoxStartIdx := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !msgBoxEndFound && strings.HasPrefix(line, "╰") && strings.HasSuffix(line, "╯") {
			msgBoxEndFound = true
		}
		if msgBoxEndFound && strings.HasPrefix(line, "╭") && strings.HasSuffix(line, "╮") {
			msgBoxStartIdx = i
			break
		}
	}
	formattedMsg := strings.Join(lines[:msgBoxStartIdx], "\n")
	if len(formattedMsg) == 0 {
		return "Welcome to Amp"
	}
	return formattedMsg
}
