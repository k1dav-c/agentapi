package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/httpmw"
)

func TestCrossOriginProtection(t *testing.T) {
	t.Parallel()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	refused := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	handler := httpmw.CrossOriginProtection([]string{"*", "http://localhost:3000", "https://*.example.com"}, refused)(ok)
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
