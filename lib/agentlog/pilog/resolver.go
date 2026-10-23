package pilog

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

	"github.com/coder/agentapi/lib/agenthome"
	"github.com/coder/agentapi/lib/agentlog"
)

// Resolver finds the JSONL session file of a Pi coding agent process.
//
// Pi stores sessions at <agent dir>/sessions/--<cwd>--/<timestamp>_<id>.jsonl,
// where the agent dir is $PI_CODING_AGENT_DIR or ~/.pi/agent, unless
// $PI_CODING_AGENT_SESSION_DIR or --session-dir points somewhere else. Pi
// writes the file only once the first message is sent, so it doesn't exist
// until then.
//
// AgentAPI starts Pi with --session-id and registers the ID for the
// process (RegisterSession), so the resolver finds the file by name. The
// ID can't be read back from the process: Pi sets its process title, which
// overwrites its command line. Without an ID (the user picked the session
// with --continue and the like) it falls back to the first session for the
// directory that started after the process did.
type Resolver struct {
	PID       int
	CWD       string
	NotBefore time.Time
	// SessionsDir overrides the directory that holds the session files
	// (for tests).
	SessionsDir string
}

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

// Session is how a Pi process was told to store its session.
type Session struct {
	// ID is the --session-id the process was started with, if any.
	ID string
	// Dir is the --session-dir the process was started with, if any.
	Dir string
}

var (
	piSessionsMu sync.Mutex
	piSessions   = map[int]Session{}
)

// RegisterSession records the session options of the Pi process pid, for
// Resolver.
func RegisterSession(pid int, session Session) {
	piSessionsMu.Lock()
	defer piSessionsMu.Unlock()
	piSessions[pid] = session
}

func registeredSession(pid int) Session {
	piSessionsMu.Lock()
	defer piSessionsMu.Unlock()
	return piSessions[pid]
}

func (r *Resolver) sessionsDir(session Session) (string, error) {
	if r.SessionsDir != "" {
		return r.SessionsDir, nil
	}
	// Pi names the directory after the real path of its working directory.
	return SessionsDir(agentlog.RealPath(r.CWD), session.Dir)
}

// SessionsDir is the directory that holds the session files of a Pi
// process in cwd that was started with --session-dir dir (empty without).
func SessionsDir(cwd, dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); dir != "" {
		return dir, nil
	}
	agentDir, err := agenthome.PiAgentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(agentDir, "sessions", SessionDirName(cwd)), nil
}

// Resolve returns the session file named after the process's --session-id,
// or else the first session for CWD that started after NotBefore.
func (r *Resolver) Resolve() (string, error) {
	session := registeredSession(r.PID)
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
	cwd := agentlog.NewDir(r.CWD)
	for _, file := range files {
		header, err := ReadSessionHeader(file.path)
		if err != nil || header.Type != "session" {
			continue
		}
		if r.CWD != "" && header.CWD != "" && !cwd.Is(header.CWD) {
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

// ReadSessionHeader reads the first line of a Pi session file.
func ReadSessionHeader(path string) (*SessionHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var header SessionHeader
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&header); err != nil {
		return nil, err
	}
	return &header, nil
}
