// Package userconfig reads and writes the user's AgentAPI settings file,
// ~/.agentapi/config.yaml: the flags of agentapi server by name, which apply
// to every server the user starts unless a flag or an AGENTAPI_* variable
// sets them. A setting there outlives a restart that runs the same command
// line, such as a Coder workspace's start script.
package userconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultPath is ~/.agentapi/config.yaml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory: %w", err)
	}
	return filepath.Join(home, ".agentapi", "config.yaml"), nil
}

// Config is the settings, by flag name.
type Config map[string]any

// Load reads the settings in path; a missing file has none.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	config := Config{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if config == nil {
		config = Config{}
	}
	return config, nil
}

// Save writes config to path, readable by its owner only (it may hold a
// webhook secret), creating the directory. The file is replaced at once.
// Comments in the file are not kept.
func Save(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(map[string]any(config))
	if err != nil {
		return err
	}
	header := []byte("# AgentAPI settings: agentapi server flags by name. A flag or an AGENTAPI_*\n# variable overrides them. agentapi hub join and leave write this file.\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(header, data...)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
