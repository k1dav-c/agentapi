package claudelog

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
)

// Header reads the session ID and start from the first line of a log,
// and reports false for a subagent's log. The working directory is in the
// lines that follow.
func Header(line []byte) (string, time.Time, bool) {
	var entry struct {
		SessionID   string `json:"sessionId"`
		Timestamp   string `json:"timestamp"`
		IsSidechain bool   `json:"isSidechain"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return entry.SessionID, started, entry.SessionID != ""
}

// commandPrefixes start the user entries Claude Code logs for what the
// user ran rather than wrote: a slash command and its output, a shell
// command run with "!" and its output. Its notes when the user interrupts
// a turn ("[Request interrupted by user]") and when a background task
// ends aren't the user's messages either.
var commandPrefixes = []string{
	"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>",
	"<bash-input>", "<bash-stdout>", "<bash-stderr>",
	"[Request interrupted by user", "<task-notification>",
}

// Prompt returns the user's message a line holds, if it holds one.
func Prompt(line []byte) (string, time.Time, bool) {
	return UserText(line, false)
}

// UserText returns the user's message a line holds: the text of a user
// entry the user wrote, as UserBlocks finds it, rather than tool results
// or what Claude Code adds. With sidechain, it is also one of a subagent's
// log, where the agent wrote the "user" messages.
func UserText(line []byte, sidechain bool) (string, time.Time, bool) {
	// Most user entries are tool results, some megabytes long: tell them
	// apart without decoding them.
	if typ, _ := agentlog.StringField(line, "type"); typ != "user" {
		return "", time.Time{}, false
	}
	message, _ := agentlog.Field(line, "message")
	content, _ := agentlog.Field(message, "content")
	if len(content) == 0 || (content[0] == '[' && !hasText(content)) {
		return "", time.Time{}, false
	}
	var entry struct {
		Timestamp   string `json:"timestamp"`
		IsMeta      bool   `json:"isMeta"`
		IsSidechain bool   `json:"isSidechain"`
		Origin      *struct {
			Kind string `json:"kind"`
		} `json:"origin"`
		Message *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsMeta || (entry.IsSidechain && !sidechain) || entry.Message == nil {
		return "", time.Time{}, false
	}
	if entry.Origin != nil && entry.Origin.Kind == "task-notification" {
		return "", time.Time{}, false
	}
	var texts []string
	for _, b := range UserBlocks(entry.Message.Content) {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" {
		return "", time.Time{}, false
	}
	for _, prefix := range commandPrefixes {
		if strings.HasPrefix(text, prefix) {
			return "", time.Time{}, false
		}
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return text, at, true
}

// hasText reports whether an array of content blocks holds a text block.
func hasText(content []byte) bool {
	found := false
	agentlog.Elements(content, func(block []byte) bool {
		typ, _ := agentlog.StringField(block, "type")
		found = typ == "text"
		return !found
	})
	return found
}
