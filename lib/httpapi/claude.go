package httpapi

import (
	"regexp"
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

// claudeTrailingMentionRe matches a message that ends with an @-mention of
// a file (@"path with spaces" or @path).
var claudeTrailingMentionRe = regexp.MustCompile(`(?:^|\s)@(?:"[^"]*"|\S+)$`)

func FormatMessage(agentType mf.AgentType, message string) []st.MessagePart {
	message = mf.TrimWhitespace(message)
	// Claude Code opens its file suggestions for an @-mention at the end of
	// the input, and the Enter that should submit the message picks a
	// suggestion instead: a message ending in an attachment (@"<upload
	// path>") stayed in the input box. A space after the mention closes the
	// suggestions. It is typed as a hidden part: the message itself stays
	// trimmed (Send rejects surrounding whitespace).
	if agentType == mf.AgentTypeClaude && claudeTrailingMentionRe.MatchString(message) {
		return []st.MessagePart{
			st.MessagePartText{Content: "\x1b[200~", Hidden: true},
			st.MessagePartText{Content: message},
			st.MessagePartText{Content: " ", Hidden: true},
			st.MessagePartText{Content: "\x1b[201~", Hidden: true},
		}
	}
	if agentType == mf.AgentTypeCodex {
		return append([]st.MessagePart{st.MessagePartText{Content: clearCodexComposer, Hidden: true}}, formatPaste(message)...)
	}
	// for now Claude Code formatting seems to also work for Goose and Aider
	// so we can use the same function for all three
	return formatClaudeCodeMessage(message)
}
