package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
		middleware := tokenAuthMiddleware("secret-token")
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("missing token returns 401", func(t *testing.T) {
		t.Parallel()
		middleware := tokenAuthMiddleware("secret-token")
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		t.Parallel()
		middleware := tokenAuthMiddleware("secret-token")
		handler := middleware(okHandler)

		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("chat path exempt from auth", func(t *testing.T) {
		t.Parallel()
		middleware := tokenAuthMiddleware("secret-token")
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
		middleware := tokenAuthMiddleware("secret-token")
		handler := middleware(okHandler)

		for _, path := range []string{"/status", "/messages", "/message", "/events", "/usage", "/webhook", "/mcp"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "path %s should require auth", path)
		}
	})
}
