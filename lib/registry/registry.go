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
}

// DefaultDir is the directory entries go to unless told otherwise:
// ~/.agentapi/instances.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".agentapi", "instances"), nil
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
// file is replaced at once, so a reader never sees half of it.
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
	tmp, err := os.CreateTemp(dir, ".entry-*")
	if err != nil {
		return "", fmt.Errorf("write the registry entry: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write the registry entry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write the registry entry: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("write the registry entry: %w", err)
	}
	return path, nil
}

// List returns the entries in dir, the earliest started first. An entry
// whose process is gone (an AgentAPI that crashed) is removed and left out;
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
		if entry.Hostname == hostname && entry.PID > 0 && !alive(entry.PID) {
			_ = os.Remove(path)
			continue
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b Entry) int { return a.StartedAt.Compare(b.StartedAt) })
	return entries, nil
}
