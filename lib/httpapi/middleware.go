package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// requestLogMiddleware logs HTTP requests with method, path, and response
// status, at requestLogLevel. SSE and static file routes are excluded to
// avoid noise.
func requestLogMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Skip noisy routes: static files, SSE streams, the terminal
			// (a request per keystroke), and docs.
			if path == "/" || strings.HasPrefix(path, "/chat") ||
				path == "/events" || path == "/internal/screen" || strings.HasPrefix(path, "/terminal") ||
				path == "/docs" || path == "/openapi.json" {
				next.ServeHTTP(w, r)
				return
			}

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rw, r)
			logger.Log(r.Context(), requestLogLevel(r.Method, rw.statusCode), "request",
				"method", r.Method,
				"path", path,
				"status", rw.statusCode,
			)
		})
	}
}

// requestLogLevel is the level a request is logged at. A read that worked
// is Debug: a client that polls (GET /status every few seconds) would
// otherwise fill the log, which nothing rotates. Changes and refused
// requests are Info, server errors Warn.
func requestLogLevel(method string, status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelWarn
	case status >= http.StatusBadRequest:
		return slog.LevelInfo
	case method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions:
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// sseMiddleware creates middleware that prevents proxy buffering for SSE endpoints
func sseMiddleware(ctx huma.Context, next func(huma.Context)) {
	// Disable proxy buffering for SSE endpoints
	ctx.SetHeader("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.SetHeader("Pragma", "no-cache")
	ctx.SetHeader("Expires", "0")
	ctx.SetHeader("X-Accel-Buffering", "no") // nginx
	ctx.SetHeader("X-Proxy-Buffering", "no") // generic proxy
	ctx.SetHeader("Connection", "keep-alive")

	next(ctx)
}
