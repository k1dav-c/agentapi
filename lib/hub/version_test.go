package hub_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/msgfmt"
)

// The page tells the instances older than the hub by the hub's own
// version, which comes with the list and the first event of the stream.
// Signing in doesn't need it, so /api/session doesn't say it.
func TestHubVersion(t *testing.T) {
	hubSrv := startHub(t, "")
	var list struct {
		Instances  []hub.Instance `json:"instances"`
		HubVersion *string        `json:"hub_version"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/api/instances", hubToken, nil, &list))
	require.NotNil(t, list.HubVersion)
	assert.Equal(t, version.Version, *list.HubVersion)
	assert.NotNil(t, list.Instances, "a list, empty")

	first := next(t, openEvents(t, hubSrv.URL+"/api/events"))
	require.Equal(t, "instances", first.event)
	var snapshot map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(first.data), &snapshot))
	assert.JSONEq(t, `"`+version.Version+`"`, string(snapshot["hub_version"]))
	assert.JSONEq(t, `[]`, string(snapshot["instances"]))

	var session map[string]any
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/api/session", "", nil, &session))
	assert.NotContains(t, session, "hub_version")
}

// fakeAgentAPI serves the summary of an AgentAPI (GET /summary and its
// stream) whose run a test changes as a restart in place does: same URL,
// another server ID and version.
type fakeAgentAPI struct {
	*httptest.Server
	mu  sync.Mutex
	run instanceapi.WebhookInstance
	// ended closes when the run ends, and its streams with it.
	ended chan struct{}
}

func startFakeAgentAPI(t *testing.T, id, serverID, agentapiVersion string) *fakeAgentAPI {
	t.Helper()
	f := &fakeAgentAPI{ended: make(chan struct{})}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.run = instanceapi.WebhookInstance{
		ID: id, ServerID: serverID, Version: agentapiVersion, URL: f.URL,
		AgentType: msgfmt.AgentTypeClaude, Transport: instanceapi.TransportNative,
	}
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAgentAPI) current() (instanceapi.SummaryBody, chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return instanceapi.SummaryBody{
		Instance: f.run, Status: instanceapi.AgentStatusStable,
		Lifecycle: instanceapi.LifecycleReady, Attention: instanceapi.AttentionNone,
	}, f.ended
}

func (f *fakeAgentAPI) serve(w http.ResponseWriter, r *http.Request) {
	summary, ended := f.current()
	data, _ := json.Marshal(summary)
	switch r.URL.Path {
	case "/summary":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	case "/events":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: summary_update\ndata: %s\n\n", data)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-ended:
		}
	default:
		http.NotFound(w, r)
	}
}

// restart ends the run, and its streams, and starts the next one.
func (f *fakeAgentAPI) restart(serverID, agentapiVersion string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.ended)
	f.ended = make(chan struct{})
	f.run.ServerID, f.run.Version = serverID, agentapiVersion
}

// An instance that restarts in place (agentapi restart, update --restart)
// comes back at the same URL as another run, of another version. The hub
// shows the new version from the new run's first event, and from its
// summary, also when no event comes (an instance added by hand sends
// none).
func TestHubVersionFollowsRestartInPlace(t *testing.T) {
	instance := startFakeAgentAPI(t, "dev-1:3284", "run-1", "1.8.1")
	hubSrv := startHub(t, "")
	started := func(delivery, serverID, agentapiVersion string) map[string]any {
		return map[string]any{
			"id": delivery, "type": "instance.started", "created_at": time.Now(),
			"instance": map[string]any{
				"id": "dev-1:3284", "server_id": serverID, "agentapi_version": agentapiVersion,
				"url": instance.URL, "agent_type": "claude",
			},
		}
	}
	shown := func() hub.Instance { return instances(t, hubSrv.URL)[0] }
	following := func(serverID string) func() bool {
		return func() bool {
			i := shown()
			return i.Connection == hub.ConnectionConnected && i.Summary != nil && i.Summary.Instance.ServerID == serverID
		}
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, started("v1", "run-1", "1.8.1"), hubSecret, time.Now()))
	require.Eventually(t, following("run-1"), 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, "1.8.1", shown().Version)
	events := openEvents(t, hubSrv.URL+"/api/events")
	require.Equal(t, "instances", next(t, events).event)

	// Its first event says the new version before its first summary does.
	instance.restart("run-2", "2.1.0")
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, started("v2", "run-2", "2.1.0"), hubSecret, time.Now()))
	assert.Equal(t, "2.1.0", shown().Version)
	require.Eventually(t, following("run-2"), 10*time.Second, 20*time.Millisecond)
	restarted := shown()
	assert.Equal(t, "run-2", restarted.ServerID)
	assert.Equal(t, "2.1.0", restarted.Version)
	assert.Equal(t, "2.1.0", restarted.Summary.Instance.Version)
	for {
		e := next(t, events)
		var changed hub.Instance
		require.NoError(t, json.Unmarshal([]byte(e.data), &changed))
		if e.event == "instance" && changed.Version == "2.1.0" {
			break
		}
	}

	// Without the event, the new run's summary says it.
	instance.restart("run-3", "2.1.1")
	require.Eventually(t, following("run-3"), 10*time.Second, 20*time.Millisecond)
	again := shown()
	assert.Equal(t, "run-3", again.ServerID)
	assert.Equal(t, "2.1.1", again.Version)
}
