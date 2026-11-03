package pilog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/agentapi/lib/agenthome"
)

// Pi stores sessions at <agent dir>/sessions/--<cwd>--/<timestamp>_<id>.jsonl,
// where the agent dir is $PI_CODING_AGENT_DIR or ~/.pi/agent, unless
// $PI_CODING_AGENT_SESSION_DIR or --session-dir points somewhere else.

// SessionHeader is the first line of a Pi session file.
type SessionHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	Version   int    `json:"version"`
}

// SessionDirName encodes a working directory the way Pi names its
// per-directory session folder: "/home/me/proj" -> "--home-me-proj--".
func SessionDirName(cwd string) string {
	trimmed := cwd
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		trimmed = trimmed[1:]
	}
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(trimmed) + "--"
}

// SessionsDir is the directory that holds the session files of a Pi
// process in cwd that was started with --session-dir dir (empty without).
// Pi takes it from --session-dir, else $PI_CODING_AGENT_SESSION_DIR, else
// the sessionDir setting (the project's .pi/settings.json over the agent
// dir's settings.json), else it is <agent dir>/sessions/--<cwd>--. Only
// the last holds one directory's sessions alone.
func SessionsDir(cwd, dir string) (string, error) {
	if dir != "" {
		return normalizeDir(dir, cwd), nil
	}
	if dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); dir != "" {
		return normalizeDir(dir, cwd), nil
	}
	agentDir, err := agenthome.PiAgentDir()
	if err != nil {
		return "", err
	}
	if dir := settingsSessionDir(agentDir, cwd); dir != "" {
		return normalizeDir(dir, cwd), nil
	}
	return filepath.Join(agentDir, "sessions", SessionDirName(cwd)), nil
}

// settingsSessionDir is the sessionDir Pi's settings set: the project's
// over the agent dir's.
func settingsSessionDir(agentDir, cwd string) string {
	for _, path := range []string{filepath.Join(cwd, ".pi", "settings.json"), filepath.Join(agentDir, "settings.json")} {
		var settings struct {
			SessionDir string `json:"sessionDir"`
		}
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &settings) == nil && settings.SessionDir != "" {
			return settings.SessionDir
		}
	}
	return ""
}

// normalizeDir resolves a directory as Pi does: "~" is the home
// directory, and a relative path is relative to Pi's working directory.
func normalizeDir(dir, cwd string) string {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return filepath.Clean(dir)
}
