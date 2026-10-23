package claudelog

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// HistoryHeader reads the session ID and start from the first line of a
// log, and reports false for a subagent's log.
func HistoryHeader(line []byte) (string, time.Time, bool) {
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

// commandPrefixes start the user entries Claude Code logs for a slash
// command the user ran, and the notes it adds when the user interrupts a
// turn ("[Request interrupted by user]"), which are not the user's messages.
var commandPrefixes = []string{"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>", "[Request interrupted by user"}

// Prompt returns the user's message a line holds, if it holds one.
func Prompt(line []byte) (string, time.Time, bool) {
	return UserText(line, false)
}

// UserText returns the user's message a line holds; with sidechain, also
// one of a subagent's log, where the agent wrote the "user" messages.
func UserText(line []byte, sidechain bool) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry struct {
		Type        string `json:"type"`
		Timestamp   string `json:"timestamp"`
		IsMeta      bool   `json:"isMeta"`
		IsSidechain bool   `json:"isSidechain"`
		Message     *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsMeta || (entry.IsSidechain && !sidechain) || entry.Message == nil {
		return "", time.Time{}, false
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) != nil {
		var blocks []ContentBlock
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			return "", time.Time{}, false
		}
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
		text = strings.Join(texts, "\n")
	}
	text = strings.TrimSpace(text)
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
