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
		SessionID string `json:"session_id"`
		CWD       string `json:"cwd"`
	} `json:"payload"`
}

// Resolve finds the Codex JSONL file by scanning the sessions directory.
//
// It looks in ~/.codex/sessions/ for today's date directory, then scans
// all JSONL files sorted by modification time (newest first), reading each
// file's first line to match the working directory.
func (r *CodexResolver) Resolve() (string, error) {
	sessionsDir := r.SessionsDir
	if sessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		sessionsDir = filepath.Join(home, ".codex", "sessions")
	}

	// Look in today's directory and yesterday's (in case of timezone edge)
	now := time.Now().UTC()
	candidates := []string{
		filepath.Join(sessionsDir, now.Format("2006"), now.Format("01"), now.Format("02")),
		filepath.Join(sessionsDir, now.AddDate(0, 0, -1).Format("2006"), now.AddDate(0, 0, -1).Format("01"), now.AddDate(0, 0, -1).Format("02")),
	}

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
