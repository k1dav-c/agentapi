package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/provider"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	ok := provider.Provider{ID: "kimi", BaseURL: "https://gw.example.com/v1"}
	require.NoError(t, ok.Validate())
	for name, p := range map[string]provider.Provider{
		"uppercase id":        {ID: "Kimi", BaseURL: ok.BaseURL},
		"reserved id":         {ID: provider.Default, BaseURL: ok.BaseURL},
		"no URL":              {ID: "kimi"},
		"not http":            {ID: "kimi", BaseURL: "ftp://gw.example.com"},
		"bad anthropic URL":   {ID: "kimi", BaseURL: ok.BaseURL, AnthropicBaseURL: "gw.example.com"},
		"model without an id": {ID: "kimi", BaseURL: ok.BaseURL, Models: []provider.Model{{Name: "Kimi"}}},
	} {
		assert.ErrorIs(t, p.Validate(), provider.ErrInvalid, name)
	}
}

func TestBaseURLs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ base, anthropic, openAI, claude string }{
		{"https://gw.example.com/v1", "", "https://gw.example.com/v1", "https://gw.example.com"},
		{"https://gw.example.com/v1/", "", "https://gw.example.com/v1", "https://gw.example.com"},
		// No version: Codex gets /v1 added.
		{"https://openrouter.ai/api", "", "https://openrouter.ai/api/v1", "https://openrouter.ai/api"},
		{"https://gw.example.com/v1beta", "", "https://gw.example.com/v1beta", "https://gw.example.com/v1beta"},
		{"https://gw.example.com/v1", "https://anthropic.example.com/", "https://gw.example.com/v1", "https://anthropic.example.com"},
	} {
		p := provider.Provider{ID: "p", BaseURL: tc.base, AnthropicBaseURL: tc.anthropic}
		assert.Equal(t, tc.openAI, p.OpenAIBaseURL(), tc.base)
		assert.Equal(t, tc.claude, p.ClaudeBaseURL(), tc.base)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestSourceFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	source := provider.NewSource(path, nil)
	assert.Empty(t, source.List(), "no file")

	writeConfig(t, path, `
port: 3284
providers:
  - id: kimi
    name: Kimi
    base_url: https://gw.example.com/v1
    api_key: sk-1
    models: [Kimi-K3, {id: Kimi-K2, name: Older Kimi}]
  - id: BAD
    base_url: https://gw.example.com/v1
  - id: kimi
    base_url: https://other.example.com/v1
`)
	list := source.List()
	require.Len(t, list, 1, "the invalid one and the second kimi are left out")
	assert.Equal(t, provider.OriginConfig, list[0].Origin)
	assert.Equal(t, "sk-1", list[0].APIKey)
	assert.Equal(t, []provider.Model{{ID: "Kimi-K3"}, {ID: "Kimi-K2", Name: "Older Kimi"}}, list[0].Models)

	// The hub's come after the file's; the file's win on an ID.
	source.SetHub([]provider.Provider{
		{ID: "kimi", BaseURL: "https://hub.example.com/v1"},
		{ID: "glm", BaseURL: "https://hub.example.com/v1"},
	})
	list = source.List()
	require.Len(t, list, 2)
	assert.Equal(t, "https://gw.example.com/v1", list[0].BaseURL)
	assert.Equal(t, "glm", list[1].ID)
	assert.Equal(t, provider.OriginHub, list[1].Origin)

	// A change of the file is read again.
	writeConfig(t, path, "providers: []\n")
	later := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(path, later, later))
	// The hub's kimi shows once the file has none.
	got, ok := source.Lookup("kimi")
	require.True(t, ok)
	assert.Equal(t, provider.OriginHub, got.Origin)
	assert.Equal(t, "https://hub.example.com/v1", got.BaseURL)
}

func TestSourceModels(t *testing.T) {
	t.Parallel()
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-1", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"Kimi-K3"},{"id":"Kimi-K2","display_name":"Kimi K2"}]}`))
	}))
	t.Cleanup(api.Close)
	source := provider.NewSource("", nil)
	p := provider.Provider{ID: "kimi", BaseURL: api.URL + "/v1", APIKey: "sk-1"}
	want := []provider.Model{{ID: "Kimi-K3"}, {ID: "Kimi-K2", Name: "Kimi K2"}}
	assert.Equal(t, want, source.Models(context.Background(), p))
	assert.Equal(t, want, source.Models(context.Background(), p))
	assert.Equal(t, 1, calls, "the list is kept")

	// Listed models are offered without asking.
	p.Models = []provider.Model{{ID: "only"}}
	assert.Equal(t, p.Models, source.Models(context.Background(), p))
	assert.Equal(t, 1, calls)
}

func TestSeal(t *testing.T) {
	t.Parallel()
	providers := []provider.Provider{{ID: "kimi", Name: "Kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1", Models: []provider.Model{{ID: "Kimi-K3"}}}}
	sealed, err := provider.Seal("secret", providers)
	require.NoError(t, err)
	assert.NotContains(t, sealed, "sk-1")
	opened, err := provider.Open("secret", sealed)
	require.NoError(t, err)
	assert.Equal(t, providers, opened)
	_, err = provider.Open("other", sealed)
	assert.Error(t, err)

	// The JSON of a provider never has its key.
	data, err := json.Marshal(providers[0])
	require.NoError(t, err)
	assert.NotContains(t, string(data), "sk-1")
}

