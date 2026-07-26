package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/stretchr/testify/require"
)

func TestServer_MCPAPI(t *testing.T) {
	ctx := logctx.WithLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	var restartCount atomic.Int32
	server, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		CWD:            t.TempDir(),
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		RestartAgent: func(context.Context) error {
			restartCount.Add(1)
			return nil
		},
	})
	require.NoError(t, err)
	schema := server.GetOpenAPI()
	require.Contains(t, schema, `"summary": "Claude stdio and remote HTTP servers"`)
	require.Contains(t, schema, `"summary": "Codex stdio and remote HTTP servers"`)
	require.Contains(t, schema, `"summary": "Remove all MCP servers"`)
	require.Contains(t, schema, `"summary": "Saved and applied immediately"`)
	require.Contains(t, schema, `"default": false`)
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	request, err := http.NewRequest(
		http.MethodPut,
		testServer.URL+"/mcp?restart=true",
		bytes.NewBufferString(`{"servers":{"local":{"command":"npx","args":["server"]}}}`),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var updated struct {
		Restarted bool `json:"restarted"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&updated))
	require.NoError(t, response.Body.Close())
	require.True(t, updated.Restarted)
	require.EqualValues(t, 1, restartCount.Load())

	response, err = http.Get(testServer.URL + "/mcp")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var result struct {
		Servers map[string]any `json:"servers"`
		Path    string         `json:"path"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Contains(t, result.Servers, "local")
	require.NotEmpty(t, result.Path)
}

func TestServer_MCPUnsupportedAgent(t *testing.T) {
	ctx := logctx.WithLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeGoose,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	response, err := http.Get(testServer.URL + "/mcp")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestServer_MCPRestartUnavailable(t *testing.T) {
	ctx := logctx.WithLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		CWD:            t.TempDir(),
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	request, err := http.NewRequest(
		http.MethodPut,
		testServer.URL+"/mcp?restart=true",
		bytes.NewBufferString(`{"servers":{}}`),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
}
