package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/provider"
)

// maxProviders is how many model providers the hub keeps.
const maxProviders = 50

// providerView is a model provider as the hub's page sees it: without its
// key.
type providerView struct {
	provider.Provider
	HasKey bool `json:"has_key" doc:"The provider has an API key, which the hub doesn't show."`
}

// providersBody is GET and PUT /api/providers.
type providersBody struct {
	Providers []providerView `json:"providers" nullable:"false"`
}

// providerChange is a provider of PUT /api/providers.
type providerChange struct {
	provider.Provider
	// APIKey nil keeps the key the provider with the ID has.
	APIKey *string `json:"api_key,omitempty" doc:"The provider's API key; missing keeps the one it has, empty removes it."`
}

// providersChange is the body of PUT /api/providers.
type providersChange struct {
	Providers []providerChange `json:"providers" nullable:"false"`
}

// providerStore keeps the hub's model providers in providers.json beside
// the instances (0600: it holds their keys), or in memory without a state
// path.
type providerStore struct {
	path string
	mu   sync.Mutex
	list []provider.Provider
}

// newProviderStore reads the providers kept beside statePath.
func newProviderStore(statePath string) (*providerStore, error) {
	s := &providerStore{}
	if statePath == "" {
		return s, nil
	}
	s.path = filepath.Join(filepath.Dir(statePath), "providers.json")
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var stored struct {
		Providers []json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	for _, raw := range stored.Providers {
		var p struct {
			provider.Provider
			APIKey string `json:"api_key"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("read %s: %w", s.path, err)
		}
		p.Provider.APIKey = p.APIKey
		s.list = append(s.list, p.Provider)
	}
	return s, nil
}

func (s *providerStore) get() []provider.Provider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// replace checks change and keeps it: a provider without a key in it
// keeps the key the one with its ID had.
func (s *providerStore) replace(change []providerChange) ([]provider.Provider, error) {
	if len(change) > maxProviders {
		return nil, fmt.Errorf("the hub keeps at most %d model providers", maxProviders)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]provider.Provider, 0, len(change))
	for _, c := range change {
		p := c.Provider
		switch {
		case c.APIKey != nil:
			p.APIKey = *c.APIKey
		default:
			if i := slices.IndexFunc(s.list, func(o provider.Provider) bool { return o.ID == p.ID }); i >= 0 {
				p.APIKey = s.list[i].APIKey
			}
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if slices.ContainsFunc(list, func(o provider.Provider) bool { return o.ID == p.ID }) {
			return nil, fmt.Errorf("%w: %s is listed twice", provider.ErrInvalid, p.ID)
		}
		list = append(list, p)
	}
	if err := s.save(list); err != nil {
		return nil, err
	}
	s.list = list
	return slices.Clone(list), nil
}

// save writes list, keys included.
func (s *providerStore) save(list []provider.Provider) error {
	if s.path == "" {
		return nil
	}
	type stored struct {
		provider.Provider
		APIKey string `json:"api_key,omitempty"`
	}
	out := struct {
		Providers []stored `json:"providers"`
	}{Providers: []stored{}}
	for _, p := range list {
		out.Providers = append(out.Providers, stored{Provider: p, APIKey: p.APIKey})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, ".providers.json-*", data)
}

func viewProviders(list []provider.Provider) providersBody {
	body := providersBody{Providers: []providerView{}}
	for _, p := range list {
		redacted, hasKey := p.Redacted()
		body.Providers = append(body.Providers, providerView{Provider: redacted, HasKey: hasKey})
	}
	return body
}

// getProviders answers the model providers, without their keys (GET
// /api/providers).
func (h *Hub) getProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, viewProviders(h.providers.get()))
}

// setProviders replaces the model providers (PUT /api/providers), which
// the AgentAPIs that report to the hub get within a minute. A list the hub
// refuses is 400, and changes nothing.
func (h *Hub) setProviders(w http.ResponseWriter, r *http.Request) {
	var body providersChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || body.Providers == nil {
		writeError(w, http.StatusBadRequest, `send {"providers": [{"id": ..., "base_url": ..., "api_key": ...}, ...]}`)
		return
	}
	list, err := h.providers.replace(body.Providers)
	switch {
	case errors.Is(err, provider.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		h.logger.Error("Failed to keep the model providers", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to keep the model providers")
		return
	}
	h.logger.Info("The model providers changed", "providers", len(list))
	writeJSON(w, http.StatusOK, viewProviders(list))
}

// syncProviders answers an AgentAPI the model providers, keys included,
// sealed with the hub's secret (POST /api/providers/sync). The request is
// signed with the secret, as webhook events are: every machine that joined
// the hub gets the keys.
func (h *Hub) syncProviders(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unreadable request")
		return
	}
	if err := instanceapi.Verify(h.config.Secret, r.Header, body, h.clock.Now()); err != nil {
		if ok, suppressed := h.badSignatures.allow(h.clock.Now(), badSignatureLogEvery); ok {
			h.logger.Warn("Refused a request for the model providers with an invalid or missing signature, or more than 10 minutes off",
				"reason", err, "from", r.RemoteAddr, "refused_since_last_logged", suppressed)
		}
		writeError(w, http.StatusUnauthorized, "invalid signature: "+err.Error())
		return
	}
	sealed, err := provider.Seal(h.config.Secret, h.providers.get())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal the model providers")
		return
	}
	writeJSON(w, http.StatusOK, provider.SyncResponse{Sealed: sealed})
}
