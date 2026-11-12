package hub_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/provider"
)

type providerView struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	BaseURL string           `json:"base_url"`
	Models  []provider.Model `json:"models"`
	HasKey  bool             `json:"has_key"`
	APIKey  string           `json:"api_key"`
}

func putProviders(t *testing.T, hubURL string, list any) (int, []providerView) {
	t.Helper()
	var body struct {
		Providers []providerView `json:"providers"`
	}
	code := request(t, http.MethodPut, hubURL+"/api/providers", hubToken, map[string]any{"providers": list}, &body)
	return code, body.Providers
}

func getProviders(t *testing.T, hubURL string) []providerView {
	t.Helper()
	var body struct {
		Providers []providerView `json:"providers"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubURL+"/api/providers", hubToken, nil, &body))
	require.NotNil(t, body.Providers)
	return body.Providers
}

// The hub keeps the model providers, shows no key, and hands them with
// their keys to an AgentAPI that signs with its secret.
func TestHubProviders(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubURL := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	assert.Empty(t, getProviders(t, hubURL))

	code, saved := putProviders(t, hubURL, []map[string]any{
		{"id": "kimi", "name": "Kimi", "base_url": "https://gw.example.com/v1", "api_key": "sk-1", "models": []map[string]string{{"id": "Kimi-K3"}}},
		{"id": "glm", "base_url": "https://glm.example.com/v1"},
	})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, saved, 2)
	assert.True(t, saved[0].HasKey)
	assert.Empty(t, saved[0].APIKey, "no key in an answer")
	assert.False(t, saved[1].HasKey)
	assert.Equal(t, saved, getProviders(t, hubURL))

	// The file holds the keys, for its owner only.
	file := filepath.Join(filepath.Dir(statePath), "providers.json")
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A change without a key keeps the one the provider had; an empty
	// one removes it.
	code, saved = putProviders(t, hubURL, []map[string]any{
		{"id": "kimi", "name": "Kimi K3", "base_url": "https://gw.example.com/v1"},
		{"id": "glm", "base_url": "https://glm.example.com/v1", "api_key": ""},
	})
	require.Equal(t, http.StatusOK, code)
	assert.True(t, saved[0].HasKey)
	assert.Equal(t, "Kimi K3", saved[0].Name)

	// A list the hub refuses changes nothing.
	for _, bad := range [][]map[string]any{
		{{"id": "Kimi", "base_url": "https://gw.example.com/v1"}},
		{{"id": "kimi", "base_url": "gw.example.com"}},
		{{"id": "default", "base_url": "https://gw.example.com/v1"}},
		{{"id": "a", "base_url": "https://a.example.com"}, {"id": "a", "base_url": "https://a.example.com"}},
	} {
		code, _ := putProviders(t, hubURL, bad)
		assert.Equal(t, http.StatusBadRequest, code, bad)
	}
	assert.Len(t, getProviders(t, hubURL), 2)

	// An AgentAPI gets them with the keys.
	source := provider.NewSource("", nil)
	secret := hubSecret
	sync := &provider.HubSync{Source: source, Hub: func() (string, string) { return hubURL + "/hooks", secret }}
	require.NoError(t, sync.Sync(context.Background()))
	kimi, ok := source.Lookup("kimi")
	require.True(t, ok)
	assert.Equal(t, "sk-1", kimi.APIKey)
	assert.Equal(t, provider.OriginHub, kimi.Origin)
	glm, ok := source.Lookup("glm")
	require.True(t, ok)
	assert.Empty(t, glm.APIKey)

	// Not without the secret.
	secret = "wrong"
	assert.ErrorContains(t, sync.Sync(context.Background()), "401")
	resp, err := http.Post(hubURL+provider.SyncPath, "application/json", nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// The keys outlive a restart of the hub.
	restarted := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	assert.Equal(t, getProviders(t, hubURL), getProviders(t, restarted))
	secret = hubSecret
	sync.Hub = func() (string, string) { return restarted + "/hooks", secret }
	require.NoError(t, sync.Sync(context.Background()))
	kimi, _ = source.Lookup("kimi")
	assert.Equal(t, "sk-1", kimi.APIKey)

	// The browser's list needs a session.
	var body json.RawMessage
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubURL+"/api/providers", "", nil, &body))
}

// POST /api/providers/check asks a provider for its models from the hub,
// with the key sent or the one the provider with the ID has.
func TestHubProviderCheck(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"Kimi-K3"},{"id":"GLM-5"}]}`))
	}))
	t.Cleanup(gateway.Close)
	hubURL := startHubWith(t, hub.Config{StatePath: filepath.Join(t.TempDir(), "instances.json")}, false).URL
	code, _ := putProviders(t, hubURL, []map[string]any{{"id": "gw", "base_url": gateway.URL + "/v1", "api_key": "sk-1"}})
	require.Equal(t, http.StatusOK, code)

	var result struct {
		Models []provider.Model `json:"models"`
	}
	// The kept key.
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubURL+"/api/providers/check", hubToken, map[string]any{"id": "gw", "base_url": gateway.URL + "/v1"}, &result))
	assert.Equal(t, []provider.Model{{ID: "Kimi-K3"}, {ID: "GLM-5"}}, result.Models)
	// A new provider, with the key typed.
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubURL+"/api/providers/check", hubToken, map[string]any{"base_url": gateway.URL, "api_key": "sk-1"}, &result))
	assert.Len(t, result.Models, 2)
	// A wrong key: the provider's answer.
	req, err := http.NewRequest(http.MethodPost, hubURL+"/api/providers/check", strings.NewReader(`{"base_url": "`+gateway.URL+`", "api_key": "nope"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+hubToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	refused, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(refused), "401")
	assert.Equal(t, http.StatusBadRequest, request(t, http.MethodPost, hubURL+"/api/providers/check", hubToken, map[string]any{"base_url": "gateway"}, nil))
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPost, hubURL+"/api/providers/check", "", map[string]any{"base_url": gateway.URL}, nil))
}
