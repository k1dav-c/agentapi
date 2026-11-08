package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
)

// Another site's page can't send the agent a message, not even to a server
// that lets every origin read its answers; a page of a listed origin can.
func TestServer_RefusesCrossOriginRequests(t *testing.T) {
	t.Parallel()
	for _, origins := range [][]string{{"*"}, {"http://localhost:3000"}} {
		ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		s, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
			AgentType:      instanceapi.AgentTypeClaude,
			ChatBasePath:   "/chat",
			AllowedHosts:   []string{"*"},
			AllowedOrigins: origins,
		})
		require.NoError(t, err)
		ts := httptest.NewServer(s.Handler())
		t.Cleanup(ts.Close)

		post := func(site, origin string) *http.Response {
			// No Content-Type, as a page's no-cors fetch of a Blob sends it.
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/message", strings.NewReader(`{"content":"hi","type":"user"}`))
			require.NoError(t, err)
			req.Header.Set("Sec-Fetch-Site", site)
			req.Header.Set("Origin", origin)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			return resp
		}
		assert.Equal(t, http.StatusForbidden, post("cross-site", "https://evil.example").StatusCode, "origins %v", origins)
		listed := post("same-site", "http://localhost:3000").StatusCode
		if origins[0] == "*" {
			assert.Equal(t, http.StatusForbidden, listed, "a wildcard trusts no page")
		} else {
			assert.NotEqual(t, http.StatusForbidden, listed, "a listed origin is trusted")
		}
		assert.NotEqual(t, http.StatusForbidden, post("same-origin", ts.URL).StatusCode)
	}
}