func TestHubSync(t *testing.T) {
	t.Parallel()
	const secret = "hub-secret"
	providers := []provider.Provider{{ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1"}}
	status := http.StatusOK
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != provider.SyncPath || status != http.StatusOK {
			w.WriteHeader(max(status, http.StatusNotFound))
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := instanceapi.Verify(secret, r.Header, body, time.Now()); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sealed, _ := provider.Seal(secret, providers)
		_ = json.NewEncoder(w).Encode(provider.SyncResponse{Sealed: sealed})
	}))
	t.Cleanup(hub.Close)

	source := provider.NewSource("", nil)
	url, key := hub.URL+"/hooks", secret
	sync := &provider.HubSync{Source: source, Hub: func() (string, string) { return url, key }}
	require.NoError(t, sync.Sync(context.Background()))
	got, ok := source.Lookup("kimi")
	require.True(t, ok)
	assert.Equal(t, "sk-1", got.APIKey)
	assert.Equal(t, provider.OriginHub, got.Origin)

	// A wrong secret is an error, and the providers stay.
	key = "wrong"
	assert.Error(t, sync.Sync(context.Background()))
	_, ok = source.Lookup("kimi")
	assert.True(t, ok)

	// A hub without the route has none.
	key, status = secret, http.StatusNotFound
	require.NoError(t, sync.Sync(context.Background()))
	assert.Empty(t, source.List())

	// No hub, none.
	status = http.StatusOK
	require.NoError(t, sync.Sync(context.Background()))
	url = ""
	require.NoError(t, sync.Sync(context.Background()))
	assert.Empty(t, source.List())
}

// A provider that doesn't answer isn't asked again for a minute, and a
// list that went stale answers while it is made again in the background.
func TestSourceModelsCache(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	failing := atomic.Bool{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"K3"}]}`))
	}))
	t.Cleanup(api.Close)
	now := time.Now()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	source := provider.NewSource("", nil)
	source.SetNow(func() time.Time { return time.Unix(0, clock.Load()) })
	advance := func(d time.Duration) { clock.Add(int64(d)) }
	p := provider.Provider{ID: "p", BaseURL: api.URL + "/v1"}
	ctx := context.Background()

	failing.Store(true)
	assert.Empty(t, source.Models(ctx, p))
	assert.Empty(t, source.Models(ctx, p))
	assert.EqualValues(t, 1, calls.Load(), "a failed list is kept")
	advance(61 * time.Second)
	failing.Store(false)
	assert.Equal(t, []provider.Model{{ID: "K3"}}, source.Models(ctx, p), "asked again after a minute")
	assert.EqualValues(t, 2, calls.Load())

	// Stale: the old list answers at once, and the new one comes in the
	// background.
	advance(11 * time.Minute)
	failing.Store(true)
	assert.Equal(t, []provider.Model{{ID: "K3"}}, source.Models(ctx, p))
	require.Eventually(t, func() bool { return calls.Load() == 3 }, 5*time.Second, time.Millisecond)
	// That failed: the models it had stay, for a minute without asking.
	require.Eventually(t, func() bool {
		return len(source.Models(ctx, p)) == 1 && calls.Load() == 3
	}, 5*time.Second, time.Millisecond)
}

// The hub's providers are kept for the next start, sealed, and stand in
// until the hub answers; a provider missing from them isn't known to be
// deleted until then.
func TestHubSyncCache(t *testing.T) {
	t.Parallel()
	const secret = "hub-secret"
	providers := []provider.Provider{{ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1"}}
	up := atomic.Bool{}
	up.Store(true)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		sealed, _ := provider.Seal(secret, providers)
		_ = json.NewEncoder(w).Encode(provider.SyncResponse{Sealed: sealed})
	}))
	t.Cleanup(hub.Close)
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	url := hub.URL + "/hooks"
	newSync := func() *provider.HubSync {
		source := provider.NewSource(config, nil)
		return &provider.HubSync{Source: source, Hub: func() (string, string) { return url, secret }, CachePath: source.HubCachePath()}
	}

	first := newSync()
	assert.False(t, first.Source.HubSettled())
	require.NoError(t, first.Sync(context.Background()))
	assert.True(t, first.Source.HubSettled())
	data, err := os.ReadFile(filepath.Join(dir, "hub-providers.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "sk-1", "sealed")

	// The next start, with the hub down: last time's stand in.
	up.Store(false)
	next := newSync()
	next.Restore()
	got, ok := next.Source.Lookup("kimi")
	require.True(t, ok)
	assert.Equal(t, "sk-1", got.APIKey)
	assert.Error(t, next.Sync(context.Background()))
	assert.False(t, next.Source.HubSettled(), "the hub hasn't answered")
	_, ok = next.Source.Lookup("kimi")
	assert.True(t, ok)

	// Another hub's aren't restored.
	url = "http://other.example.com/hooks"
	other := newSync()
	other.Restore()
	assert.Empty(t, other.Source.List())

	// Without a hub, the file goes.
	url = ""
	require.NoError(t, other.Sync(context.Background()))
	assert.True(t, other.Source.HubSettled())
	_, err = os.Stat(filepath.Join(dir, "hub-providers.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
