package pilog

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

// Namer follows the session's name: Pi logs
// {"type":"session_info","name":...} for /name.
type Namer struct {
	name string
}

// NewNamer returns a Namer.
func NewNamer() *Namer {
	return &Namer{}
}

// Name reads one session log line and returns the session's name and
// whether it changed.
func (n *Namer) Name(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`"session_info"`)) {
		return n.name, false
	}
	var entry struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "session_info" || entry.Name == n.name {
		return n.name, false
	}
	n.name = entry.Name
	return n.name, true
}

// HistoryHeader reads the session ID and start from the first line of a
// log, and reports false for a log that isn't a session of cwd.
func HistoryHeader(line []byte, cwd string) (string, time.Time, bool) {
	var header SessionHeader
	if json.Unmarshal(line, &header) != nil || header.Type != "session" || header.ID == "" {
		return "", time.Time{}, false
	}
	if header.CWD != "" && filepath.Clean(header.CWD) != filepath.Clean(cwd) {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, header.Timestamp)
	return header.ID, started, true
}

// Prompt returns the user's message a line holds, if it holds one.
func Prompt(line []byte) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry piEntry
	if json.Unmarshal(line, &entry) != nil || entry.Type != "message" || entry.Message == nil || entry.Message.Role != "user" {
		return "", time.Time{}, false
	}
	text := strings.TrimSpace(Text(entry.Message.Content))
	if text == "" {
		return "", time.Time{}, false
	}
	return text, Timestamp(entry.Timestamp, entry.Message.Timestamp), true
}
