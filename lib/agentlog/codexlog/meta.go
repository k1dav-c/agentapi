package codexlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/coder/agentapi/lib/agenthome"
	"github.com/coder/agentapi/lib/agentlog"
)

// SessionMeta is the first-line metadata in a Codex session file.
type SessionMeta struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		// ID identifies this thread. SessionID identifies the root thread
		// of the session; sub-agent threads share it with their parent.
		ID             string `json:"id"`
		SessionID      string `json:"session_id"`
		ParentThreadID string `json:"parent_thread_id"`
		CWD            string `json:"cwd"`
		CLIVersion     string `json:"cli_version"`
		// AgentPath and AgentNickname say what a subagent is.
		AgentPath     string `json:"agent_path"`
		AgentNickname string `json:"agent_nickname"`
		// HistoryStart is the first line that is a subagent's own: Codex
		// starts a subagent's log with a copy of its parent's.
		HistoryStart int64 `json:"subagent_history_start_ordinal"`
		// ForkedFromID and ForkedBefore say which thread a fork continues,
		// and the line of that thread's log it continues before.
		ForkedFromID string `json:"forked_from_id"`
		ForkedBefore *int64 `json:"forked_from_ordinal_exclusive"`
	} `json:"payload"`
}

// IsSubAgent reports whether the file belongs to a sub-agent thread rather
// than the session's main thread.
func (m *SessionMeta) IsSubAgent() bool {
	return m.Payload.ParentThreadID != "" ||
		(m.Payload.ID != "" && m.Payload.SessionID != "" && m.Payload.ID != m.Payload.SessionID)
}

// SessionsRoot returns sessionsDir, defaulting to $CODEX_HOME/sessions.
func SessionsRoot(sessionsDir string) (string, error) {
	if sessionsDir != "" {
		return sessionsDir, nil
	}
	home, err := agenthome.CodexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

// metaReaders are the buffers ReadSessionMeta reads with: finding a
// directory's sessions reads the first line of thousands of files.
var metaReaders = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 64*1024) }}

// ReadSessionMeta reads the first line of a Codex session file. The
// session_meta line can be large: it embeds base_instructions, ~18KB.
func ReadSessionMeta(path string) (*SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r, _ := metaReaders.Get().(*bufio.Reader)
	r.Reset(f)
	defer func() {
		r.Reset(nil)
		metaReaders.Put(r)
	}()
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		// Longer than the buffer: read the rest of it, after the part
		// the buffer holds, which the next read overwrites.
		line = slices.Clone(line)
		rest, _ := agentlog.ReadLine(r)
		line = append(line, rest...)
	}
	var meta SessionMeta
	if err := json.Unmarshal(line, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// Metas keeps the first lines of Codex's session files, each read once:
// Codex writes it when it creates the file and never changes it, and
// finding a directory's sessions takes the first line of every file,
// since Codex keeps all directories' in one tree.
type Metas struct {
	mu    sync.Mutex
	metas map[string]*SessionMeta
}

// maxMetas bounds how many first lines Metas keeps; past it, it reads
// those of new files every time.
const maxMetas = 200_000

// Get returns the first line of the session file at path, read from the
// file the first time only. A first line that doesn't decode yet (Codex
// is still writing it) is read again the next time.
func (m *Metas) Get(path string) (*SessionMeta, error) {
	m.mu.Lock()
	meta, ok := m.metas[path]
	m.mu.Unlock()
	if ok {
		return meta, nil
	}
	meta, err := ReadSessionMeta(path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.metas == nil {
		m.metas = make(map[string]*SessionMeta)
	}
	if len(m.metas) < maxMetas {
		m.metas[path] = meta
	}
	m.mu.Unlock()
	return meta, nil
}

// Retain forgets the files keep doesn't want: those no longer there.
func (m *Metas) Retain(keep func(path string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for path := range m.metas {
		if !keep(path) {
			delete(m.metas, path)
		}
	}
}

// Has reports whether the first line of the file at path is kept.
func (m *Metas) Has(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.metas[path]
	return ok
}

// ThreadNames reads Codex's session index ($CODEX_HOME/session_index.jsonl),
// where Codex records the names it gives its threads, a line per name, the
// latest last. It is an agentlog.LogReader: the index only grows.
type ThreadNames struct {
	names map[string]string
	size  int
}

// NewThreadNames returns a ThreadNames.
func NewThreadNames(string) *ThreadNames {
	return &ThreadNames{names: make(map[string]string)}
}

// IndexPath is the path of Codex's session index.
func IndexPath() (string, error) {
	home, err := agenthome.CodexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "session_index.jsonl"), nil
}

// Line reads a line of the index.
func (t *ThreadNames) Line(line []byte) bool {
	var entry struct {
		ID         string `json:"id"`
		ThreadName string `json:"thread_name"`
	}
	if json.Unmarshal(line, &entry) == nil && entry.ID != "" && entry.ThreadName != "" {
		if _, ok := t.names[entry.ID]; !ok {
			t.size += len(entry.ID) + 64
		}
		t.size += len(entry.ThreadName) - len(t.names[entry.ID])
		t.names[entry.ID] = entry.ThreadName
	}
	return true
}

// Size is about how many bytes of memory the names take.
func (t *ThreadNames) Size() int { return t.size }

// Name is the latest name of thread id, if it has one.
func (t *ThreadNames) Name(id string) string { return t.names[id] }
