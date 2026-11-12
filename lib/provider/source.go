package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// modelsTTL is how long the models a provider listed are kept; after it
// they still answer while they are listed again in the background.
const modelsTTL = 10 * time.Minute

// failedTTL is how long a provider whose models couldn't be listed isn't
// asked again: GET /settings doesn't wait for one that is down each time.
const failedTTL = time.Minute

// settingsListTimeout bounds a model list GET /settings waits for.
const settingsListTimeout = 3 * time.Second

// listTimeout bounds a provider's model list.
const listTimeout = 10 * time.Second

// Source is the providers a server offers: those of the config file, read
// again when it changes, then the hub's that the file doesn't define. The
// zero value offers none.
type Source struct {
	path   string
	logger *slog.Logger
	client *http.Client
	// now is the clock of the model lists; tests set it.
	now func() time.Time

	mu sync.Mutex
	// file are the config file's providers as read at fileMod.
	file    []Provider
	fileMod time.Time
	fileErr string
	hub     []Provider
	// hubSettled: hub are the hub's as it answered since the server
	// started, or there is no hub; else they are those it answered last
	// time (RestoreHub), or none yet.
	hubSettled bool
	listed     map[string]listedModels
}

type listedModels struct {
	key    string
	models []Model
	at     time.Time
	// failed: the last list failed; models are those of the one before.
	failed bool
	// refreshing: a list runs in the background.
	refreshing bool
}

// NewSource returns a source of the providers in the config file at path
// (none if path is empty).
func NewSource(path string, logger *slog.Logger) *Source {
	if logger == nil {
		logger = slog.Default()
	}
	return &Source{path: path, logger: logger, client: &http.Client{Timeout: settingsListTimeout}, now: time.Now, listed: map[string]listedModels{}}
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

// SetHub replaces the hub's providers with those it answered (none when
// there is no hub).
func (s *Source) SetHub(providers []Provider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hub = slices.Clone(providers)
	s.hubSettled = true
}

// RestoreHub has the hub's providers be those it answered last time,
// until it answers again.
func (s *Source) RestoreHub(providers []Provider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hubSettled {
		s.hub = slices.Clone(providers)
	}
}

// HubSettled reports whether the hub's providers are those it answered
// since the server started (or there is no hub): a provider missing from
// them was deleted, not just not known yet.
func (s *Source) HubSettled() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hubSettled
}

// HubCachePath is the file that keeps the hub's providers for the next
// start, sealed with the hub's secret: hub-providers.json beside the
// config file; empty without one.
func (s *Source) HubCachePath() string {
	if s == nil || s.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.path), "hub-providers.json")
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
// else those its API lists at <base_url>/models. A list is kept for 10
// minutes, then answers while it is made again in the background; a list
// that failed isn't made again for a minute. A provider whose API lists
// none, or doesn't answer the first time, offers none.
func (s *Source) Models(ctx context.Context, p Provider) []Model {
	if len(p.Models) > 0 || s == nil {
		return p.Models
	}
	// The same ID with another URL or key is another provider.
	key := p.OpenAIBaseURL() + "\x00" + p.APIKey
	s.mu.Lock()
	cached, ok := s.listed[p.ID]
	ok = ok && cached.key == key
	if ok {
		age := s.now().Sub(cached.at)
		switch {
		case cached.failed && age < failedTTL, !cached.failed && age < modelsTTL:
			s.mu.Unlock()
			return cached.models
		case !cached.failed:
			if !cached.refreshing {
				cached.refreshing = true
				s.listed[p.ID] = cached
				go s.list(context.WithoutCancel(ctx), p, key, cached.models)
			}
			s.mu.Unlock()
			return cached.models
		}
	}
	s.mu.Unlock()
	var previous []Model
	if ok {
		previous = cached.models
	}
	return s.list(ctx, p, key, previous)
}

// list lists p's models and keeps them under key, or keeps that it
// failed, with the models listed before (previous). It returns the models
// it keeps.
func (s *Source) list(ctx context.Context, p Provider, key string, previous []Model) []Model {
	models, err := ListModels(ctx, s.client, p)
	entry := listedModels{key: key, models: models}
	if err != nil {
		s.logger.Warn("Failed to list a model provider's models", "provider", p.ID, "error", err)
		entry.models, entry.failed = previous, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.at = s.now()
	if current, ok := s.listed[p.ID]; ok && current.key != key && current.at.After(entry.at) {
		// The provider changed meanwhile.
		return entry.models
	}
	s.listed[p.ID] = entry
	return entry.models
}

func sameOrigin(a, b *url.URL) bool {
	if !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch strings.ToLower(u.Scheme) {
		case "http":
			return "80"
		case "https":
			return "443"
		default:
			return ""
		}
	}
	return port(a) == port(b)
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
	// Work on a copy so the caller's client and redirect hook stay unchanged.
	redirectClient := *client
	checkRedirect := client.CheckRedirect
	redirectClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		switch {
		case checkRedirect != nil:
			if err := checkRedirect(next, via); err != nil {
				return err
			}
		case len(via) >= 10:
			// A custom hook otherwise replaces net/http's default limit.
			return errors.New("stopped after 10 redirects")
		}
		if len(via) > 0 && !sameOrigin(next.URL, via[0].URL) {
			next.Header.Del("Authorization")
			next.Header.Del("x-api-key")
		}
		return nil
	}
	resp, err := redirectClient.Do(req)
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
