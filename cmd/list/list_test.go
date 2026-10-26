package list

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/registry"
)

func TestListInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hostname, _ := os.Hostname()
	summary := httpapi.SummaryBody{
		Instance:  httpapi.WebhookInstance{ServerID: "a1"},
		Status:    httpapi.AgentStatusStable,
		Attention: httpapi.AttentionRequest,
		Title:     "Fix the bug",
		Unread:    true,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summary" || r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(summary)
	}))
	defer srv.Close()
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	for _, entry := range []registry.Entry{
		{ID: "host:1", Name: "api", ServerID: "a1", URL: srv.URL, Hostname: hostname, PID: os.Getpid(), AgentType: "claude", AuthRequired: true, StartedAt: start},
		{ID: "host:2", ServerID: "b2", URL: srv.URL, Hostname: hostname, PID: os.Getpid(), AgentType: "codex", StartedAt: start.Add(time.Second)},
		{ID: "host:3", ServerID: "c3", URL: "http://127.0.0.1:1", Hostname: hostname, PID: os.Getpid(), AgentType: "pi", StartedAt: start.Add(2 * time.Second)},
	} {
		_, err := registry.Write(dir, entry)
		require.NoError(t, err)
	}

	instances, err := listInstances(context.Background(), dir, "secret", http.DefaultClient)
	require.NoError(t, err)
	require.Len(t, instances, 3)
	require.NotNil(t, instances[0].Summary)
	assert.Equal(t, "needs request, unread", stateOf(*instances[0].Summary))
	assert.Contains(t, instances[1].Error, "another AgentAPI answers", "a server ID that doesn't match")
	assert.Contains(t, instances[2].Error, "unreachable")

	noToken, err := listInstances(context.Background(), dir, "", http.DefaultClient)
	require.NoError(t, err)
	assert.Equal(t, "needs an API token", noToken[0].Error)

	var out bytes.Buffer
	require.NoError(t, writeTable(&out, instances))
	assert.Contains(t, out.String(), "api")
	assert.Contains(t, out.String(), "Fix the bug")
	out.Reset()
	require.NoError(t, writeJSON(&out, nil))
	assert.Equal(t, "[]\n", out.String())
}

func TestStateOf(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "idle", stateOf(httpapi.SummaryBody{Attention: httpapi.AttentionNone, Status: httpapi.AgentStatusStable}))
	assert.Equal(t, "working", stateOf(httpapi.SummaryBody{Attention: httpapi.AttentionNone, Status: httpapi.AgentStatusRunning}))
	assert.Equal(t, "exited", stateOf(httpapi.SummaryBody{Attention: httpapi.AttentionExited}))
	assert.Equal(t, "needs login", stateOf(httpapi.SummaryBody{Attention: httpapi.AttentionLogin}))
}
