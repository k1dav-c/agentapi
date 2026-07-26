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
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
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

	if meta.SessionID == "" {
		return "", fmt.Errorf("session file %s has empty sessionId", sessionFile)
	}
	if meta.CWD == "" {
		return "", fmt.Errorf("session file %s has empty cwd", sessionFile)
	}

	encodedCWD := encodeCWD(meta.CWD)
	jsonlPath := filepath.Join(home, ".claude", "projects", encodedCWD, meta.SessionID+".jsonl")

	return jsonlPath, nil
}

// encodeCWD encodes a working directory path for use as a Claude projects
// directory name. It replaces path separators with dashes.
// For example, "/home/k1dave6412" becomes "-home-k1dave6412".
func encodeCWD(cwd string) string {
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}
