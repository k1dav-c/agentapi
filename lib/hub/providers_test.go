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
	"sync/atomic"
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
	revision, _ := providerSnapshot(t, hubURL)
	code, _, providers := putProvidersAtRevision(t, hubURL, revision, list)
	return code, providers
}

func putProvidersAtRevision(t *testing.T, hubURL, revision string, list any) (int, string, []providerView) {
	t.Helper()
	var body struct {
		Revision  string         `json:"revision"`
		Providers []providerView `json:"providers"`
	}
	code := request(t, http.MethodPut, hubURL+"/api/providers", hubToken, map[string]any{"revision": revision, "providers": list}, &body)
	return code, body.Revision, body.Providers
}

func providerSnapshot(t *testing.T, hubURL string) (string, []providerView) {
	t.Helper()
	var body struct {
		Revision  string         `json:"revision"`
		Providers []providerView `json:"providers"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubURL+"/api/providers", hubToken, nil, &body))
	require.NotEmpty(t, body.Revision)
	require.NotNil(t, body.Providers)
	return body.Revision, body.Providers
}

func getProviders(t *testing.T, hubURL string) []providerView {
	t.Helper()
	_, providers := providerSnapshot(t, hubURL)
	return providers
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

	// The keys and revision outlive a restart of the hub.
	revision, providers := providerSnapshot(t, hubURL)
	restarted := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	restartedRevision, restartedProviders := providerSnapshot(t, restarted)
	assert.Equal(t, revision, restartedRevision)
	assert.Equal(t, providers, restartedProviders)
	secret = hubSecret
	sync.Hub = func() (string, string) { return restarted + "/hooks", secret }
	require.NoError(t, sync.Sync(context.Background()))
	kimi, _ = source.Lookup("kimi")
	assert.Equal(t, "sk-1", kimi.APIKey)

	// The browser's list needs a session.
	var body json.RawMessage
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubURL+"/api/providers", "", nil, &body))
}

// A whole-list replacement is conditional: two clients holding the same
// revision can't both overwrite it, and the conflict survives a restart.
func TestHubProvidersRevisionConflict(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubURL := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	initial, _ := providerSnapshot(t, hubURL)
	code, current, saved := putProvidersAtRevision(t, hubURL, initial, []map[string]any{{
		"id": "kimi", "base_url": "https://kimi.example.com/v1", "api_key": "sk-synthetic",
	}})
	require.Equal(t, http.StatusOK, code)
	assert.NotEqual(t, initial, current)

	code, staleAnswer, _ := putProvidersAtRevision(t, hubURL, initial, []map[string]any{{
		"id": "other", "base_url": "https://other.example.com/v1",
	}})
	assert.Equal(t, http.StatusConflict, code)
	assert.Empty(t, staleAnswer)
	gotRevision, gotProviders := providerSnapshot(t, hubURL)
	assert.Equal(t, current, gotRevision)
	assert.Equal(t, saved, gotProviders, "the stale replacement changed nothing")

	// A missing revision isn't a wildcard for a whole-list replacement.
	var failure map[string]any
	code = request(t, http.MethodPut, hubURL+"/api/providers", hubToken, map[string]any{"providers": []any{}}, &failure)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _, _ = putProvidersAtRevision(t, hubURL, initial, []any{})
	assert.Equal(t, http.StatusConflict, code)

	restarted := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	code, _, _ = putProvidersAtRevision(t, restarted, initial, []any{})
	assert.Equal(t, http.StatusConflict, code, "the old token stays stale across the restart")
	code, next, _ := putProvidersAtRevision(t, restarted, current, []any{})
	require.Equal(t, http.StatusOK, code)
	assert.NotEqual(t, current, next)
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

func TestHubProviderKeyInheritance(t *testing.T) {
	const originalKey = "sk-original"
	var expectedKey atomic.Value
	expectedKey.Store(originalKey)
	var originalCalls atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+expectedKey.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"original-model"}]}`))
	}))
	t.Cleanup(original.Close)
	var changedCalls atomic.Int32
	changed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		changedCalls.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":"changed-model"}]}`))
	}))
	t.Cleanup(changed.Close)

	hubURL := startHubWith(t, hub.Config{StatePath: filepath.Join(t.TempDir(), "instances.json")}, false).URL
	code, saved := putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1", "anthropic_base_url": original.URL + "/", "api_key": originalKey,
	}})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, saved, 1)
	require.True(t, saved[0].HasKey)

	check := func(body map[string]any) int {
		var result struct {
			Models []provider.Model `json:"models"`
		}
		return request(t, http.MethodPost, hubURL+"/api/providers/check", hubToken, body, &result)
	}
	canonical := map[string]any{"id": "gw", "base_url": original.URL, "anthropic_base_url": original.URL + "/"}
	// Name and model edits, an implied /v1, and trailing slashes keep both
	// effective endpoints and therefore the key.
	code, saved = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "name": "Renamed", "base_url": original.URL + "/v1/", "anthropic_base_url": original.URL,
		"models": []map[string]string{{"id": "renamed-model"}},
	}})
	require.Equal(t, http.StatusOK, code)
	assert.True(t, saved[0].HasKey)
	assert.Equal(t, http.StatusOK, check(canonical))
	assert.EqualValues(t, 1, originalCalls.Load())

	for name, body := range map[string]map[string]any{
		"OpenAI endpoint changed": {
			"id": "gw", "base_url": changed.URL + "/v1", "anthropic_base_url": original.URL,
		},
		"Anthropic endpoint changed": {
			"id": "gw", "base_url": original.URL + "/v1/", "anthropic_base_url": changed.URL,
		},
	} {
		t.Run(name, func(t *testing.T) {
			beforeOriginal, beforeChanged := originalCalls.Load(), changedCalls.Load()
			assert.Equal(t, http.StatusBadRequest, check(body))
			assert.Equal(t, beforeOriginal, originalCalls.Load(), "a changed URL must not be checked without an explicit key")
			assert.Equal(t, beforeChanged, changedCalls.Load(), "a changed URL must not receive the saved key")
		})
	}

	// An explicit key can check a changed URL.
	assert.Equal(t, http.StatusOK, check(map[string]any{
		"id": "gw", "base_url": changed.URL + "/v1", "anthropic_base_url": original.URL, "api_key": "sk-explicit",
	}))
	assert.EqualValues(t, 1, changedCalls.Load())

	// Saving a changed OpenAI endpoint without a key is refused rather than
	// silently treating the editor's empty key input as an explicit removal.
	// Neither the old key nor the new endpoint gets a check without one.
	code, _ = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": changed.URL + "/v1", "anthropic_base_url": original.URL,
	}})
	assert.Equal(t, http.StatusBadRequest, code)
	saved = getProviders(t, hubURL)
	assert.True(t, saved[0].HasKey)
	beforeOriginal, beforeChanged := originalCalls.Load(), changedCalls.Load()
	assert.Equal(t, http.StatusBadRequest, check(map[string]any{
		"id": "gw", "base_url": changed.URL + "/v1", "anthropic_base_url": original.URL,
	}))
	assert.Equal(t, beforeOriginal, originalCalls.Load())
	assert.Equal(t, beforeChanged, changedCalls.Load())
	assert.Equal(t, http.StatusOK, check(map[string]any{
		"id": "gw", "base_url": changed.URL + "/v1/", "anthropic_base_url": original.URL, "api_key": "sk-changed",
	}))

	// Reset, then changing only the effective Anthropic endpoint is refused too.
	code, saved = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1", "anthropic_base_url": original.URL, "api_key": originalKey,
	}})
	require.Equal(t, http.StatusOK, code)
	assert.True(t, saved[0].HasKey)
	code, _ = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1/", "anthropic_base_url": changed.URL,
	}})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.True(t, getProviders(t, hubURL)[0].HasKey)

	// An explicit replacement is used; an explicit empty key removes it.
	code, saved = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1", "anthropic_base_url": original.URL, "api_key": originalKey,
	}})
	require.Equal(t, http.StatusOK, code)
	assert.True(t, saved[0].HasKey)
	code, saved = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1/", "anthropic_base_url": original.URL + "/", "api_key": "sk-replacement",
	}})
	require.Equal(t, http.StatusOK, code)
	assert.True(t, saved[0].HasKey)
	expectedKey.Store("sk-replacement")
	assert.Equal(t, http.StatusOK, check(map[string]any{"id": "gw", "base_url": original.URL + "/v1", "anthropic_base_url": original.URL}))
	code, saved = putProviders(t, hubURL, []map[string]any{{
		"id": "gw", "base_url": original.URL + "/v1", "anthropic_base_url": original.URL, "api_key": "",
	}})
	require.Equal(t, http.StatusOK, code)
	assert.False(t, saved[0].HasKey)
}
