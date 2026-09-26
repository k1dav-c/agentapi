package jsonlwatcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CodexResolver finds the JSONL session file for a Codex process.
//
// Codex stores sessions at ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl.
// There is no PID mapping file, so we scan for the most recent session
// matching the working directory.
type CodexResolver struct {
	PID         int
	CWD         string
	NotBefore   time.Time
	SessionsDir string
}

// codexSessionMeta is the first-line metadata in a Codex session file.
type codexSessionMeta struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		// ID identifies this thread. SessionID identifies the root thread
		// of the session; sub-agent threads share it with their parent.
		ID             string `json:"id"`
		SessionID      string `json:"session_id"`
		ParentThreadID string `json:"parent_thread_id"`
		CWD            string `json:"cwd"`
		AgentPath      string `json:"agent_path"`
		AgentNickname  string `json:"agent_nickname"`
		// Sub-agent files start with a copy of the parent's history; the
		// sub-agent's own records begin at this line index.
		HistoryStart int `json:"subagent_history_start_ordinal"`
		// Source is a plain string ("cli") for main threads and an object
		// describing the spawn for sub-agents; see subAgentSpawn.
		Source json.RawMessage `json:"source"`
	} `json:"payload"`
}

// codexThreadSpawn describes how a sub-agent thread was spawned.
type codexThreadSpawn struct {
	Depth     int     `json:"depth"`
	AgentRole *string `json:"agent_role"`
}

// subAgentSpawn returns the spawn details of a sub-agent thread, or nil.
func (m *codexSessionMeta) subAgentSpawn() *codexThreadSpawn {
	var source struct {
		Subagent *struct {
			ThreadSpawn *codexThreadSpawn `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if err := json.Unmarshal(m.Payload.Source, &source); err != nil || source.Subagent == nil {
		return nil
	}
	return source.Subagent.ThreadSpawn
}

// isSubAgent reports whether the file belongs to a sub-agent thread rather
// than the session's main thread.
func (m *codexSessionMeta) isSubAgent() bool {
	return m.Payload.ParentThreadID != "" ||
		(m.Payload.ID != "" && m.Payload.SessionID != "" && m.Payload.ID != m.Payload.SessionID)
}

// codexSessionsRoot returns sessionsDir, defaulting to ~/.codex/sessions.
func codexSessionsRoot(sessionsDir string) (string, error) {
	if sessionsDir != "" {
		return sessionsDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// codexSessionDirs returns the date directories that may hold current
// session files: today's and yesterday's (in case of a timezone edge).
func codexSessionDirs(root string, now time.Time) []string {
	now = now.UTC()
	yesterday := now.AddDate(0, 0, -1)
	return []string{
		filepath.Join(root, now.Format("2006"), now.Format("01"), now.Format("02")),
		filepath.Join(root, yesterday.Format("2006"), yesterday.Format("01"), yesterday.Format("02")),
	}
}

// Resolve finds the Codex JSONL file by scanning the sessions directory.
//
// It looks in ~/.codex/sessions/ for today's date directory, then scans
// all JSONL files sorted by modification time (newest first), reading each
// file's first line to match the working directory.
func (r *CodexResolver) Resolve() (string, error) {
	sessionsDir, err := codexSessionsRoot(r.SessionsDir)
	if err != nil {
		return "", err
	}
	candidates := codexSessionDirs(sessionsDir, time.Now())

	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var files []fileInfo
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // directory might not exist
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				path:    filepath.Join(dir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	// Find the newest file that matches our CWD
	for _, fi := range files {
		meta, err := readCodexSessionMeta(fi.path)
		if err != nil {
			continue
		}
		// Sub-agent threads share the cwd and are newer than the main
		// thread; following them would replace the session's messages
		// with a sub-agent's.
		if meta.isSubAgent() {
			continue
		}
		startedAt, err := time.Parse(time.RFC3339Nano, meta.Timestamp)
		if err != nil {
			continue
		}
		if meta.Payload.CWD == r.CWD &&
			(r.NotBefore.IsZero() || !startedAt.Before(r.NotBefore)) {
			return fi.path, nil
		}
	}

	return "", fmt.Errorf("no Codex session file found for cwd %q", r.CWD)
}

// readCodexSessionMeta reads the first line of a Codex session file.
// The session_meta line can be large (it embeds base_instructions, ~18KB),
// so decode a single JSON value from the stream instead of reading a
// fixed-size buffer.
func readCodexSessionMeta(path string) (*codexSessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var meta codexSessionMeta
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
