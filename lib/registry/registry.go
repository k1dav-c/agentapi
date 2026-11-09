// Package registry lists the AgentAPIs running on a machine: each writes an
// entry file into a directory when it starts serving and removes it when it
// stops, so a client that watches many finds them without being told.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/instanceapi"
)

// Entry is a running AgentAPI: who it is, as its webhook events and
// summary say, with URL where a client reaches it (--public-url, else the
// address it listens on). It holds no secret: whether the API needs a
// token, not the token.
type Entry struct {
	instanceapi.WebhookInstance
	AuthRequired bool      `json:"auth_required"`
	StartedAt    time.Time `json:"started_at"`
	// RestartsInPlace: the server restarts in place on SIGUSR2 (agentapi
	// restart); earlier releases ignore the signal.
	RestartsInPlace bool `json:"restarts_in_place,omitempty"`
	// ProcessStart is when the process PID started, as the system counts
	// it (ProcessStart): another process that got the PID after a crash
	// started at another time. Empty where the system doesn't tell.
	ProcessStart string `json:"process_start,omitempty"`
}

// ProcessStart is when process pid started, as an opaque value that
// differs for another process that gets the same PID later; empty where it
// can't be told (a platform without it, or a process gone).
func ProcessStart(pid int) string {
	return processStart(pid)
}

// DefaultDir is the directory entries go to unless told otherwise:
// ~/.agentapi/instances.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return DirIn(home), nil
}

// DirIn is the default directory of the user whose home directory is home.
func DirIn(home string) string {
	return filepath.Join(home, ".agentapi", "instances")
}

// fileName is the file of the AgentAPI run serverID.
func fileName(serverID string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, serverID) + ".json"
}

// Write writes entry into dir, creating it, and returns the file's path. The
// file is replaced at once, so a reader never sees half of it, and a crash
// leaves the old entry or the new.
func Write(dir string, entry Entry) (string, error) {
	if entry.ServerID == "" {
		return "", errors.New("an entry needs a server ID")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create the registry directory: %w", err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fileName(entry.ServerID))
	if err := atomicfile.Write(path, ".entry-*", append(data, '\n')); err != nil {
		return "", fmt.Errorf("write the registry entry: %w", err)
	}
	return path, nil
}

// List returns the entries in dir, the earliest started first. An entry
// whose process is gone (an AgentAPI that crashed) is removed and left out,
// also when another process got its PID since (its ProcessStart differs);
// so are files that aren't entries. A missing dir lists none.
func List(dir string) ([]Entry, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hostname, _ := os.Hostname()
	var entries []Entry
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") || strings.HasPrefix(file.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil || entry.ServerID == "" {
			continue
		}
		// A directory shared between machines holds others' entries, whose
		// processes can't be checked from here.
		if entry.Hostname == hostname && entry.PID > 0 && !running(entry) {
			_ = os.Remove(path)
			continue
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return a.StartedAt.Compare(b.StartedAt) })
	return entries, nil
}

// running reports whether entry's process still runs: its PID does, and
// started when the entry says, where both are known.
func running(entry Entry) bool {
	if !alive(entry.PID) {
		return false
	}
	if entry.ProcessStart == "" {
		return true
	}
	start := processStart(entry.PID)
	return start == "" || start == entry.ProcessStart
}
