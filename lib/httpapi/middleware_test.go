package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// levelRecorder is a slog.Handler that keeps the levels of the records
// logged.
type levelRecorder struct {
	mu     sync.Mutex
	levels []slog.Level
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *levelRecorder) WithGroup(string) slog.Handler            { return h }

func (h *levelRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.levels = append(h.levels, r.Level)
	return nil
}

func (h *levelRecorder) take() []slog.Level {
	h.mu.Lock()
	defer h.mu.Unlock()
	levels := h.levels
	h.levels = nil
	return levels
}

// A read that worked logs at Debug, so a client that polls doesn't fill the
// log; changes and refused requests log at Info, server errors at Warn.
// Event streams, the terminal and the chat's files don't log.
func TestRequestLogLevels(t *testing.T) {
	t.Parallel()
	rec := &levelRecorder{}
	handler := requestLogMiddleware(slog.New(rec))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status, err := strconv.Atoi(r.URL.Query().Get("status")); err == nil {
			w.WriteHeader(status)
		}
	}))
	for _, tc := range []struct {
		method, target string
		levels         []slog.Level
	}{
		{http.MethodGet, "/status", []slog.Level{slog.LevelDebug}},
		{http.MethodHead, "/status", []slog.Level{slog.LevelDebug}},
		{http.MethodOptions, "/message", []slog.Level{slog.LevelDebug}},
		{http.MethodGet, "/files/image?status=304", []slog.Level{slog.LevelDebug}},
		{http.MethodPost, "/message", []slog.Level{slog.LevelInfo}},
		{http.MethodDelete, "/queue/1", []slog.Level{slog.LevelInfo}},
		{http.MethodGet, "/requests/r1?status=404", []slog.Level{slog.LevelInfo}},
		{http.MethodPost, "/message?status=409", []slog.Level{slog.LevelInfo}},
		{http.MethodGet, "/status?status=500", []slog.Level{slog.LevelWarn}},
		{http.MethodPost, "/message?status=502", []slog.Level{slog.LevelWarn}},
		{http.MethodGet, "/events", nil},
		{http.MethodPost, "/terminal/input", nil},
		{http.MethodGet, "/chat/index.html", nil},
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.target, nil))
		require.Equal(t, tc.levels, rec.take(), "%s %s", tc.method, tc.target)
	}
	assert.Equal(t, slog.LevelInfo, requestLogLevel(http.MethodPatch, http.StatusOK))
}
