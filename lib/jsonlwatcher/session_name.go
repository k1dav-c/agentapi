package jsonlwatcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionNamer follows the name an agent gives its session: Claude Code's
// generated title (or the one set with /rename), Codex's thread name, Pi's
// /name.
type SessionNamer interface {
	// Name reads one session log line and returns the session's name and
	// whether it changed.
	Name(line []byte) (string, bool)
}

// PolledSessionNamer is a SessionNamer whose name can also change outside
// the session log, so it needs checking now and then.
type PolledSessionNamer interface {
	SessionNamer
	Poll() (string, bool)
}

// NewSessionNamer returns the namer for an agent's session log, or nil
// when the agent has none.
func NewSessionNamer(agentType string) SessionNamer {
	switch agentType {
	case "claude":
		return &claudeSessionNamer{}
	case "codex":
		return &codexSessionNamer{}
	case "pi":
		return &piSessionNamer{}
	}
	return nil
}

// claudeSessionNamer: Claude Code logs {"type":"ai-title","aiTitle":...}
// as it titles the session, and {"type":"custom-title","customTitle":...}
// after /rename. A custom title wins over generated ones.
type claudeSessionNamer struct {
	name   string
	custom bool
}

func (n *claudeSessionNamer) Name(line []byte) (string, bool) {
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

// piSessionNamer: Pi logs {"type":"session_info","name":...} for /name.
type piSessionNamer struct {
	name string
}

func (n *piSessionNamer) Name(line []byte) (string, bool) {
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

// codexSessionNamer: Codex names a thread after its first turn and records
// it in $CODEX_HOME/session_index.jsonl ({"id","thread_name","updated_at"}),
// not in the session log, so the namer takes the thread ID from the log's
// session_meta line and reads the index when it changes.
type codexSessionNamer struct {
	mu        sync.Mutex
	id        string
	name      string
	indexPath string // for tests; defaults to $CODEX_HOME/session_index.jsonl
	indexMod  time.Time
}

func (n *codexSessionNamer) Name(line []byte) (string, bool) {
	if bytes.Contains(line, []byte(`"session_meta"`)) {
		var meta codexSessionMeta
		if json.Unmarshal(line, &meta) == nil && meta.Payload.ID != "" {
			n.mu.Lock()
			if n.id != meta.Payload.ID {
				n.id, n.indexMod = meta.Payload.ID, time.Time{}
			}
			n.mu.Unlock()
		}
	}
	return n.Poll()
}

func (n *codexSessionNamer) Poll() (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.id == "" {
		return n.name, false
	}
	path := n.indexPath
	if path == "" {
		root, err := codexHome()
		if err != nil {
			return n.name, false
		}
		path = filepath.Join(root, "session_index.jsonl")
	}
	info, err := os.Stat(path)
	if err != nil || info.ModTime().Equal(n.indexMod) {
		return n.name, false
	}
	n.indexMod = info.ModTime()
	name := codexThreadName(path, n.id)
	if name == "" || name == n.name {
		return n.name, false
	}
	n.name = name
	return name, true
}

func codexHome() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// codexThreadName returns the latest name the index records for thread id.
func codexThreadName(path, id string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	var name string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	needle := []byte(id)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, needle) {
			continue
		}
		var entry struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.ID == id && entry.ThreadName != "" {
			name = entry.ThreadName
		}
	}
	return name
}
