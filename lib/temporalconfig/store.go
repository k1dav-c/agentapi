package temporalconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ Root string }

func NewStore(root string) (Store, error) {
	if root == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	return Store{Root: absolute}, err
}

func (s Store) Path() string { return filepath.Join(s.Root, ".agentapi", "temporal.json") }
func (s Store) ProfilesPath() string {
	return filepath.Join(s.Root, ".agentapi", "temporal-profiles.json")
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	config, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse Temporal settings: %w", err)
	}
	return config, nil
}

func (s Store) Read() (Config, error) {
	config, err := Load(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	return config, err
}

func (s Store) Write(config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.AllowedUsers == nil {
		config.AllowedUsers = []string{}
	}
	return writeJSON(s.Path(), Document{Config: config})
}

func (s Store) ReadProfiles() (map[string]Config, error) {
	data, err := os.ReadFile(s.ProfilesPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var document ProfilesDocument
	if err := decodeStrict(data, &document); err != nil {
		return nil, fmt.Errorf("parse Temporal profiles: %w", err)
	}
	if document.Profiles == nil {
		return nil, fmt.Errorf("profiles must be an object")
	}
	if err := validateProfiles(document.Profiles); err != nil {
		return nil, err
	}
	return document.Profiles, nil
}

func validateProfiles(profiles map[string]Config) error {
	for name, config := range profiles {
		if !ValidProfileName(name) {
			return fmt.Errorf("invalid Temporal profile name")
		}
		if err := config.Validate(); err != nil {
			return fmt.Errorf("invalid Temporal profile %q: %w", name, err)
		}
	}
	return nil
}

func (s Store) WriteProfiles(profiles map[string]Config) error {
	if err := validateProfiles(profiles); err != nil {
		return err
	}
	return writeJSON(s.ProfilesPath(), ProfilesDocument{Profiles: profiles})
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".temporal-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
