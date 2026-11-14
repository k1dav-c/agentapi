package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerlog"
	"github.com/coder/agentapi/lib/sessions"
)

// The costs name each provider as the server's providers have it, and a
// call to one is priced at its price of the model.
func TestNative_SessionCostsOfProviders(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`providers:
  - id: gw
    name: LiteLLM gateway
    base_url: https://gw.example.com/v1
    models: [Kimi-K3-NVFP4]
    prices:
      Kimi-K3-NVFP4: {input: 0.19, output: 16, cache_read: 0.18}
`), 0o600))
	ledger := providerlog.Open(filepath.Join(dir, providerlog.FileName), providerlog.Options{})
	require.NoError(t, ledger.Record(providerlog.Entry{At: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), Agent: "claude", Session: "current", Provider: "gw"}))
	n := newSessionServerConfig(t, func(a *nativetest.Adapter) { a.Ref = "current" }, func(config *httpapi.ServerConfig) {
		config.Native.Providers = provider.NewSource(configPath, nil)
		config.Native.ProviderLedger = ledger
	})
	logDir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-app")
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "current.jsonl"), []byte(
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","sessionId":"current","message":{"role":"user","content":"First"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-20T10:00:05.000Z","sessionId":"current","message":{"id":"msg_a","role":"assistant","model":"Kimi-K3-NVFP4","content":[{"type":"text","text":"ok"}],`+
			`"usage":{"input_tokens":1000000,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1000000}}}`+"\n"), 0o600))

	var list struct {
		TotalCost sessions.SessionCost `json:"total_cost"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/sessions", nil, &list))
	require.Len(t, list.TotalCost.Providers, 1)
	assert.Equal(t, sessions.ProviderCost{Provider: "gw", Name: "LiteLLM gateway", USD: 16.19, Calls: 1, Priced: true, Tokens: list.TotalCost.Providers[0].Tokens}, list.TotalCost.Providers[0])
	var cost struct {
		Cost sessions.SessionCost `json:"cost"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/cost", nil, &cost))
	require.Len(t, cost.Cost.Providers, 1)
	assert.Equal(t, "LiteLLM gateway", cost.Cost.Providers[0].Name)
	assert.InDelta(t, 16.19, cost.Cost.USD, 1e-9)
}
