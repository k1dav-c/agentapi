package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAuthMiddleware(t *testing.T) {
	t.Parallel()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("no middleware means no auth", func(t *testing.T) {
		t.Parallel()
		// When APIToken is empty, the middleware is not registered at all.
		// This test verifies the handler works without the middleware.
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		rec := httptest.NewRecorder()
		okHandler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("valid token passes", func(t *testing.T) {
		t.Parallel()
		middleware := newTokenAuth("secret-token").middleware
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		t.Parallel()
		middleware := newTokenAuth("secret-token").middleware
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		t.Parallel()
		middleware := newTokenAuth("secret-token").middleware
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("chat path exempt from auth", func(t *testing.T) {
		t.Parallel()
		middleware := newTokenAuth("secret-token").middleware
		handler := middleware(okHandler)

		for _, path := range []string{"/", "/chat", "/chat/", "/chat/index.html"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, "path %s should be exempt", path)
		}
	})

	t.Run("api paths require auth", func(t *testing.T) {
		t.Parallel()
		middleware := newTokenAuth("secret-token").middleware
		handler := middleware(okHandler)

		for _, path := range []string{"/status", "/messages", "/message", "/events", "/internal/screen", "/uploads/0123456789abcdef/a.png", "/webhook", "/mcp", "/handoff", "/handoff/reply", "/openapi.json", "/docs"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "path %s should require auth", path)
		}
	})
}

func newAuthTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"http://localhost:3000"},
		APIToken:       token,
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Stop(context.Background())
	})
	return ts
}

func doRequest(t *testing.T, method, url string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func withBearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// withCookie adds the session cookie of a server whose token is token,
// holding value.
func withCookie(token, value string) func(*http.Request) {
	name := sessionCookieName(sessionCookieValue(token))
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: value}) }
}

// sessionCookieFrom returns the session cookie the server with token set.
func sessionCookieFrom(t *testing.T, resp *http.Response, token string) *http.Cookie {
	t.Helper()
	name := sessionCookieName(sessionCookieValue(token))
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("response has no %s cookie (Set-Cookie: %q)", name, resp.Header.Values("Set-Cookie"))
	return nil
}

