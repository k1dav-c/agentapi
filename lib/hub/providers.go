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
	"strconv"
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
	Revision  string         `json:"revision" doc:"The hub's opaque optimistic-concurrency token for the provider list."`
	Providers []providerView `json:"providers" nullable:"false"`
}

// providerChange is a provider of PUT /api/providers.
type providerChange struct {
	provider.Provider
	// APIKey nil keeps the key only while the provider's endpoints are unchanged.
	APIKey *string `json:"api_key,omitempty" doc:"The provider's API key; missing keeps it at unchanged endpoints, empty removes it."`
}

// providersChange is the body of PUT /api/providers.
type providersChange struct {
	Revision  string           `json:"revision" doc:"The revision returned by the client's latest provider-list read."`
	Providers []providerChange `json:"providers" nullable:"false"`
}

var errProviderRevision = errors.New("the model providers changed since the client read them")

// providerStore keeps the hub's model providers in providers.json beside
// the instances (0600: it holds their keys), or in memory without a state
// path.
type providerStore struct {
	path     string
	mu       sync.Mutex
	list     []provider.Provider
	revision uint64
}

// newProviderStore reads the providers kept beside statePath.
func newProviderStore(statePath string) (*providerStore, error) {
	s := &providerStore{revision: 1}
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
		Revision  string            `json:"revision"`
		Providers []json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if stored.Revision != "" {
		s.revision, err = strconv.ParseUint(stored.Revision, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("read %s revision: %w", s.path, err)
		}
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

// providerAPIKey resolves an edited provider's key. A missing key is inherited
// only at the same effective OpenAI and Anthropic endpoints; changed reports
// whether an endpoint changed, so a check can stop before making a request.
func providerAPIKey(saved []provider.Provider, p provider.Provider, supplied *string) (key string, changed bool) {
	if supplied != nil {
		return *supplied, false
	}
	if i := slices.IndexFunc(saved, func(o provider.Provider) bool { return o.ID == p.ID }); i >= 0 {
		o := saved[i]
		if o.OpenAIBaseURL() != p.OpenAIBaseURL() || o.ClaudeBaseURL() != p.ClaudeBaseURL() {
			return "", true
		}
		return o.APIKey, false
	}
	return "", false
}

// snapshot returns one coherent list/revision pair.
func (s *providerStore) snapshot() ([]provider.Provider, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list), strconv.FormatUint(s.revision, 10)
}

// replace checks change and keeps it if expected is still current. A missing
// key is inherited only at unchanged effective endpoints; a changed endpoint
// requires an explicit replacement, and an explicitly empty key removes it.
func (s *providerStore) replace(expected string, change []providerChange) (providersBody, error) {
	if len(change) > maxProviders {
		return providersBody{}, fmt.Errorf("the hub keeps at most %d model providers", maxProviders)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != strconv.FormatUint(s.revision, 10) {
		return providersBody{}, errProviderRevision
	}
	list := make([]provider.Provider, 0, len(change))
	for _, c := range change {
		p := c.Provider
		var changed bool
		p.APIKey, changed = providerAPIKey(s.list, p, c.APIKey)
		if changed {
			return providersBody{}, fmt.Errorf("%w: api_key is required when provider %s's URL changes", provider.ErrInvalid, p.ID)
		}
		if err := p.Validate(); err != nil {
			return providersBody{}, err
		}
		if slices.ContainsFunc(list, func(o provider.Provider) bool { return o.ID == p.ID }) {
			return providersBody{}, fmt.Errorf("%w: %s is listed twice", provider.ErrInvalid, p.ID)
		}
		list = append(list, p)
	}
	next := s.revision + 1
	if next == 0 {
		return providersBody{}, errors.New("the model-provider revision space is exhausted")
	}
	if err := s.save(list, next); err != nil {
		return providersBody{}, err
	}
	s.list = list
	s.revision = next
	body := viewProviders(list)
	body.Revision = strconv.FormatUint(next, 10)
	return body, nil
}

// save writes list and its next revision, keys included, in one atomic file.
func (s *providerStore) save(list []provider.Provider, revision uint64) error {
	if s.path == "" {
		return nil
	}
	type stored struct {
		provider.Provider
		APIKey string `json:"api_key,omitempty"`
	}
	out := struct {
		Revision  string   `json:"revision"`
		Providers []stored `json:"providers"`
	}{Revision: strconv.FormatUint(revision, 10), Providers: []stored{}}
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
	list, revision := h.providers.snapshot()
	body := viewProviders(list)
	body.Revision = revision
	writeJSON(w, http.StatusOK, body)
}

// setProviders replaces the model providers (PUT /api/providers) if the
// client's revision is still current, which the AgentAPIs that report to the
// hub get within a minute. A stale list is 409, an invalid one 400; both
// change nothing.
func (h *Hub) setProviders(w http.ResponseWriter, r *http.Request) {
	var body providersChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || body.Providers == nil || body.Revision == "" {
		writeError(w, http.StatusBadRequest, `send {"revision": ..., "providers": [{"id": ..., "base_url": ..., "api_key": ...}, ...]}`)
		return
	}
	kept, err := h.providers.replace(body.Revision, body.Providers)
	switch {
	case errors.Is(err, errProviderRevision):
		writeError(w, http.StatusConflict, err.Error()+"; read the current list and retry")
		return
	case errors.Is(err, provider.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		h.logger.Error("Failed to keep the model providers", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to keep the model providers")
		return
	}
	h.logger.Info("The model providers changed", "providers", len(kept.Providers))
	writeJSON(w, http.StatusOK, kept)
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
	var request provider.SyncRequest
	if err := json.Unmarshal(body, &request); err != nil || request.Nonce == "" {
		writeError(w, http.StatusBadRequest, `send {"nonce": ...}`)
		return
	}
	sealed, err := provider.Seal(h.config.Secret, h.providers.get())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal the model providers")
		return
	}
	proof := instanceapi.Sign(h.config.Secret, request.Nonce, []byte(sealed))
	writeJSON(w, http.StatusOK, provider.SyncResponse{Sealed: sealed, Nonce: request.Nonce, Proof: proof})
}

// providerCheck is the body of POST /api/providers/check: a provider as
// the editor has it, whose key, when it sends none, is the one the provider
// with the ID has at unchanged endpoints.
type providerCheck struct {
	provider.Provider
	APIKey *string `json:"api_key,omitempty" doc:"The key to check with; missing uses the saved one at unchanged endpoints."`
}

// providerCheckResult is the answer of POST /api/providers/check.
type providerCheckResult struct {
	Models    []provider.Model `json:"models" nullable:"false" doc:"The models the provider lists at <base_url>/models."`
	LatencyMS int64            `json:"latency_ms" doc:"How long the provider took to answer, in milliseconds."`
}

// checkProvider asks a provider for its models from the hub (POST
// /api/providers/check), so the page can tell whether its URL and key
// work before it saves them. A provider that doesn't answer with its
// models is 502, with the reason.
func (h *Hub) checkProvider(w http.ResponseWriter, r *http.Request) {
	var body providerCheck
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, `send {"base_url": ..., "api_key": ...}`)
		return
	}
	p := body.Provider
	key, changed := providerAPIKey(h.providers.get(), p, body.APIKey)
	if changed {
		writeError(w, http.StatusBadRequest, "api_key is required when a provider URL changes")
		return
	}
	p.APIKey = key
	if p.ID == "" {
		// The URL is what is checked; a new provider may have no ID yet.
		p.ID = "check"
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	started := h.clock.Now()
	models, err := provider.ListModels(r.Context(), nil, p)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, providerCheckResult{Models: models, LatencyMS: h.clock.Since(started).Milliseconds()})
}
