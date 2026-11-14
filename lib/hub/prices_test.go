package hub_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/provider"
)

type modelPrice struct {
	Model          string               `json:"model"`
	Name           string               `json:"name"`
	Choice         string               `json:"choice"`
	OpenRouter     string               `json:"openrouter"`
	OpenRouterName string               `json:"openrouter_name"`
	Price          *provider.ModelPrice `json:"price"`
}

type pricedProvider struct {
	ID          string            `json:"id"`
	OpenRouter  map[string]string `json:"openrouter"`
	ModelPrices []modelPrice      `json:"model_prices"`
}

type pricedProviders struct {
	Revision  string           `json:"revision"`
	Providers []pricedProvider `json:"providers"`
	Prices    struct {
		Enabled   bool       `json:"enabled"`
		FetchedAt *time.Time `json:"fetched_at"`
		Models    int        `json:"models"`
		Error     string     `json:"error"`
	} `json:"prices"`
}

// fakeOpenRouter serves the fixture as OpenRouter's list, or fails while
// down; it counts the requests.
type fakeOpenRouter struct {
	*httptest.Server
	down     atomic.Bool
	requests atomic.Int32
}

func newFakeOpenRouter(t *testing.T) *fakeOpenRouter {
	t.Helper()
	data, err := os.ReadFile("testdata/openrouter-models.json")
	require.NoError(t, err)
	f := &fakeOpenRouter{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		assert.Equal(t, "/api/v1/models", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"), "the list needs no key")
		if f.down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(f.Close)
	return f
}

func pricesOf(t *testing.T, method, hubURL string) pricedProviders {
	t.Helper()
	var body pricedProviders
	path := "/api/providers"
	if method == http.MethodPost {
		path = "/api/providers/prices"
	}
	require.Equal(t, http.StatusOK, request(t, method, hubURL+path, hubToken, nil, &body))
	return body
}

// summary is each model's price as "model choice openrouter input/output".
func summary(p pricedProvider) []string {
	out := []string{}
	for _, m := range p.ModelPrices {
		line := m.Model + " " + m.Choice + " " + m.OpenRouter
		if m.Price != nil {
			line += " " + formatPrice(*m.Price)
		}
		out = append(out, line)
	}
	return out
}

func formatPrice(p provider.ModelPrice) string {
	return strconv.FormatFloat(p.Input, 'f', -1, 64) + "/" + strconv.FormatFloat(p.Output, 'f', -1, 64) + " " + p.Source
}

// The hub prices its providers' models at OpenRouter's list prices: by
// name, or as picked in providers.json; a provider that lists its models
// lists them at a refresh. It sends the prices with the providers, keeps
// OpenRouter's list for when it can't get it, and searches it.
func TestHubProviderPrices(t *testing.T) {
	openRouter := newFakeOpenRouter(t)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-gw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"Kimi-K3.1-Pro-NVFP4"},{"id":"private-model"}]}`))
	}))
	t.Cleanup(gateway.Close)
	statePath := filepath.Join(t.TempDir(), "instances.json")
	config := hub.Config{StatePath: statePath, OpenRouterURL: openRouter.URL + "/api/v1/models"}
	hubURL := startHubWith(t, config, false).URL

	code, _ := putProviders(t, hubURL, []map[string]any{
		{"id": "kimi", "base_url": "https://gw.example.com/v1", "models": []map[string]string{{"id": "Kimi-K3-NVFP4"}, {"id": "Kimi-K3.1-Pro-NVFP4"}, {"id": "local-model"}}},
		{"id": "gw", "base_url": gateway.URL + "/v1", "api_key": "sk-gw"},
	})
	require.Equal(t, http.StatusOK, code)

	// Before the hub has OpenRouter's list: no prices.
	body := pricesOf(t, http.MethodGet, hubURL)
	assert.True(t, body.Prices.Enabled)
	assert.Nil(t, body.Prices.FetchedAt)
	assert.Equal(t, []string{"Kimi-K3-NVFP4 auto ", "Kimi-K3.1-Pro-NVFP4 auto ", "local-model auto "}, summary(body.Providers[0]))
	assert.Empty(t, summary(body.Providers[1]), "gw lists its models at a refresh")
	assert.Zero(t, openRouter.requests.Load(), "a hub that doesn't run fetches on request only")

	body = pricesOf(t, http.MethodPost, hubURL)
	assert.EqualValues(t, 1, openRouter.requests.Load())
	require.NotNil(t, body.Prices.FetchedAt)
	assert.Equal(t, 9, body.Prices.Models)
	assert.Empty(t, body.Prices.Error)
	assert.Equal(t, []string{
		"Kimi-K3-NVFP4 auto moonshotai/kimi-k3 0.192/16 openrouter:moonshotai/kimi-k3",
		"Kimi-K3.1-Pro-NVFP4 auto moonshotai/kimi-k3.1-pro 2/10 openrouter:moonshotai/kimi-k3.1-pro",
		"local-model auto ",
	}, summary(body.Providers[0]))
	assert.Equal(t, "MoonshotAI: Kimi K3", body.Providers[0].ModelPrices[0].OpenRouterName)
	assert.Equal(t, []string{
		"Kimi-K3.1-Pro-NVFP4 auto moonshotai/kimi-k3.1-pro 2/10 openrouter:moonshotai/kimi-k3.1-pro",
		"private-model auto ",
	}, summary(body.Providers[1]))

	// Picks: another OpenRouter model, and no price.
	code, _, _ = putProvidersAtRevision(t, hubURL, body.Revision, []map[string]any{
		{"id": "kimi", "base_url": "https://gw.example.com/v1", "models": []map[string]string{{"id": "Kimi-K3-NVFP4"}, {"id": "Kimi-K3.1-Pro-NVFP4"}, {"id": "local-model"}},
			"openrouter": map[string]string{"Kimi-K3-NVFP4": "moonshotai/kimi-k3:batch", "Kimi-K3.1-Pro-NVFP4": "none", "local-model": "moonshotai/kimi-k2.6"}},
		{"id": "gw", "base_url": gateway.URL + "/v1", "openrouter": map[string]string{"gone-model": "moonshotai/kimi-k9"}},
	})
	require.Equal(t, http.StatusOK, code)
	body = pricesOf(t, http.MethodGet, hubURL)
	assert.Equal(t, map[string]string{"Kimi-K3-NVFP4": "moonshotai/kimi-k3:batch", "Kimi-K3.1-Pro-NVFP4": "none", "local-model": "moonshotai/kimi-k2.6"}, body.Providers[0].OpenRouter)
	assert.Equal(t, []string{
		"Kimi-K3-NVFP4 chosen moonshotai/kimi-k3:batch 2.28/11.4 openrouter:moonshotai/kimi-k3:batch",
		"Kimi-K3.1-Pro-NVFP4 none ",
		"local-model chosen moonshotai/kimi-k2.6 " + formatPrice(*body.Providers[0].ModelPrices[2].Price),
	}, summary(body.Providers[0]))
	assert.Equal(t, []string{
		"Kimi-K3.1-Pro-NVFP4 auto moonshotai/kimi-k3.1-pro 2/10 openrouter:moonshotai/kimi-k3.1-pro",
		"private-model auto ",
		// A model only a choice names, and an OpenRouter model the list
		// doesn't have: no price.
		"gone-model chosen moonshotai/kimi-k9",
	}, summary(body.Providers[1]))

	// A change without the choices keeps them; an empty object removes
	// them; a bad one changes nothing.
	code, _ = putProviders(t, hubURL, []map[string]any{
		{"id": "kimi", "base_url": "https://gw.example.com/v1", "models": []map[string]string{{"id": "Kimi-K3-NVFP4"}, {"id": "Kimi-K3.1-Pro-NVFP4"}, {"id": "local-model"}}},
		{"id": "gw", "base_url": gateway.URL + "/v1", "openrouter": map[string]string{}},
	})
	require.Equal(t, http.StatusOK, code)
	for _, bad := range []map[string]string{{"m": ""}, {"": "a/b"}, {"m": "a b"}} {
		code, _ := putProviders(t, hubURL, []map[string]any{{"id": "gw", "base_url": gateway.URL + "/v1", "openrouter": bad}})
		assert.Equal(t, http.StatusBadRequest, code, bad)
	}
	body = pricesOf(t, http.MethodGet, hubURL)
	require.Len(t, body.Providers, 2)
	assert.Len(t, body.Providers[0].OpenRouter, 3)
	assert.Nil(t, body.Providers[1].OpenRouter)

	// The AgentAPIs get the prices with the providers.
	source := provider.NewSource("", nil)
	sync := &provider.HubSync{Source: source, Hub: func() (string, string) { return hubURL + "/hooks", hubSecret }}
	require.NoError(t, sync.Sync(context.Background()))
	table := source.Prices()
	assert.Equal(t, provider.ModelPrice{Input: 2.28, Output: 11.4, CacheRead: 0.228, Source: "openrouter:moonshotai/kimi-k3:batch"}, table["kimi"]["Kimi-K3-NVFP4"])
	assert.NotContains(t, table["kimi"], "Kimi-K3.1-Pro-NVFP4", "no price, as picked")
	assert.Equal(t, map[string]provider.ModelPrice{"Kimi-K3.1-Pro-NVFP4": {Input: 2, Output: 10, CacheRead: 0.2, Source: "openrouter:moonshotai/kimi-k3.1-pro"}}, table["gw"])
	gw, _ := source.Lookup("gw")
	assert.Empty(t, gw.Models, "gw still lists its own models")

	// The editor's picker searches the list.
	var found struct {
		Models []struct {
			ID    string              `json:"id"`
			Name  string              `json:"name"`
			Price provider.ModelPrice `json:"price"`
		} `json:"models"`
		FetchedAt *time.Time `json:"fetched_at"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubURL+"/api/providers/openrouter?q=kimi+k3.1", hubToken, nil, &found))
	require.Len(t, found.Models, 2)
	assert.Equal(t, "moonshotai/kimi-k3.1-pro", found.Models[0].ID)
	assert.InDelta(t, 10, found.Models[0].Price.Output, 1e-12)
	assert.NotNil(t, found.FetchedAt)
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubURL+"/api/providers/openrouter", "", nil, nil))

	// OpenRouter down: the hub keeps the list it had, and says why.
	openRouter.down.Store(true)
	body = pricesOf(t, http.MethodPost, hubURL)
	assert.Contains(t, body.Prices.Error, "502")
	assert.Equal(t, 9, body.Prices.Models)
	assert.Len(t, body.Providers[1].ModelPrices, 2, "gw's models listed again")

	// And after a restart, offline.
	openRouter.Close()
	gateway.Close()
	restarted := startHubWith(t, config, false).URL
	again := pricesOf(t, http.MethodGet, restarted)
	assert.Equal(t, summary(body.Providers[0]), summary(again.Providers[0]))
	assert.Equal(t, summary(body.Providers[1]), summary(again.Providers[1]), "with the models gw listed")
	assert.Equal(t, 9, again.Prices.Models)
}

