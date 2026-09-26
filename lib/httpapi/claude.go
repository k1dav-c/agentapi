package httpapi

import (
	"strings"

	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
)

// formatPaste wraps message in bracketed paste escape sequences.
// These sequences start with ESC (\x1b), which TUI selection
// widgets (e.g. Claude Code's numbered-choice prompt) interpret
// as "cancel". For selection prompts, callers should use
// MessageTypeRaw to send raw keystrokes directly instead.
func formatPaste(message string) []st.MessagePart {
	return []st.MessagePart{
		// Bracketed paste mode start sequence
		st.MessagePartText{Content: "\x1b[200~", Hidden: true},
		st.MessagePartText{Content: message},
		// Bracketed paste mode end sequence
		st.MessagePartText{Content: "\x1b[201~", Hidden: true},
	}
}

func formatClaudeCodeMessage(message string) []st.MessagePart {
	parts := make([]st.MessagePart, 0)
	parts = append(parts, formatPaste(message)...)

	return parts
}

// codexClearComposerLines bounds how many draft lines clearCodexComposer
// removes. Each extra round is a no-op once the composer is empty.
const codexClearComposerLines = 32

// clearCodexComposer empties the Codex composer: per draft line, move to the
// line end (Ctrl+E), delete to its start (Ctrl+U), and join it with the line
// above (Backspace). Codex puts queued follow-up inputs back into the
// composer when a turn is interrupted, and a pasted message would otherwise
// be appended to that leftover draft.
var clearCodexComposer = strings.Repeat("\x05\x15\x7f", codexClearComposerLines)

func FormatMessage(agentType mf.AgentType, message string) []st.MessagePart {
	message = mf.TrimWhitespace(message)
	if agentType == mf.AgentTypeCodex {
		return append([]st.MessagePart{st.MessagePartText{Content: clearCodexComposer, Hidden: true}}, formatPaste(message)...)
	}
	// for now Claude Code formatting seems to also work for Goose and Aider
	// so we can use the same function for all three
	return formatClaudeCodeMessage(message)
}
