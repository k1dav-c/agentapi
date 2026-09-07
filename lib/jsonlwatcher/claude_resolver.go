package jsonlwatcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeSessionMeta represents the session metadata from ~/.claude/sessions/<pid>.json.
type ClaudeSessionMeta struct {
	PID         int    `json:"pid"`
	SessionID   string `json:"sessionId"`
	CWD         string `json:"cwd"`
	ParkedJobID string `json:"parkedJobId,omitempty"`
	JobID       string `json:"jobId,omitempty"`
}

// ClaudeResolver finds the JSONL session file for a Claude Code process.
type ClaudeResolver struct {
	PID int
}

// Resolve finds the JSONL file path for the configured Claude process PID.
//
// It reads ~/.claude/sessions/<pid>.json to get the sessionId and cwd,
// then constructs the path to the JSONL file at
// ~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl.
//
// Claude Code v2.1.261+ uses session parking: the initial interactive
// process may park its work to a background process. When this happens,
// the initial session's JSONL file may not exist (or be stale), while
// the active JSONL is under the background process's sessionId. This
// resolver follows the parkedJobId to find the correct session.
func (r *ClaudeResolver) Resolve() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	sessionFile := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", r.PID))
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return "", fmt.Errorf("failed to read session file %s: %w", sessionFile, err)
	}

	var meta ClaudeSessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("failed to parse session file %s: %w", sessionFile, err)
	}

	if meta.CWD == "" {
		return "", fmt.Errorf("session file %s has empty cwd", sessionFile)
	}

	encodedCWD := encodeCWD(meta.CWD)

	// If the session has a parkedJobId, scan other session files first to find
	// the background process that has the matching jobId — its sessionId
	// points to the active JSONL file. The original JSONL can continue to
	// exist after parking, so checking it first would permanently select a
	// stale session.
	if meta.ParkedJobID != "" {
		sessionsDir := filepath.Join(home, ".claude", "sessions")
		entries, err := os.ReadDir(sessionsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				candidateData, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
				if err != nil {
					continue
				}
				var candidate ClaudeSessionMeta
				if err := json.Unmarshal(candidateData, &candidate); err != nil {
					continue
				}
				if candidate.JobID == meta.ParkedJobID && candidate.SessionID != "" {
					jsonlPath := filepath.Join(home, ".claude", "projects", encodedCWD, candidate.SessionID+".jsonl")
					if _, err := os.Stat(jsonlPath); err == nil {
						return jsonlPath, nil
					}
				}
			}
		}
	}

	// Use the direct session when it has not been parked (or while the parked
	// session metadata/file has not appeared yet).
	if meta.SessionID != "" {
		jsonlPath := filepath.Join(home, ".claude", "projects", encodedCWD, meta.SessionID+".jsonl")
		if _, err := os.Stat(jsonlPath); err == nil {
			return jsonlPath, nil
		}
	}

	// Fallback: return the original path (may not exist yet).
	if meta.SessionID == "" {
		return "", fmt.Errorf("session file %s has empty sessionId", sessionFile)
	}
	return filepath.Join(home, ".claude", "projects", encodedCWD, meta.SessionID+".jsonl"), nil
}

// encodeCWD encodes a working directory path for use as a Claude projects
// directory name. It replaces path separators with dashes.
// For example, "/home/k1dave6412" becomes "-home-k1dave6412".
func encodeCWD(cwd string) string {
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}
