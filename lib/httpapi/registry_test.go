package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/registry"
)

// A server with a registry directory has an entry there while it serves,
// which names it as GET /summary does and follows changes of its ID.
func TestRegistry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "instances")
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer cancel()
	factory := &nativetest.Factory{}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		Native:         httpapi.NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Bind:           "127.0.0.1",
		Port:           0,
		CWD:            "/work/api",
		APIToken:       "secret",
		RegistryDir:    dir,
	})
	require.NoError(t, err)
	served := make(chan error, 1)
	go func() { served <- srv.Start() }()

	var entry registry.Entry
	require.Eventually(t, func() bool {
		entries, err := registry.List(dir)
		require.NoError(t, err)
		if len(entries) == 1 {
			entry = entries[0]
		}
		return len(entries) == 1
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, "api", entry.Name)
	assert.True(t, entry.AuthRequired)
	assert.NotZero(t, entry.Port)
	assert.Equal(t, "http://127.0.0.1:"+itoa(entry.Port), entry.URL)

	var summary httpapi.SummaryBody
	require.Equal(t, http.StatusOK, authorized(t, http.MethodGet, entry.URL+"/summary", nil, &summary))
	assert.Equal(t, summary.Instance.ServerID, entry.ServerID)
	assert.Equal(t, summary.Instance.ID, entry.ID)

	// Changing the ID rewrites the entry.
	body := map[string]any{"url": "", "timeout_seconds": 10, "max_attempts": 3, "instance_id": "build-box"}
	require.Equal(t, http.StatusOK, authorized(t, http.MethodPut, entry.URL+"/webhook", body, nil))
	entries, err := registry.List(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "build-box", entries[0].ID)

	require.NoError(t, srv.Stop(context.Background()))
	require.True(t, errors.Is(<-served, http.ErrServerClosed))
	entries, err = registry.List(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the entry is removed")
}

// authorized sends a request with the token "secret" and decodes the JSON
// response into out. It returns the status code.
func authorized(t *testing.T, method, url string, body, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < 300 {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}
