package mcpconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

type claudeStore struct {
	path string
}

func newClaudeStore(workDir string) (*claudeStore, error) {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, xerrors.Errorf("resolve working dir: %w", err)
		}
	}
	return &claudeStore{path: filepath.Join(workDir, ".mcp.json")}, nil
}

func (s *claudeStore) Path() string { return s.path }

func (s *claudeStore) readFile() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("read %s: %w", s.path, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, xerrors.Errorf("parse %s: %w", s.path, err)
	}
	return document, nil
}

func (s *claudeStore) Read() (Servers, error) {
	document, err := s.readFile()
	if err != nil {
		return nil, err
	}
	servers := Servers{}
	if raw, ok := document["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, xerrors.Errorf("parse mcpServers in %s: %w", s.path, err)
		}
	}
	return servers, nil
}

func (s *claudeStore) Replace(servers Servers) error {
	document, err := s.readFile()
	if err != nil {
		return err
	}
	if servers == nil {
		servers = Servers{}
	}
	raw, err := json.Marshal(servers)
	if err != nil {
		return xerrors.Errorf("encode mcpServers: %w", err)
	}
	document["mcpServers"] = raw
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return xerrors.Errorf("encode %s: %w", s.path, err)
	}
	if err := os.WriteFile(s.path, append(data, '\n'), 0o644); err != nil {
		return xerrors.Errorf("write %s: %w", s.path, err)
	}
	return nil
}
