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
		RestartAgent: func(context.Context) (int, error) {
			restartCount.Add(1)
			return 12345, nil
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

func TestServer_MCPManagement(t *testing.T) {
	ctx := logctx.WithLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	workDir := t.TempDir()
	server, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		CWD:            workDir,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "secret", r.Header.Get("X-Test"))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(remote.Close)

	doJSON := func(method, path, body string, status int) []byte {
		t.Helper()
		request, requestErr := http.NewRequest(method, testServer.URL+path, bytes.NewBufferString(body))
		require.NoError(t, requestErr)
		request.Header.Set("Content-Type", "application/json")
		response, responseErr := http.DefaultClient.Do(request)
		require.NoError(t, responseErr)
		defer response.Body.Close()
		data, readErr := io.ReadAll(response.Body)
		require.NoError(t, readErr)
		require.Equal(t, status, response.StatusCode, string(data))
		return data
	}

	doJSON(http.MethodPost, "/mcp/servers",
		`{"name":"local","config":{"command":"go"}}`, http.StatusOK)
	doJSON(http.MethodPost, "/mcp/servers",
		`{"name":"local","config":{"command":"go"}}`, http.StatusConflict)
	doJSON(http.MethodPost, "/mcp/servers",
		`{"name":"remote","config":{"url":"`+remote.URL+`","headers":{"X-Test":"secret"}}}`, http.StatusOK)
	doJSON(http.MethodPatch, "/mcp/servers/local",
		`{"config":{"command":"definitely-not-an-agentapi-executable"}}`, http.StatusOK)

	checkData := doJSON(http.MethodPost, "/mcp/check", `{}`, http.StatusOK)
	var checks struct {
		Results []httpapi.MCPCheckResult `json:"results"`
	}
	require.NoError(t, json.Unmarshal(checkData, &checks))
	require.Len(t, checks.Results, 2)
	require.Equal(t, "unreachable", checks.Results[0].Status)
	require.Equal(t, "ready", checks.Results[1].Status)

	profileData := doJSON(http.MethodPut, "/mcp/profiles/dev",
		`{"servers":{"saved":{"command":"go"}}}`, http.StatusOK)
	require.Contains(t, string(profileData), `"dev"`)
	require.FileExists(t, workDir+"/.agentapi/mcp-profiles.json")
	doJSON(http.MethodDelete, "/mcp/servers/remote", "", http.StatusOK)
	doJSON(http.MethodPost, "/mcp/profiles/dev/apply", "", http.StatusOK)

	response, err := http.Get(testServer.URL + "/mcp")
	require.NoError(t, err)
	defer response.Body.Close()
	var config struct {
		Servers map[string]any `json:"servers"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&config))
	require.Equal(t, []string{"saved"}, func() []string {
		names := make([]string, 0, len(config.Servers))
		for name := range config.Servers {
			names = append(names, name)
		}
		return names
	}())

	doJSON(http.MethodDelete, "/mcp/profiles/dev", "", http.StatusOK)
}
