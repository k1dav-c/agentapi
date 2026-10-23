package codexlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/agenthome"
)

// Namer follows the session's name: Codex names a thread after its first
// turn and records it in $CODEX_HOME/session_index.jsonl
// ({"id","thread_name","updated_at"}), not in the session log, so the namer
// takes the thread ID from the log's session_meta line and reads the index
// when it changes.
type Namer struct {
	mu        sync.Mutex
	id        string
	name      string
	indexPath string // for tests; defaults to $CODEX_HOME/session_index.jsonl
	indexMod  time.Time
}

// NewNamer returns a Namer.
func NewNamer() *Namer {
	return &Namer{}
}

// Name reads one session log line and returns the session's name and
// whether it changed.
func (n *Namer) Name(line []byte) (string, bool) {
	if bytes.Contains(line, []byte(`"session_meta"`)) {
		var meta SessionMeta
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

// Poll reads the name from the session index, if the index changed.
func (n *Namer) Poll() (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.id == "" {
		return n.name, false
	}
	path := n.indexPath
	if path == "" {
		root, err := agenthome.CodexHome()
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
	name := threadName(path, n.id)
	if name == "" || name == n.name {
		return n.name, false
	}
	n.name = name
	return name, true
}

// threadName returns the latest name the index records for thread id.
func threadName(path, id string) string {
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
