package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerproxy"
)

// GET /settings and the summary say how each provider answered the
// agent's calls through the proxy; the server closes the proxy when it
// stops.
func TestNative_ProviderHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	}))
	t.Cleanup(upstream.Close)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("providers:\n  - {id: kimi, name: Kimi, base_url: "+upstream.URL+"/v1, api_key: sk-1, models: [K3]}\n  - {id: glm, base_url: "+upstream.URL+"/v1, models: [GLM]}\n"), 0o600))
	source := provider.NewSource(configPath, nil)
	proxy := providerproxy.New(source, providerproxy.Options{})
	// After the server stopped.
	t.Cleanup(func() {
		_, err := proxy.Endpoint("kimi")
		assert.ErrorIs(t, err, providerproxy.ErrClosed)
	})
	factory := &nativetest.Factory{Providers: true, Configure: configurableAdapter}
	n := newNativeServerConfig(t, nil, func(config *httpapi.ServerConfig) {
		config.Native.Factory = factory.New
		config.Native.Providers = source
		config.Native.ProviderProxy = proxy
	})

	var got httpapi.SettingsResponse
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/settings", nil, &got.Body))
	require.Len(t, got.Body.Options.Providers, 2)
	assert.Nil(t, got.Body.Options.Providers[0].Health, "not called yet")
	var summary instanceapi.SummaryBody
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/summary", nil, &summary))
	assert.Empty(t, summary.ProviderHealth)

	// The agent calls kimi through the proxy.
	endpoint, err := proxy.Endpoint("kimi")
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, endpoint.AnthropicBaseURL+"/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Eventually(t, func() bool { return proxy.Health("kimi") != nil }, 5*time.Second, 5*time.Millisecond)

	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/settings", nil, &got.Body))
	health := got.Body.Options.Providers[0].Health
	require.NotNil(t, health)
	assert.Equal(t, 1, health.Requests)
	assert.Equal(t, 1, health.Failures)
	require.NotNil(t, health.LastFailure)
	assert.Equal(t, agentstate.ProviderFailureStatus, health.LastFailure.Kind)
	assert.Equal(t, http.StatusServiceUnavailable, health.LastFailure.Status)
	assert.Equal(t, "Overloaded", health.LastFailure.Message)
	assert.Nil(t, got.Body.Options.Providers[1].Health)

	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/summary", nil, &summary))
	require.Len(t, summary.ProviderHealth, 1, "the providers called in the hour")
	assert.Equal(t, "kimi", summary.ProviderHealth[0].Provider)
	assert.True(t, summary.ProviderHealth[0].ProviderHealth.Equal(*health))
}