// A running hub gets OpenRouter's list once it has providers and its copy
// is a day old; a hub without OpenRouter's URL looks nothing up.
func TestHubProviderPricesRefresh(t *testing.T) {
	openRouter := newFakeOpenRouter(t)
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubURL := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	code, _ := putProviders(t, hubURL, []map[string]any{{"id": "kimi", "base_url": "https://gw.example.com/v1", "models": []map[string]string{{"id": "Kimi-K3-NVFP4"}}}})
	require.Equal(t, http.StatusOK, code)
	assert.False(t, pricesOf(t, http.MethodGet, hubURL).Prices.Enabled)
	assert.Equal(t, http.StatusConflict, request(t, http.MethodPost, hubURL+"/api/providers/prices", hubToken, nil, nil))

	running := startHubWith(t, hub.Config{StatePath: statePath, OpenRouterURL: openRouter.URL + "/api/v1/models"}, true).URL
	require.Eventually(t, func() bool {
		return pricesOf(t, http.MethodGet, running).Prices.FetchedAt != nil
	}, 5*time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 1, openRouter.requests.Load())
	assert.Equal(t, []string{"Kimi-K3-NVFP4 auto moonshotai/kimi-k3 0.192/16 openrouter:moonshotai/kimi-k3"}, summary(pricesOf(t, http.MethodGet, running).Providers[0]))

	// A copy less than a day old isn't fetched again at the start.
	startHubWith(t, hub.Config{StatePath: statePath, OpenRouterURL: openRouter.URL + "/api/v1/models"}, true)
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, 1, openRouter.requests.Load())
}