func decodeAuthSession(t *testing.T, resp *http.Response) AuthSessionBody {
	t.Helper()
	var body AuthSessionBody
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func TestSessionCookieValue(t *testing.T) {
	t.Parallel()
	value := sessionCookieValue("secret-token")
	assert.NotEqual(t, "secret-token", value)
	assert.NotContains(t, value, "secret-token")
	assert.Len(t, value, 64, "hex-encoded HMAC-SHA256")
	assert.Equal(t, value, sessionCookieValue("secret-token"), "deterministic")
	assert.NotEqual(t, value, sessionCookieValue("other-token"), "depends on the token")
}

func TestSessionCookieName(t *testing.T) {
	t.Parallel()
	value := sessionCookieValue("secret-token")
	name := sessionCookieName(value)
	assert.Regexp(t, `^agentapi_session_[0-9a-f]{8}$`, name)
	assert.NotContains(t, name, value[:8], "named after a hash of the value, not the value")
	assert.Equal(t, name, sessionCookieName(value), "deterministic")
	// Servers with other tokens on the same host use other cookies, so
	// signing in to one keeps the browser signed in to the others.
	assert.NotEqual(t, name, sessionCookieName(sessionCookieValue("other-token")))
}

func TestSecretEqual(t *testing.T) {
	t.Parallel()
	assert.True(t, secretEqual("abc", "abc"))
	assert.False(t, secretEqual("abc", "abd"))
	assert.False(t, secretEqual("ab", "abc"))
	assert.False(t, secretEqual("", "abc"))
}

func TestTokenAuthMiddleware_Cookie(t *testing.T) {
	t.Parallel()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := newTokenAuth("secret-token").middleware(okHandler)
	serve := func(mutate func(*http.Request)) int {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		mutate(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	assert.Equal(t, http.StatusOK, serve(withCookie("secret-token", sessionCookieValue("secret-token"))), "derived cookie")
	assert.Equal(t, http.StatusUnauthorized, serve(withCookie("secret-token", "secret-token")), "raw token is not a valid cookie")
	assert.Equal(t, http.StatusUnauthorized, serve(withCookie("secret-token", sessionCookieValue("old-token"))), "value of another token")
	assert.Equal(t, http.StatusUnauthorized, serve(withCookie("old-token", sessionCookieValue("secret-token"))), "valid value under another server's name")
	assert.Equal(t, http.StatusUnauthorized, serve(withCookie("secret-token", "")), "empty cookie")
	assert.Equal(t, http.StatusOK, serve(func(r *http.Request) { r.Header.Set("Authorization", "bearer secret-token") }), "scheme is case-insensitive")
	assert.Equal(t, http.StatusUnauthorized, serve(func(r *http.Request) { r.Header.Set("Authorization", "Basic secret-token") }), "wrong scheme")
	assert.Equal(t, http.StatusUnauthorized, serve(func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }), "empty token")
}

func TestTokenAuthMiddleware_AuthSessionExempt(t *testing.T) {
	t.Parallel()
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := newTokenAuth("secret-token").middleware(okHandler)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		req := httptest.NewRequest(method, authSessionPath, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "%s %s reaches its handler", method, authSessionPath)
	}
	// Only the exact path is exempt.
	req := httptest.NewRequest(http.MethodGet, authSessionPath+"/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthSession(t *testing.T) {
	t.Parallel()
	const token = "secret-token"

	t.Run("unauthenticated state is readable", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		resp := doRequest(t, http.MethodGet, ts.URL+authSessionPath, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, AuthSessionBody{AuthRequired: true, Authenticated: false}, decodeAuthSession(t, resp))
		assert.Empty(t, resp.Header.Values("Set-Cookie"))
	})

	t.Run("sign in requires the bearer token", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		resp := doRequest(t, http.MethodPost, ts.URL+authSessionPath, nil)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Empty(t, resp.Cookies())

		resp = doRequest(t, http.MethodPost, ts.URL+authSessionPath, withBearer("wrong-token"))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Empty(t, resp.Cookies())

		// A session cookie can't be used to mint another one.
		resp = doRequest(t, http.MethodPost, ts.URL+authSessionPath, withCookie(token, sessionCookieValue(token)))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("cookie flow", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)

		resp := doRequest(t, http.MethodGet, ts.URL+"/status", nil)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		resp = doRequest(t, http.MethodPost, ts.URL+authSessionPath, withBearer(token))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, AuthSessionBody{AuthRequired: true, Authenticated: true}, decodeAuthSession(t, resp))
		cookie := sessionCookieFrom(t, resp, token)
		assert.NotEqual(t, token, cookie.Value, "the cookie must not hold the raw token")
		assert.NotContains(t, cookie.Value, token)
		assert.Equal(t, sessionCookieValue(token), cookie.Value)
		assert.True(t, cookie.HttpOnly)
		assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
		assert.Equal(t, "/", cookie.Path)
		assert.False(t, cookie.Secure, "plain HTTP")

		for _, path := range []string{"/status", "/messages", "/queue"} {
			resp = doRequest(t, http.MethodGet, ts.URL+path, withCookie(token, cookie.Value))
			assert.Equal(t, http.StatusOK, resp.StatusCode, "%s with the session cookie", path)
		}
		// Images: past auth, then 404 because nothing was uploaded.
		resp = doRequest(t, http.MethodGet, ts.URL+"/uploads/0123456789abcdef/a.png", withCookie(token, cookie.Value))
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp = doRequest(t, http.MethodGet, ts.URL+"/uploads/0123456789abcdef/a.png", nil)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		resp = doRequest(t, http.MethodGet, ts.URL+authSessionPath, withCookie(token, cookie.Value))
		assert.Equal(t, AuthSessionBody{AuthRequired: true, Authenticated: true}, decodeAuthSession(t, resp))

		resp = doRequest(t, http.MethodGet, ts.URL+"/status", withCookie(token, token))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "raw token as cookie")
	})

	t.Run("SSE endpoints accept the cookie", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		for _, path := range []string{"/events", "/internal/screen"} {
			for _, tc := range []struct {
				name   string
				mutate func(*http.Request)
				want   int
			}{
				{"cookie", withCookie(token, sessionCookieValue(token)), http.StatusOK},
				{"none", func(*http.Request) {}, http.StatusUnauthorized},
			} {
				ctx, cancel := context.WithCancel(context.Background())
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
				require.NoError(t, err)
				tc.mutate(req)
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				assert.Equal(t, tc.want, resp.StatusCode, "%s with %s", path, tc.name)
				cancel()
				_ = resp.Body.Close()
			}
		}
	})

	t.Run("secure cookie behind a TLS proxy", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		resp := doRequest(t, http.MethodPost, ts.URL+authSessionPath, func(r *http.Request) {
			withBearer(token)(r)
			r.Header.Set("X-Forwarded-Proto", "https")
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, sessionCookieFrom(t, resp, token).Secure)
	})

	t.Run("logout clears the cookie", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		resp := doRequest(t, http.MethodDelete, ts.URL+authSessionPath, withCookie(token, sessionCookieValue(token)))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, AuthSessionBody{AuthRequired: true, Authenticated: false}, decodeAuthSession(t, resp))
		cookie := sessionCookieFrom(t, resp, token)
		assert.Empty(t, cookie.Value)
		assert.Negative(t, cookie.MaxAge, "Max-Age=0 deletes the cookie")
		assert.Equal(t, "/", cookie.Path)
		assert.True(t, cookie.HttpOnly)
	})

	t.Run("changing the token invalidates cookies", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, "new-token")
		resp := doRequest(t, http.MethodGet, ts.URL+"/status", withCookie(token, sessionCookieValue(token)))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("servers on one host keep their own sessions", func(t *testing.T) {
		t.Parallel()
		// Browsers send every cookie of the host to every port.
		first := newAuthTestServer(t, token)
		second := newAuthTestServer(t, "other-token")
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		client := &http.Client{Jar: jar}
		signIn := func(url, token string) {
			req, err := http.NewRequest(http.MethodPost, url+authSessionPath, nil)
			require.NoError(t, err)
			withBearer(token)(req)
			resp, err := client.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
		status := func(url string) int {
			resp, err := client.Get(url + "/status")
			require.NoError(t, err)
			_ = resp.Body.Close()
			return resp.StatusCode
		}
		signIn(first.URL, token)
		signIn(second.URL, "other-token")
		assert.Equal(t, http.StatusOK, status(first.URL), "still signed in to the first server")
		assert.Equal(t, http.StatusOK, status(second.URL))
	})

	t.Run("401 carries CORS headers and preflight passes", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, token)
		resp := doRequest(t, http.MethodGet, ts.URL+"/status", func(r *http.Request) {
			r.Header.Set("Origin", "http://localhost:3000")
		})
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, "http://localhost:3000", resp.Header.Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))

		resp = doRequest(t, http.MethodOptions, ts.URL+authSessionPath, func(r *http.Request) {
			r.Header.Set("Origin", "http://localhost:3000")
			r.Header.Set("Access-Control-Request-Method", "POST")
			r.Header.Set("Access-Control-Request-Headers", "Authorization")
		})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "http://localhost:3000", resp.Header.Get("Access-Control-Allow-Origin"))

		resp = doRequest(t, http.MethodOptions, ts.URL+"/message", func(r *http.Request) {
			r.Header.Set("Origin", "http://localhost:3000")
			r.Header.Set("Access-Control-Request-Method", "POST")
			r.Header.Set("Access-Control-Request-Headers", "Content-Type")
		})
		assert.Equal(t, http.StatusOK, resp.StatusCode, "preflight needs no credentials")
	})

	t.Run("without a token", func(t *testing.T) {
		t.Parallel()
		ts := newAuthTestServer(t, "")
		resp := doRequest(t, http.MethodGet, ts.URL+authSessionPath, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, AuthSessionBody{AuthRequired: false, Authenticated: true}, decodeAuthSession(t, resp))

		resp = doRequest(t, http.MethodPost, ts.URL+authSessionPath, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Cookies(), "nothing to sign in to")

		resp = doRequest(t, http.MethodGet, ts.URL+"/status", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
