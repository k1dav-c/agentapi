package claudelog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/agentapi/lib/agenthome"
)

// SessionMeta represents the session metadata from ~/.claude/sessions/<pid>.json.
type SessionMeta struct {
	PID         int    `json:"pid"`
	SessionID   string `json:"sessionId"`
	CWD         string `json:"cwd"`
	ParkedJobID string `json:"parkedJobId,omitempty"`
	JobID       string `json:"jobId,omitempty"`
}

// Resolver finds the JSONL session file for a Claude Code process.
type Resolver struct {
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
func (r *Resolver) Resolve() (string, error) {
	configDir, err := agenthome.ClaudeConfigDir()
	if err != nil {
		return "", err
	}
	projectsDir := filepath.Join(configDir, "projects")

	sessionFile := filepath.Join(configDir, "sessions", fmt.Sprintf("%d.json", r.PID))
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		return "", fmt.Errorf("failed to read session file %s: %w", sessionFile, err)
	}

	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("failed to parse session file %s: %w", sessionFile, err)
	}

	if meta.CWD == "" {
		return "", fmt.Errorf("session file %s has empty cwd", sessionFile)
	}

	encodedCWD := ProjectDirName(meta.CWD)

	// If the session has a parkedJobId, scan other session files first to find
	// the background process that has the matching jobId — its sessionId
	// points to the active JSONL file. The original JSONL can continue to
	// exist after parking, so checking it first would permanently select a
	// stale session.
	if meta.ParkedJobID != "" {
		sessionsDir := filepath.Join(configDir, "sessions")
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
				var candidate SessionMeta
				if err := json.Unmarshal(candidateData, &candidate); err != nil {
					continue
				}
				if candidate.JobID == meta.ParkedJobID && candidate.SessionID != "" {
					jsonlPath := filepath.Join(projectsDir, encodedCWD, candidate.SessionID+".jsonl")
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
		jsonlPath := filepath.Join(projectsDir, encodedCWD, meta.SessionID+".jsonl")
		if _, err := os.Stat(jsonlPath); err == nil {
			return jsonlPath, nil
		}
		// Session ids are unique, so if the project directory name doesn't
		// match our encoding (an older Claude Code named the directories of
		// long paths otherwise), find the file by id instead.
		if matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", meta.SessionID+".jsonl")); len(matches) > 0 {
			return matches[0], nil
		}
	}

	// Fallback: return the original path (may not exist yet).
	if meta.SessionID == "" {
		return "", fmt.Errorf("session file %s has empty sessionId", sessionFile)
	}
	return filepath.Join(projectsDir, encodedCWD, meta.SessionID+".jsonl"), nil
}
