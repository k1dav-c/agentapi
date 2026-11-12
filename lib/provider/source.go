package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/coder/agentapi/lib/userconfig"
)

// ConfigKey is the section of ~/.agentapi/config.yaml with the providers.
const ConfigKey = "providers"

// Origin says where a provider is defined.
type Origin string

const (
	OriginConfig Origin = "config"
	OriginHub    Origin = "hub"
)

// Listed is a provider a Source offers, with where it is defined.
type Listed struct {
	Provider
	Origin Origin
}

// modelsTTL is how long the models a provider listed are kept.
const modelsTTL = 10 * time.Minute

// listTimeout bounds a provider's model list.
const listTimeout = 10 * time.Second

// Source is the providers a server offers: those of the config file, read
// again when it changes, then the hub's that the file doesn't define. The
// zero value offers none.
type Source struct {
	path   string
	logger *slog.Logger
	client *http.Client

	mu sync.Mutex
	// file are the config file's providers as read at fileMod.
	file    []Provider
	fileMod time.Time
	fileErr string
	hub     []Provider
	listed  map[string]listedModels
}

type listedModels struct {
	key    string
	models []Model
	at     time.Time
}

// NewSource returns a source of the providers in the config file at path
// (none if path is empty).
func NewSource(path string, logger *slog.Logger) *Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{path: path, logger: logger, client: &http.Client{Timeout: listTimeout}, listed: map[string]listedModels{}}
}

// LoadFile reads the providers of the config file at path. A provider
// that isn't valid is left out, with a warning.
func LoadFile(path string, logger *slog.Logger) ([]Provider, error) {
	file, err := userconfig.Load(path)
	if err != nil {
		return nil, err
	}
	section, ok := file[ConfigKey]
	if !ok {
		return nil, nil
	}
	data, err := yaml.Marshal(section)
	if err != nil {
		return nil, err
	}
	var all []Provider
	if err := yaml.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("read %s of %s: %w", ConfigKey, path, err)
	}
	return Valid(all, logger), nil
}

// Valid returns the valid providers of all, the first of each ID, and
// logs why it leaves the others out.
func Valid(all []Provider, logger *slog.Logger) []Provider {
	var out []Provider
	for _, p := range all {
		if err := p.Validate(); err != nil {
			logger.Warn("Leaving out a model provider", "error", err)
			continue
		}
		if slices.ContainsFunc(out, func(o Provider) bool { return o.ID == p.ID }) {
			logger.Warn("Leaving out a model provider defined twice", "provider", p.ID)
			continue
		}
		out = append(out, p)
	}
	return out
}

// SetHub replaces the hub's providers.
func (s *Source) SetHub(providers []Provider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hub = slices.Clone(providers)
}

// List returns the providers: the config file's, then the hub's.
func (s *Source) List() []Listed {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	out := make([]Listed, 0, len(s.file)+len(s.hub))
	for _, p := range s.file {
		out = append(out, Listed{Provider: p, Origin: OriginConfig})
	}
	for _, p := range s.hub {
		if !slices.ContainsFunc(s.file, func(f Provider) bool { return f.ID == p.ID }) {
			out = append(out, Listed{Provider: p, Origin: OriginHub})
		}
	}
	return out
}

// Lookup returns the provider with the id.
func (s *Source) Lookup(id string) (Listed, bool) {
	all := s.List()
	i := slices.IndexFunc(all, func(l Listed) bool { return l.ID == id })
	if i < 0 {
		return Listed{}, false
	}
	return all[i], true
}

// reloadLocked reads the config file again if it changed.
func (s *Source) reloadLocked() {
	if s.path == "" {
		return
	}
	info, err := os.Stat(s.path)
	if err != nil {
		s.file, s.fileMod = nil, time.Time{}
		return
	}
	if info.ModTime().Equal(s.fileMod) {
		return
	}
	s.fileMod = info.ModTime()
	file, err := LoadFile(s.path, s.logger)
	if err != nil {
		if msg := err.Error(); msg != s.fileErr {
			s.logger.Warn("Failed to read the model providers", "error", err)
			s.fileErr = msg
		}
		return
	}
	s.file, s.fileErr = file, ""
}

// Models returns the models p offers: those it lists in its definition,
// else those its API lists at <base_url>/models, kept for 10 minutes. A
// provider whose API lists none offers none.
func (s *Source) Models(ctx context.Context, p Provider) []Model {
	if len(p.Models) > 0 || s == nil {
		return p.Models
	}
	// The same ID with another URL or key is another provider.
	key := p.OpenAIBaseURL() + "\x00" + p.APIKey
	s.mu.Lock()
	cached, ok := s.listed[p.ID]
	s.mu.Unlock()
	if ok && cached.key == key && time.Since(cached.at) < modelsTTL {
		return cached.models
	}
	models, err := ListModels(ctx, s.client, p)
	if err != nil {
		s.logger.Warn("Failed to list a model provider's models", "provider", p.ID, "error", err)
		if ok && cached.key == key {
			return cached.models
		}
		return nil
	}
	s.mu.Lock()
	s.listed[p.ID] = listedModels{key: key, models: models, at: time.Now()}
	s.mu.Unlock()
	return models
}

// ListModels asks p's API for its models (GET <base_url>/models, OpenAI's
// format), with client (nil: one that gives up after 10 seconds).
func ListModels(ctx context.Context, client *http.Client, p Provider) ([]Model, error) {
	if client == nil {
		client = &http.Client{Timeout: listTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.OpenAIBaseURL()+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		req.Header.Set("x-api-key", p.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /models answered %s", resp.Status)
	}
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("read the models: %w", err)
	}
	models := []Model{}
	for _, m := range body.Data {
		if m.ID != "" {
			models = append(models, Model{ID: m.ID, Name: m.DisplayName})
		}
	}
	return models, nil
}
