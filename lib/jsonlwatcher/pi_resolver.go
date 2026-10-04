package jsonlwatcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PiResolver finds the JSONL session file of a Pi coding agent process.
//
// Pi stores sessions at <agent dir>/sessions/--<cwd>--/<timestamp>_<id>.jsonl,
// where the agent dir is $PI_CODING_AGENT_DIR or ~/.pi/agent, unless
// $PI_CODING_AGENT_SESSION_DIR or --session-dir points somewhere else. Pi
// writes the file only once the first message is sent, so it doesn't exist
// until then.
//
// AgentAPI starts Pi with --session-id and registers the ID for the
// process (RegisterPiSession), so the resolver finds the file by name. The
// ID can't be read back from the process: Pi sets its process title, which
// overwrites its command line. Without an ID (the user picked the session
// with --continue and the like) it falls back to the first session for the
// directory that started after the process did.
type PiResolver struct {
	PID       int
	CWD       string
	NotBefore time.Time
	// SessionsDir overrides the directory that holds the session files
	// (for tests).
	SessionsDir string
}

// piSessionHeader is the first line of a Pi session file.
type piSessionHeader struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	Version   int    `json:"version"`
}

// piSessionDirName encodes a working directory the way Pi names its
// per-directory session folder: "/home/me/proj" -> "--home-me-proj--".
func piSessionDirName(cwd string) string {
	trimmed := cwd
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		trimmed = trimmed[1:]
	}
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(trimmed) + "--"
}

// PiSession is how a Pi process was told to store its session.
type PiSession struct {
	// ID is the --session-id the process was started with, if any.
	ID string
	// Dir is the --session-dir the process was started with, if any.
	Dir string
}

var (
	piSessionsMu sync.Mutex
	piSessions   = map[int]PiSession{}
)

// RegisterPiSession records the session options of the Pi process pid, for
// PiResolver.
func RegisterPiSession(pid int, session PiSession) {
	piSessionsMu.Lock()
	defer piSessionsMu.Unlock()
	piSessions[pid] = session
}

func registeredPiSession(pid int) PiSession {
	piSessionsMu.Lock()
	defer piSessionsMu.Unlock()
	return piSessions[pid]
}

func (r *PiResolver) sessionsDir(session PiSession) (string, error) {
	if r.SessionsDir != "" {
		return r.SessionsDir, nil
	}
	if session.Dir != "" {
		return session.Dir, nil
	}
	if dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); dir != "" {
		return dir, nil
	}
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		agentDir = filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(agentDir, "sessions", piSessionDirName(r.CWD)), nil
}

// Resolve returns the session file named after the process's --session-id,
// or else the first session for CWD that started after NotBefore.
func (r *PiResolver) Resolve() (string, error) {
	session := registeredPiSession(r.PID)
	dir, err := r.sessionsDir(session)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no Pi session directory %s: %w", dir, err)
	}
	if id := session.ID; id != "" {
		suffix := "_" + id + ".jsonl"
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
				return filepath.Join(dir, entry.Name()), nil
			}
		}
		return "", fmt.Errorf("no Pi session file for session %q yet", id)
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var files []candidate
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{filepath.Join(dir, entry.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })

	// Take the session that started first after this process did: when
	// several Pi processes share a directory, a later one's session is newer
	// but isn't ours. A session last written after NotBefore isn't enough:
	// it can be another process's older session that is still in use.
	var started string
	var startedAt time.Time
	for _, file := range files {
		header, err := readPiSessionHeader(file.path)
		if err != nil || header.Type != "session" {
			continue
		}
		if r.CWD != "" && header.CWD != "" && filepath.Clean(header.CWD) != filepath.Clean(r.CWD) {
			continue
		}
		if r.NotBefore.IsZero() {
			return file.path, nil
		}
		at, err := time.Parse(time.RFC3339Nano, header.Timestamp)
		if err == nil && !at.Before(r.NotBefore) && (started == "" || at.Before(startedAt)) {
			started, startedAt = file.path, at
		}
	}
	if started != "" {
		return started, nil
	}
	return "", fmt.Errorf("no Pi session file found for cwd %q", r.CWD)
}

func readPiSessionHeader(path string) (*piSessionHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var header piSessionHeader
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&header); err != nil {
		return nil, err
	}
	return &header, nil
}
