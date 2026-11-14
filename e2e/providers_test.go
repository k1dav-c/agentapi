//go:build unix

package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settingsBody is what the test reads of GET and PUT /settings.
type settingsBody struct {
	Settings struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
	} `json:"settings"`
	Options struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	} `json:"options"`
}

func (s *nativeServer) settings(ctx context.Context, method, body string) settingsBody {
	s.t.Helper()
	code, data := s.do(ctx, method, "/settings", body)
	require.Equal(s.t, http.StatusOK, code, "%s /settings: %s", method, data)
	var got settingsBody
	require.NoError(s.t, json.Unmarshal(data, &got))
	return got
}

// Claude Code switches to a provider of the config file while it runs, and
// back to its own, in the same conversation. A provider that doesn't
// answer its model list holds GET /settings up for a few seconds once, and
// not again.
func TestNativeClaudeProviders(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*testTimeout)
	defer cancel()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer sk-test":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v1/models":
			// AgentAPI lists the models with the key itself.
			_, _ = w.Write([]byte(`{"data":[{"id":"K3"},{"id":"GLM"}]}`))
		case r.URL.Path == "/v1/messages" && r.Header.Get("anthropic-version") != "" && r.Header.Get("x-api-key") == "":
			// Claude Code's call, through AgentAPI's proxy, which adds the key.
			_, _ = w.Write([]byte(`gw ok`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(gateway.Close)
	// A provider that never answers.
	stuck := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-stuck }))
	t.Cleanup(func() {
		close(stuck)
		slow.Close()
	})
	config := fmt.Sprintf(`providers:
  - {id: gw, name: Gateway, base_url: %s/v1, api_key: sk-test}
  - {id: slow, base_url: %s/v1}
`, gateway.URL, slow.URL)

	s := startNativeWith(ctx, t, config)
	s.waitStable(ctx, "")

	started := time.Now()
	got := s.settings(ctx, http.MethodGet, "")
	assert.Less(t, time.Since(started), 5*time.Second, "the provider that doesn't answer is given up on")
	assert.Equal(t, "default", got.Settings.Provider)
	require.Len(t, got.Options.Providers, 2)
	assert.Equal(t, "gw", got.Options.Providers[0].ID)
	require.Len(t, got.Options.Providers[0].Models, 2, "listed by the gateway")
	assert.Equal(t, "GLM", got.Options.Providers[0].Models[1].ID)
	assert.Empty(t, got.Options.Providers[1].Models)
	started = time.Now()
	s.settings(ctx, http.MethodGet, "")
	assert.Less(t, time.Since(started), time.Second, "nor asked again at once")

	s.post(ctx, "/message", `{"content":"one","type":"user"}`)
	s.waitMessages(ctx, []string{"one", "Echo: one"})

	// To the gateway, while it runs.
	got = s.settings(ctx, http.MethodPut, `{"provider":"gw","model":"GLM"}`)
	assert.Equal(t, "gw", got.Settings.Provider)
	assert.Equal(t, "GLM", got.Settings.Model)
	s.post(ctx, "/message", `{"content":"two","type":"user"}`)
	// Claude Code calls the proxy, with the proxy's token, and the gateway
	// gets the call with its key.
	var messages []string
	require.Eventually(t, func() bool { messages = s.messages(ctx); return len(messages) == 4 }, operationTimeout, 50*time.Millisecond)
	via := messages[3]
	assert.Regexp(t, `^Echo: two \[via http://127\.0\.0\.1:\d+/gw/anthropic on GLM: 200 gw ok\]$`, via)
	assert.NotContains(t, via, gateway.URL)

	// A model the gateway doesn't list, or a provider there isn't.
	code, _ := s.do(ctx, http.MethodPut, "/settings", `{"model":"nope"}`)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = s.do(ctx, http.MethodPut, "/settings", `{"provider":"nope"}`)
	assert.Equal(t, http.StatusBadRequest, code)

	// Back to Anthropic: Claude Code starts again on the session.
	session := s.sessionID(ctx)
	got = s.settings(ctx, http.MethodPut, `{"provider":"default"}`)
	assert.Equal(t, "default", got.Settings.Provider)
	s.waitStable(ctx, "")
	assert.Equal(t, session, s.sessionID(ctx), "the same session")
	s.post(ctx, "/message", `{"content":"three","type":"user"}`)
	s.waitMessages(ctx, []string{"one", "Echo: one", "two", via, "three", "Echo: three"})
	assert.NotContains(t, strings.Join(s.messages(ctx), "\n"), "sk-test")
	s.mu.Lock()
	logged := strings.Join(s.stderr, "\n")
	s.mu.Unlock()
	assert.NotContains(t, logged, "sk-test", "the key isn't logged")
}
