package mcpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// jsonStore manages the "mcpServers" object of a JSON config file, the
// format Claude Code and Pi share. Other keys in the file are kept.
type jsonStore struct {
	path string
}

func newClaudeStore(workDir string) (*jsonStore, error) {
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working dir: %w", err)
		}
	}
	return &jsonStore{path: filepath.Join(workDir, ".mcp.json")}, nil
}

func (s *jsonStore) Path() string { return s.path }

func (s *jsonStore) readFile() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return document, nil
}

func (s *jsonStore) Read() (Servers, error) {
	document, err := s.readFile()
	if err != nil {
		return nil, err
	}
	servers := Servers{}
	if raw, ok := document["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, fmt.Errorf("parse mcpServers in %s: %w", s.path, err)
		}
	}
	return servers, nil
}

func (s *jsonStore) Replace(servers Servers) error {
	document, err := s.readFile()
	if err != nil {
		return err
	}
	if servers == nil {
		servers = Servers{}
	}
	raw, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encode mcpServers: %w", err)
	}
	document["mcpServers"] = raw
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", s.path, err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(s.path), err)
	}
	if err := os.WriteFile(s.path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	return nil
}

// newPiStore manages Pi's user-level MCP config, $PI_CODING_AGENT_DIR/mcp.json
// (default ~/.pi/agent/mcp.json). Pi only reads a project's .pi/mcp.json
// after the project is trusted, so the user-level file is the one that
// always applies.
func newPiStore() (*jsonStore, error) {
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		agentDir = filepath.Join(home, ".pi", "agent")
	}
	return &jsonStore{path: filepath.Join(agentDir, "mcp.json")}, nil
}
