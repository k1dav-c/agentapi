package claudelog

import (
	"bytes"
	"encoding/json"
)

// Namer follows the session's name: Claude Code logs
// {"type":"ai-title","aiTitle":...} as it titles the session, and
// {"type":"custom-title","customTitle":...} after /rename. A custom title
// wins over generated ones.
type Namer struct {
	name   string
	custom bool
}

// NewNamer returns a Namer.
func NewNamer() *Namer {
	return &Namer{}
}

// Name reads one session log line and returns the session's name and
// whether it changed.
func (n *Namer) Name(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`-title"`)) {
		return n.name, false
	}
	var entry struct {
		Type        string `json:"type"`
		AITitle     string `json:"aiTitle"`
		CustomTitle string `json:"customTitle"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return n.name, false
	}
	var name string
	switch {
	case entry.Type == "custom-title" && entry.CustomTitle != "":
		name, n.custom = entry.CustomTitle, true
	case entry.Type == "ai-title" && entry.AITitle != "" && !n.custom:
		name = entry.AITitle
	default:
		return n.name, false
	}
	if name == n.name {
		return n.name, false
	}
	n.name = name
	return name, true
}
