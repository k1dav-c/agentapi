package hub_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/dispatch"
	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/hub"
)

// A launcher's host.* event joins its machine to the dispatch, the
// dispatch token starts work on it and nothing else, and the job's task
// events add up its cost.
func TestHubDispatch(t *testing.T) {
	var starts atomic.Int32
	launcher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer launcher-token" || r.Method != http.MethodPost || r.URL.Path != "/jobs" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		starts.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(host.StartResponse{Job: host.JobInfo{Key: "acme/widget#3", Agent: "claude", InstanceID: "pve~acme~widget~3", State: host.JobRunning}, Created: true})
	}))
	defer launcher.Close()

	dir := t.TempDir()
	d, err := dispatch.New(dispatch.Config{StatePath: filepath.Join(dir, "dispatch.json"), AppPath: filepath.Join(dir, "github-app.json"), Secret: hubSecret, DispatchToken: "dispatch-token"})
	require.NoError(t, err)
	srv := startHubWith(t, hub.Config{Dispatch: d}, false)
	// The page's stream has the machines and jobs, and each change.
	events := openEvents(t, srv.URL+"/api/events")
	assert.Equal(t, "instances", next(t, events).event)
	assert.Equal(t, "favorites", next(t, events).event)
	first := next(t, events)
	assert.Equal(t, "dispatch", first.event)
	assert.JSONEq(t, `{"jobs": [], "hosts": []}`, first.data)
	board := func() dispatch.Board {
		t.Helper()
		e := next(t, events)
		require.Equal(t, "dispatch", e.event)
		var b dispatch.Board
		require.NoError(t, json.Unmarshal([]byte(e.data), &b))
		return b
	}

	event := map[string]any{
		"id": "host-1", "type": host.EventHostStarted, "created_at": time.Now(),
		"instance": map[string]any{"id": "host~pve", "server_id": "run-1"},
		"data": host.HostEventData{
			HostInfo: host.HostInfo{Hostname: "pve", URL: launcher.URL, Repos: []string{"acme/widget"}, Agents: []string{"claude"}, DefaultAgent: "claude", MaxJobs: 2},
			Token:    "launcher-token", HeartbeatSeconds: 60,
		},
	}
	assert.Equal(t, http.StatusUnauthorized, postEvent(t, srv.URL, event, "wrong", time.Now()))
	assert.Equal(t, http.StatusNoContent, postEvent(t, srv.URL, event, hubSecret, time.Now()))
	joined := board()
	require.Len(t, joined.Hosts, 1)
	assert.Equal(t, "pve", joined.Hosts[0].Hostname)

	var hosts struct {
		Hosts []dispatch.Host `json:"hosts"`
	}
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, srv.URL+"/api/hosts", "dispatch-token", nil, nil))
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, srv.URL+"/api/hosts", hubToken, nil, &hosts))
	require.Len(t, hosts.Hosts, 1)
	assert.True(t, hosts.Hosts[0].Online)
	assert.Empty(t, hosts.Hosts[0].Token, "the launcher's token stays on the hub")

	start := dispatch.Request{Action: dispatch.ActionStart, Repo: "acme/widget", Issue: 3}
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPost, srv.URL+"/api/dispatch", "wrong", start, nil))
	var resp dispatch.Response
	require.Equal(t, http.StatusCreated, request(t, http.MethodPost, srv.URL+"/api/dispatch", "dispatch-token", start, &resp))
	assert.Equal(t, "pve", resp.Job.Host)
	assert.Equal(t, int32(1), starts.Load())
	for b := board(); len(b.Jobs) == 0; b = board() {
	}
	// A signed-in browser dispatches too.
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, srv.URL+"/api/dispatch", hubToken, start, &resp))
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, srv.URL+"/api/instances", "dispatch-token", nil, nil))

	task := map[string]any{
		"id": "task-1", "type": "task.completed", "created_at": time.Now(),
		"instance": map[string]any{"id": "pve~acme~widget~3", "server_id": "run-9"},
		"data":     map[string]any{"duration_seconds": 42, "cost": map[string]any{"usd": 0.3}},
	}
	assert.Equal(t, http.StatusNoContent, postEvent(t, srv.URL, task, hubSecret, time.Now()))
	var jobs struct {
		Jobs []dispatch.Job `json:"jobs"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, srv.URL+"/api/jobs", hubToken, nil, &jobs))
	require.Len(t, jobs.Jobs, 1)
	assert.InDelta(t, 0.3, jobs.Jobs[0].Runs[0].Cost.USD, 1e-9)
	assert.Equal(t, 1, jobs.Jobs[0].Runs[0].Cost.Tasks)
}
