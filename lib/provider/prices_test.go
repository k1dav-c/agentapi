package provider_test

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/provider"
)

func TestSourcePrices(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, `
providers:
  - id: gateway
    base_url: https://gw.example.com/v1
    prices:
      Kimi-K3-NVFP4: {input: 0.6, output: 2.5, cache_read: 0.15}
  - id: bad
    base_url: https://gw.example.com/v1
    prices:
      Kimi-K3: {input: -1, output: 2}
`)
	source := provider.NewSource(path, nil)
	list := source.List()
	require.Len(t, list, 1, "a provider with a negative price is left out")
	want := provider.ModelPrice{Input: 0.6, Output: 2.5, CacheRead: 0.15, Source: provider.SourceManual}
	assert.Equal(t, map[string]provider.ModelPrice{"Kimi-K3-NVFP4": want}, list[0].Prices)

	// The hub's come with the prices it resolved.
	hubPrice := provider.ModelPrice{Input: 0.19, Output: 16, Source: "openrouter:moonshotai/kimi-k3"}
	source.SetHub([]provider.Provider{{ID: "hub", BaseURL: "https://hub.example.com/v1", Prices: map[string]provider.ModelPrice{"Kimi-K3": hubPrice}}})
	table := source.Prices()
	got, ok := table.Lookup("gateway", "Kimi-K3-NVFP4")
	assert.True(t, ok)
	assert.Equal(t, want, got)
	got, ok = table.Lookup("hub", "kimi-k3")
	assert.True(t, ok, "a model ID that differs in case")
	assert.Equal(t, hubPrice, got)
	_, ok = table.Lookup("hub", "Kimi-K2")
	assert.False(t, ok)
	_, ok = table.Lookup("other", "Kimi-K3")
	assert.False(t, ok)
	assert.Equal(t, pricing.Price{Input: 0.19, Output: 16}, hubPrice.Pricing())
}

// The prices travel sealed with the providers; an AgentAPI from before
// them opens the same providers, and a new one opens an old hub's.
func TestSealPricesCompatibility(t *testing.T) {
	t.Parallel()
	price := provider.ModelPrice{Input: 0.19, Output: 16, CacheRead: 0.18, Source: "openrouter:moonshotai/kimi-k3"}
	providers := []provider.Provider{{
		ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1",
		Prices: map[string]provider.ModelPrice{"Kimi-K3-NVFP4": price},
	}}
	sealed, err := provider.Seal("secret", providers)
	require.NoError(t, err)
	opened, err := provider.Open("secret", sealed)
	require.NoError(t, err)
	assert.Equal(t, providers, opened)

	// v5.0.0's wire type: a provider and its key, nothing else.
	type oldWire struct {
		ID      string `json:"id"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key,omitempty"`
	}
	plain := openPlain(t, "secret", sealed)
	var old []oldWire
	require.NoError(t, json.Unmarshal(plain, &old))
	assert.Equal(t, []oldWire{{ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1"}}, old)

	// An old hub's providers have no prices.
	oldSealed, err := provider.Seal("secret", []provider.Provider{{ID: "kimi", BaseURL: "https://gw.example.com/v1"}})
	require.NoError(t, err)
	assert.NotContains(t, string(openPlain(t, "secret", oldSealed)), "prices", "no prices, no field")
	opened, err = provider.Open("secret", oldSealed)
	require.NoError(t, err)
	assert.Nil(t, opened[0].Prices)
}

// openPlain is what Seal sealed, as JSON.
func openPlain(t *testing.T, secret, sealed string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(sealed)
	require.NoError(t, err)
	plain, err := provider.OpenRaw(secret, data)
	require.NoError(t, err)
	return plain
}
