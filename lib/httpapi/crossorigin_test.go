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
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
)

func TestCrossOriginProtection(t *testing.T) {
	t.Parallel()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	refused := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	handler := httpapi.CrossOriginProtection([]string{"*", "http://localhost:3000", "https://*.example.com"}, refused)(ok)
	cases := []struct {
		name    string
		method  string
		site    string
		origin  string
		allowed bool
	}{
		{"a GET from another site", http.MethodGet, "cross-site", "https://evil.example", true},
		{"a preflight", http.MethodOptions, "cross-site", "https://evil.example", true},
		{"no browser", http.MethodPost, "", "", true},
		{"the same origin", http.MethodPost, "same-origin", "http://agent.test", true},
		{"typed by the user", http.MethodPost, "none", "", true},
		{"another site", http.MethodPost, "cross-site", "https://evil.example", false},
		{"another port of the host", http.MethodPost, "same-site", "http://agent.test:9999", false},
		{"a trusted origin", http.MethodPut, "same-site", "http://localhost:3000", true},
		{"an older browser, same host", http.MethodPost, "", "http://agent.test", true},
		{"an older browser, another site", http.MethodDelete, "", "https://evil.example", false},
		{"an older browser, a trusted origin", http.MethodPost, "", "http://localhost:3000", true},
		{"a pattern", http.MethodPost, "cross-site", "https://app.example.com", true},
		{"a pattern, any case", http.MethodPost, "cross-site", "https://APP.Example.com", true},
		{"outside the pattern", http.MethodPost, "cross-site", "https://example.com.evil.test", false},
		{"the pattern's scheme", http.MethodPost, "cross-site", "http://app.example.com", false},
		// "*" lets pages read answers; it trusts no page to send these.
		{"a wildcard isn't trust", http.MethodPost, "cross-site", "*", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, "http://agent.test/message", nil)
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if tc.allowed {
				assert.Equal(t, http.StatusNoContent, rec.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}
}

// Another site's page can't send the agent a message, not even to a server
// that lets every origin read its answers; a page of a listed origin can.
func TestServer_RefusesCrossOriginRequests(t *testing.T) {
	t.Parallel()
	for _, origins := range [][]string{{"*"}, {"http://localhost:3000"}} {
		ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
		s, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
			AgentType:      msgfmt.AgentTypeClaude,
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
