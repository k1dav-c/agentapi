package hub_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/webhook"
)

const (
	hubToken      = "hub-token"
	hubSecret     = "hub-secret"
	instanceToken = "instance-token"
)

// hubs are the hubs the tests run, by URL, for flushHub.
var hubs sync.Map

// startHubWith runs a hub of config and serves it until the test ends,
// when it closes the hub; flushHub writes its files. It signs in with
// hubToken (or none, with NoToken) and checks hubSecret unless config says
// otherwise. With run, it follows its instances' summaries (Run); without,
// it follows none, so nothing changes but what the test does.
func startHubWith(t *testing.T, config hub.Config, run bool) *httptest.Server {
	t.Helper()
	if config.Token == "" && !config.NoToken {
		config.Token = hubToken
	}
	if config.Secret == "" {
		config.Secret = hubSecret
	}
	h, err := hub.New(config)
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	hubs.Store(srv.URL, h)
	stop := func() {}
	if run {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			h.Run(ctx)
			close(done)
		}()
		stop = func() {
			cancel()
			<-done
		}
	}
	t.Cleanup(func() {
		srv.Close()
		stop()
		_ = h.Close()
	})
	return srv
}

// following is the config of a hub that keeps its instances in statePath
// (none when empty), reaches them with instanceToken, and calls one
// unreachable soon after it fails.
func following(statePath string) hub.Config {
	return hub.Config{InstanceToken: instanceToken, StatePath: statePath, ConnectGrace: 200 * time.Millisecond}
}

// flushHub has the hub at hubURL write its files now: it writes a change
// within a second.
func flushHub(t *testing.T, hubURL string) {
	t.Helper()
	h, ok := hubs.Load(hubURL)
	require.True(t, ok)
	require.NoError(t, h.(*hub.Hub).Flush())
}

// startInstance runs an AgentAPI with a fake native agent that needs
// instanceToken.
func startInstance(t *testing.T) (*httptest.Server, *httpapi.Server) {
	t.Helper()
	return startInstanceOn(t, nil, "")
}

// startInstanceOn runs it on listener (nil is a new one) with the
// instance ID id (empty is <hostname>:<port>).
func startInstanceOn(t *testing.T, listener net.Listener, id string) (*httptest.Server, *httpapi.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      instanceapi.AgentTypeClaude,
		Native:         httpapi.NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            "/work/api",
		APIToken:       instanceToken,
		Webhook:        webhook.Config{InstanceID: id},
	})
	require.NoError(t, err)
	ts := httptest.NewUnstartedServer(srv.Handler())
	if listener != nil {
		_ = ts.Listener.Close()
		ts.Listener = listener
	}
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Stop(context.Background())
		cancel()
	})
	return ts, srv
}

func request(t *testing.T, method, url, bearer string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
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

func instances(t *testing.T, hubURL string) []hub.Instance {
	t.Helper()
	var body struct {
		Instances []hub.Instance `json:"instances"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubURL+"/api/instances", hubToken, nil, &body))
	return body.Instances
}

// The instances join with their webhook; the hub follows their summaries
// and passes the browser's requests on with their token.
func TestHub(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "hub", "instances.json")
	// Started first, stopped last: the hubs' summary streams end first.
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(statePath), true)

	// What agentapi hub join does to a running instance.
	webhook := map[string]any{
		"url": hubSrv.URL + "/hooks", "timeout_seconds": 5, "max_attempts": 3,
		"events": []string{"instance.*", "task.*"}, "secret": hubSecret, "public_url": instSrv.URL,
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodPut, instSrv.URL+"/webhook", instanceToken, webhook, nil))

	var joined hub.Instance
	require.Eventually(t, func() bool {
		list := instances(t, hubSrv.URL)
		if len(list) == 1 && list[0].Connection == instanceapi.ConnectionConnected {
			joined = list[0]
			return true
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, instSrv.URL, joined.URL)
	assert.Equal(t, "api", joined.Name)
	assert.Equal(t, hub.SourceWebhook, joined.Source)
	require.NotNil(t, joined.Summary)
	assert.Empty(t, joined.Token, "the browser never gets a token")
	assert.Equal(t, instanceapi.StateIdle, joined.State)
	// Who it is, as it says: the hub keeps all of it.
	assert.Equal(t, os.Getpid(), joined.PID)
	assert.NotEmpty(t, joined.Version)
	assert.Equal(t, instanceapi.TransportNative, joined.Transport)

	// The browser reaches the instance through the hub, which sends the
	// instance's token, not the browser's credentials.
	id := joined.ID
	var status convstate.StatusChangeBody
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", hubToken, nil, &status))
	assert.Equal(t, joined.ServerID, status.ServerID)
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", "", nil, nil), "not signed in")
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", instanceToken, nil, nil), "the instance's token isn't the hub's")
	assert.Equal(t, http.StatusNotFound, request(t, http.MethodGet, hubSrv.URL+"/i/nope/status", hubToken, nil, nil))
	// As a browser escapes it (encodeURIComponent).
	escaped := strings.ReplaceAll(url.PathEscape(id), ":", "%3A")
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+escaped+"/status", hubToken, nil, nil))

	// The file keeps the instance, readable by its owner only.
	flushHub(t, hubSrv.URL)
	info, err := os.Stat(statePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The browser's stream: the instances, then their changes.
	events := openEvents(t, hubSrv.URL+"/api/events")
	first := next(t, events)
	require.Equal(t, "instances", first.event)
	assert.Contains(t, first.data, id)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/i/"+id+"/message", hubToken, map[string]string{"content": "hello", "type": "user"}, nil))
	for {
		e := next(t, events)
		if e.event != "instance" {
			continue
		}
		var changed hub.Instance
		require.NoError(t, json.Unmarshal([]byte(e.data), &changed))
		if changed.Summary != nil && changed.Summary.Task != nil {
			assert.Equal(t, "hello", changed.Summary.Task.Prompt)
			assert.Equal(t, instanceapi.StateOf(changed.Connection, changed.Summary, ""), changed.State, "the state comes with every change")
			break
		}
	}

	// A hub that starts again knows the instance.
	flushHub(t, hubSrv.URL)
	again := startHubWith(t, following(statePath), true)
	require.Eventually(t, func() bool {
		list := instances(t, again.URL)
		return len(list) == 1 && list[0].Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)

	// Forgetting it.
	require.Equal(t, http.StatusNoContent, request(t, http.MethodDelete, again.URL+"/api/instances/"+id, hubToken, nil, nil))
	assert.Empty(t, instances(t, again.URL))
}

// sign signs body as an AgentAPI does at time at.
func sign(body []byte, secret string, at time.Time) (string, string) {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	return timestamp, instanceapi.Sign(secret, timestamp, body)
}

func postEvent(t *testing.T, hubURL string, event map[string]any, secret string, at time.Time) int {
	t.Helper()
	body, err := json.Marshal(event)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, hubURL+"/hooks", bytes.NewReader(body))
	require.NoError(t, err)
	timestamp, signature := sign(body, secret, at)
	req.Header.Set("X-AgentAPI-Timestamp", timestamp)
	req.Header.Set("X-AgentAPI-Signature-256", signature)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Only signed, recent events count: an event names the URL the hub sends
// an instance token to.
func TestHubWebhookSignature(t *testing.T) {
	hubSrv := startHubWith(t, following(""), true)
	event := func(id, typ string) map[string]any {
		return map[string]any{
			"id": id, "type": typ, "created_at": time.Now(),
			"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
		}
	}
	assert.Equal(t, http.StatusUnauthorized, postEvent(t, hubSrv.URL, event("e1", "instance.started"), "wrong", time.Now()))
	assert.Equal(t, http.StatusUnauthorized, postEvent(t, hubSrv.URL, event("e2", "instance.started"), hubSecret, time.Now().Add(-time.Hour)), "too old")
	assert.Empty(t, instances(t, hubSrv.URL))

	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("e3", "instance.started"), hubSecret, time.Now()))
	list := instances(t, hubSrv.URL)
	require.Len(t, list, 1)
	assert.Equal(t, "http://127.0.0.1:1", list[0].URL)
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond, "nothing listens there")

	// The same delivery again changes nothing; instance.stopped stops it.
	stopped := event("e3", "instance.stopped")
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	assert.NotEqual(t, instanceapi.ConnectionStopped, instances(t, hubSrv.URL)[0].Connection, "a replayed delivery")
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("e4", "instance.stopped"), hubSecret, time.Now()))
	assert.Equal(t, instanceapi.ConnectionStopped, instances(t, hubSrv.URL)[0].Connection)
}

// An event without a delivery ID can't be told from a copy of it, which
// would work again for the 10 minutes its signature holds: the hub refuses
// it, and every copy.
func TestHubRefusesEventsWithoutDeliveryID(t *testing.T) {
	var logs lockedBuffer
	h, err := hub.New(hub.Config{Token: hubToken, Secret: hubSecret, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	hubSrv := httptest.NewServer(h.Handler())
	t.Cleanup(hubSrv.Close)

	at := time.Now()
	captured := map[string]any{
		"type": "instance.started", "created_at": at,
		"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
	}
	for range 2 {
		assert.Equal(t, http.StatusBadRequest, postEvent(t, hubSrv.URL, captured, hubSecret, at))
	}
	assert.Empty(t, instances(t, hubSrv.URL))
	check := map[string]any{"type": "webhook.test", "created_at": at, "instance": map[string]any{"id": "agentapi-hub-join-check"}}
	assert.Equal(t, http.StatusBadRequest, postEvent(t, hubSrv.URL, check, hubSecret, at))
	assert.Equal(t, 3, strings.Count(logs.String(), "Refused an event without a delivery ID"))
}

// An instance that isn't listening yet when its first event comes (it is
// starting) is connecting, not unreachable, until it does; one that never
// does is unreachable once the connect grace is over.
func TestHubConnectGrace(t *testing.T) {
	hubSrv := startHubWith(t, hub.Config{InstanceToken: instanceToken, ConnectGrace: 3 * time.Second}, true)
	// A port nothing listens on yet.
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := reserved.Addr().String()
	require.NoError(t, reserved.Close())
	started := map[string]any{
		"id": "e1", "type": "instance.started", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://" + addr, "agent_type": "claude"},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, started, hubSecret, time.Now()))

	// It starts a moment later; until then it is connecting.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		assert.Equal(t, instanceapi.ConnectionConnecting, instances(t, hubSrv.URL)[0].Connection)
		time.Sleep(50 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	startInstanceOn(t, listener, "dev-1:3284")
	// Stopped before the instance closes (cleanups run last first): its
	// server waits for the hub's stream to end. With the instance's own
	// server ID, which the hub took from its summary.
	t.Cleanup(func() {
		serverID := ""
		for _, instance := range instances(t, hubSrv.URL) {
			if instance.ID == "dev-1:3284" {
				serverID = instance.ServerID
			}
		}
		stopped := map[string]any{"id": "e3", "type": "instance.stopped", "created_at": time.Now(), "instance": map[string]any{"id": "dev-1:3284", "server_id": serverID, "url": "http://" + addr}}
		assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	})
	require.Eventually(t, func() bool {
		connection := instances(t, hubSrv.URL)[0].Connection
		assert.NotEqual(t, instanceapi.ConnectionUnreachable, connection)
		return connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 50*time.Millisecond)

	// One that never listens is unreachable after the grace.
	other := map[string]any{
		"id": "e2", "type": "instance.started", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev-2:3284", "server_id": "s2", "url": "http://127.0.0.1:1", "agent_type": "codex"},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, other, hubSecret, time.Now()))
	connection := func() instanceapi.Connection {
		for _, instance := range instances(t, hubSrv.URL) {
			if instance.ID == "dev-2:3284" {
				return instance.Connection
			}
		}
		return ""
	}
	time.Sleep(time.Second)
	assert.Equal(t, instanceapi.ConnectionConnecting, connection(), "within the grace")
	require.Eventually(t, func() bool { return connection() == instanceapi.ConnectionUnreachable }, 20*time.Second, 100*time.Millisecond)
}

// joinInstance has a running AgentAPI join the hub, as agentapi hub join
// does, and returns it once the hub follows it.
func joinInstance(t *testing.T, hubURL, instanceURL string) hub.Instance {
	t.Helper()
	webhook := map[string]any{
		"url": hubURL + "/hooks", "timeout_seconds": 5, "max_attempts": 3,
		"events": []string{"instance.*"}, "secret": hubSecret, "public_url": instanceURL,
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodPut, instanceURL+"/webhook", instanceToken, webhook, nil))
	var joined hub.Instance
	require.Eventually(t, func() bool {
		list := instances(t, hubURL)
		if len(list) == 1 && list[0].Connection == instanceapi.ConnectionConnected {
			joined = list[0]
			return true
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	return joined
}

// Another AgentAPI that answers at an instance's URL (the address went to
// another machine) isn't taken for it: the hub shows why, and doesn't send
// it the instance's requests.
func TestHubOtherInstanceAnswers(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(""), true)
	started := map[string]any{
		"id": "o1", "type": "instance.started", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": instSrv.URL, "agent_type": "claude"},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, started, hubSecret, time.Now()))
	var shown hub.Instance
	require.Eventually(t, func() bool {
		shown = instances(t, hubSrv.URL)[0]
		return shown.Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)
	assert.Contains(t, shown.Error, "another AgentAPI answers at this URL")
	assert.Nil(t, shown.Summary, "not the other one's summary")
	assert.Equal(t, "s1", shown.ServerID)
	assert.Equal(t, instanceapi.StateUnreachable, shown.State)
	assert.Equal(t, http.StatusBadGateway, request(t, http.MethodGet, hubSrv.URL+"/i/dev-1:3284/status", hubToken, nil, nil))
}

// Two AgentAPIs with the same instance ID (machines with the same name):
// the hub keeps the one it follows instead of switching at each heartbeat.
func TestHubKeepsTheRunItFollows(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(""), true)
	joined := joinInstance(t, hubSrv.URL, instSrv.URL)

	twin := func(id, typ string) map[string]any {
		return map[string]any{
			"id": id, "type": typ, "created_at": time.Now(),
			"instance": map[string]any{"id": joined.ID, "server_id": "twin", "url": "http://127.0.0.1:1", "agent_type": "claude"},
		}
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, twin("t1", "instance.heartbeat"), hubSecret, time.Now()))
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, twin("t2", "instance.stopped"), hubSecret, time.Now()))
	kept := instances(t, hubSrv.URL)[0]
	assert.Equal(t, instSrv.URL, kept.URL)
	assert.Equal(t, joined.ServerID, kept.ServerID)
	assert.Equal(t, instanceapi.ConnectionConnected, kept.Connection)

	// Once the one it follows stopped, the other is the instance; but an
	// instance.stopped is no sign its URL is dead (anyone with the secret
	// can send one): the hub tries that URL first, and stays while the
	// instance answers there.
	stopped := map[string]any{
		"id": "s1", "type": "instance.stopped", "created_at": time.Now(),
		"instance": map[string]any{"id": joined.ID, "server_id": joined.ServerID, "url": instSrv.URL},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	require.Equal(t, instanceapi.ConnectionStopped, instances(t, hubSrv.URL)[0].Connection)
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, twin("t3", "instance.heartbeat"), hubSecret, time.Now()))
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond, "it still answers")
	kept = instances(t, hubSrv.URL)[0]
	assert.Equal(t, instSrv.URL, kept.URL)
	assert.Equal(t, joined.ServerID, kept.ServerID)

	// Gone for real: once the hub fails there, the other's next event
	// moves it.
	stopped["id"] = "s2"
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	_ = instSrv.Listener.Close()
	instSrv.CloseClientConnections()
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, twin("t4", "instance.heartbeat"), hubSecret, time.Now()))
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, instSrv.URL, instances(t, hubSrv.URL)[0].URL, "not before it failed there")
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, twin("t5", "instance.heartbeat"), hubSecret, time.Now()))
	moved := instances(t, hubSrv.URL)[0]
	assert.Equal(t, "http://127.0.0.1:1", moved.URL)
	assert.Equal(t, "twin", moved.ServerID)
}

// elsewhere serves another URL and counts the requests it got: the hub
// must send it nothing of an instance it doesn't move there.
func elsewhere(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var got atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &got
}

// A signed event moves no instance the hub follows, whatever run it names:
// the hub would send that URL the instance's token, and the user's
// messages and approvals. An instance.* event that names no run is
// refused; AgentAPI has named it since 1.0.
func TestHubEventsDontMoveAFollowedInstance(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(""), true)
	joined := joinInstance(t, hubSrv.URL, instSrv.URL)
	otherURL, got := elsewhere(t)

	event := func(id, typ, instanceID, serverID string) map[string]any {
		instance := map[string]any{"id": instanceID, "url": otherURL, "agent_type": "claude"}
		if serverID != "" {
			instance["server_id"] = serverID
		}
		return map[string]any{"id": id, "type": typ, "created_at": time.Now(), "instance": instance}
	}
	for i, typ := range []string{"instance.heartbeat", "instance.started", "instance.stopped"} {
		assert.Equal(t, http.StatusBadRequest, postEvent(t, hubSrv.URL, event("n"+strconv.Itoa(i), typ, joined.ID, ""), hubSecret, time.Now()), typ)
	}
	assert.Equal(t, http.StatusBadRequest, postEvent(t, hubSrv.URL, event("n9", "instance.started", "dev-9:3284", ""), hubSecret, time.Now()))
	assert.Len(t, instances(t, hubSrv.URL), 1, "nor added")

	// Its own run, or another, at another URL.
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("r1", "instance.heartbeat", joined.ID, joined.ServerID), hubSecret, time.Now()))
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("r2", "instance.started", joined.ID, "other"), hubSecret, time.Now()))

	kept := instances(t, hubSrv.URL)[0]
	assert.Equal(t, instSrv.URL, kept.URL)
	assert.Equal(t, joined.ServerID, kept.ServerID)
	assert.Equal(t, instanceapi.ConnectionConnected, kept.Connection)
	assert.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+joined.ID+"/status", hubToken, nil, nil))
	assert.Never(t, func() bool { return got.Load() > 0 }, 300*time.Millisecond, 20*time.Millisecond, "nothing went to the other URL")
}

// An AgentAPI whose machine got another address (DHCP, after its
// workspace restarted) names it in its next heartbeat ({ip} in its public
// URL). The hub keeps the URL it reaches while that answers; once it
// stopped answering, the heartbeat moves the instance to the new one.
func TestHubFollowsANewAddress(t *testing.T) {
	oldSrv, inst := startInstanceOn(t, nil, "dev-1:3285")
	hubSrv := startHubWith(t, following(""), true)
	joined := joinInstance(t, hubSrv.URL, oldSrv.URL)
	// The same AgentAPI at its new address.
	newSrv := httptest.NewServer(inst.Handler())
	t.Cleanup(newSrv.Close)
	// Stopped before newSrv closes (cleanups run last first): its server
	// waits for the hub's stream to end.
	t.Cleanup(func() {
		stopped := map[string]any{"id": "d9", "type": "instance.stopped", "created_at": time.Now(), "instance": map[string]any{"id": joined.ID, "server_id": joined.ServerID, "url": newSrv.URL}}
		assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	})
	heartbeat := func(id string) map[string]any {
		return map[string]any{
			"id": id, "type": "instance.heartbeat", "created_at": time.Now(),
			"instance": map[string]any{"id": joined.ID, "server_id": joined.ServerID, "url": newSrv.URL, "agent_type": "claude"},
			"data":     map[string]any{"heartbeat_seconds": 60},
		}
	}

	// While the old address answers, the hub stays there.
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, heartbeat("d1"), hubSecret, time.Now()))
	kept := instances(t, hubSrv.URL)[0]
	assert.Equal(t, oldSrv.URL, kept.URL)
	assert.Equal(t, instanceapi.ConnectionConnected, kept.Connection)

	// The old address is gone.
	_ = oldSrv.Listener.Close()
	oldSrv.CloseClientConnections()
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)

	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, heartbeat("d2"), hubSecret, time.Now()))
	var moved hub.Instance
	require.Eventually(t, func() bool {
		moved = instances(t, hubSrv.URL)[0]
		return moved.Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, newSrv.URL, moved.URL)
	assert.Equal(t, joined.ServerID, moved.ServerID, "the same run")
	assert.Empty(t, moved.Error)
	assert.Len(t, instances(t, hubSrv.URL), 1, "moved, not added")
	assert.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+joined.ID+"/status", hubToken, nil, nil), "requests go to the new address")
}

// gatedInstance serves an AgentAPI whose summary stream (GET /events) can
// be held: while hold is set, a stream the hub opens waits, unanswered,
// until release (or the hub gives up): the hub connects, and doesn't
// reach it yet.
type gatedInstance struct {
	*httptest.Server
	inst    *httpapi.Server
	hold    atomic.Bool
	release chan struct{}
}

func startGatedInstance(t *testing.T) *gatedInstance {
	t.Helper()
	_, inst := startInstanceOn(t, nil, "dev-1:3285")
	g := &gatedInstance{inst: inst, release: make(chan struct{})}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" && g.hold.Load() {
			select {
			case <-g.release:
			case <-r.Context().Done():
				return
			}
		}
		inst.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(g.Close)
	return g
}

// hijack is a heartbeat of another machine that has the hub's secret,
// for the instance joined, with its own URL.
func hijack(id string, joined hub.Instance, url string) map[string]any {
	return map[string]any{
		"id": id, "type": "instance.heartbeat", "created_at": time.Now(),
		"instance": map[string]any{"id": joined.ID, "server_id": joined.ServerID, "url": url, "agent_type": "claude"},
		"data":     map[string]any{"heartbeat_seconds": 60},
	}
}

// A machine that has the hub's secret can't take over another's instance
// while the hub connects to it after it restarted: the hub follows it
// where it last reached it, sends nothing through the proxy until it
// reaches it there, and nothing to the other URL.
func TestHubRestartRefusesAMove(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	inst := startGatedInstance(t)
	first := startHubWith(t, following(statePath), true)
	joined := joinInstance(t, first.URL, inst.URL)
	otherURL, got := elsewhere(t)
	flushHub(t, first.URL)

	inst.hold.Store(true)
	again := startHubWith(t, hub.Config{InstanceToken: instanceToken, StatePath: statePath, ConnectGrace: 5 * time.Second}, true)
	require.Equal(t, instanceapi.ConnectionConnecting, instances(t, again.URL)[0].Connection)
	assert.Equal(t, http.StatusNoContent, postEvent(t, again.URL, hijack("h1", joined, otherURL), hubSecret, time.Now()))
	kept := instances(t, again.URL)[0]
	assert.Equal(t, inst.URL, kept.URL)
	assert.Equal(t, instanceapi.ConnectionConnecting, kept.Connection)
	assert.Equal(t, http.StatusServiceUnavailable, request(t, http.MethodGet, again.URL+"/i/"+joined.ID+"/status", hubToken, nil, nil), "not connected yet")

	close(inst.release)
	require.Eventually(t, func() bool {
		return instances(t, again.URL)[0].Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, inst.URL, instances(t, again.URL)[0].URL)
	assert.Equal(t, http.StatusOK, request(t, http.MethodGet, again.URL+"/i/"+joined.ID+"/status", hubToken, nil, nil))
	assert.Never(t, func() bool { return got.Load() > 0 }, 300*time.Millisecond, 20*time.Millisecond, "nothing went to the other URL")
}

// Nor while its summary stream dropped and the hub connects again, within
// the connect grace.
func TestHubStreamDropRefusesAMove(t *testing.T) {
	inst := startGatedInstance(t)
	hubSrv := startHubWith(t, hub.Config{InstanceToken: instanceToken, ConnectGrace: 5 * time.Second}, true)
	joined := joinInstance(t, hubSrv.URL, inst.URL)
	otherURL, got := elsewhere(t)

	inst.hold.Store(true)
	inst.CloseClientConnections()
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionConnecting
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, hijack("h1", joined, otherURL), hubSecret, time.Now()))
	assert.Equal(t, inst.URL, instances(t, hubSrv.URL)[0].URL)
	assert.Equal(t, http.StatusServiceUnavailable, request(t, http.MethodGet, hubSrv.URL+"/i/"+joined.ID+"/status", hubToken, nil, nil))

	close(inst.release)
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, inst.URL, instances(t, hubSrv.URL)[0].URL)
	assert.Never(t, func() bool { return got.Load() > 0 }, 300*time.Millisecond, 20*time.Millisecond, "nothing went to the other URL")
}

// An instance moved to a URL that doesn't answer as it gets nothing
// through the proxy there: only the hub's watch, which waits for its
// summary.
func TestHubProxyWaitsForAMovedInstance(t *testing.T) {
	inst, _ := startInstanceOn(t, nil, "dev-1:3285")
	hubSrv := startHubWith(t, following(""), true)
	joined := joinInstance(t, hubSrv.URL, inst.URL)
	var mu sync.Mutex
	var paths []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(other.Close)

	_ = inst.Listener.Close()
	inst.CloseClientConnections()
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, hijack("m1", joined, other.URL), hubSecret, time.Now()))
	assert.Equal(t, other.URL, instances(t, hubSrv.URL)[0].URL, "its URL is dead: it moves")
	for _, path := range []string{"/status", "/message", "/update"} {
		assert.Equal(t, http.StatusServiceUnavailable, request(t, http.MethodGet, hubSrv.URL+"/i/"+joined.ID+path, hubToken, nil, nil), path)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		assert.Equal(t, "/events", path, "only the hub's watch")
	}
}

// An instance added by hand stays at its URL: no event moves it, not even
// while the hub can't reach it.
func TestHubAddedInstanceStays(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(""), true)
	var added hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": instSrv.URL, "token": instanceToken}, &added))
	otherURL, got := elsewhere(t)
	// Gone, as far as the hub can tell (Close would wait for the hub's
	// stream to end).
	_ = instSrv.Listener.Close()
	instSrv.CloseClientConnections()
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)

	heartbeat := map[string]any{
		"id": "m1", "type": "instance.heartbeat", "created_at": time.Now(),
		"instance": map[string]any{"id": added.ID, "server_id": "another-run", "url": otherURL, "agent_type": "claude"},
	}
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, heartbeat, hubSecret, time.Now()))
	kept := instances(t, hubSrv.URL)[0]
	assert.Equal(t, instSrv.URL, kept.URL)
	assert.Equal(t, hub.SourceManual, kept.Source)
	assert.Never(t, func() bool { return got.Load() > 0 }, 300*time.Millisecond, 20*time.Millisecond, "nothing went to the other URL")
}

// An instance added by its URL, with its own token.
func TestHubAddInstance(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHubWith(t, following(""), true)
	assert.Equal(t, http.StatusBadRequest, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": "ftp://x"}, nil))
	var added hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": instSrv.URL + "/", "token": instanceToken}, &added))
	assert.Equal(t, hub.SourceManual, added.Source)
	assert.Equal(t, instSrv.URL, added.URL)
	assert.Empty(t, added.Token)
	require.Eventually(t, func() bool {
		list := instances(t, hubSrv.URL)
		return len(list) == 1 && list[0].Connection == instanceapi.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)
}

// Signing in trades the hub token for a cookie, which the hub's pages and
// the proxy accept.
func TestHubSession(t *testing.T) {
	hubSrv := startHubWith(t, following(""), true)
	var session map[string]bool
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/api/session", "", nil, &session))
	assert.False(t, session["authenticated"])
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPost, hubSrv.URL+"/api/session", "", map[string]string{"token": "wrong"}, nil))

	body, _ := json.Marshal(map[string]string{"token": hubToken})
	resp, err := http.Post(hubSrv.URL+"/api/session", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].HttpOnly)
	assert.NotContains(t, cookies[0].Value, hubToken)

	req, err := http.NewRequest(http.MethodGet, hubSrv.URL+"/api/instances", nil)
	require.NoError(t, err)
	req.AddCookie(cookies[0])
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNewRefusesWithoutSecrets(t *testing.T) {
	_, err := hub.New(hub.Config{Secret: "s"})
	assert.ErrorContains(t, err, "--token")
	_, err = hub.New(hub.Config{Token: "t"})
	assert.ErrorContains(t, err, "--secret")
	_, err = hub.New(hub.Config{Token: "t", NoToken: true, Secret: "s"})
	assert.ErrorContains(t, err, "not both")
}

// A hub without a token lets everyone in: no sign-in, no bearer.
func TestHubNoToken(t *testing.T) {
	h, err := hub.New(hub.Config{NoToken: true, Secret: hubSecret})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	var session struct {
		Authenticated bool `json:"authenticated"`
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, srv.URL+"/api/session", "", nil, &session))
	assert.True(t, session.Authenticated)
	var list struct {
		Instances []hub.Instance `json:"instances"`
	}
	assert.Equal(t, http.StatusOK, request(t, http.MethodGet, srv.URL+"/api/instances", "", nil, &list))
	// Signing in with anything works too, for a page that tries.
	assert.Equal(t, http.StatusOK, request(t, http.MethodPost, srv.URL+"/api/session", "", map[string]string{"token": "anything"}, &session))
	assert.True(t, session.Authenticated)
}

type sseEvent struct{ event, data string }

func openEvents(t *testing.T, url string) <-chan sseEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+hubToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := make(chan sseEvent, 100)
	go func() {
		defer close(events)
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var e sseEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			case line == "" && e.event != "":
				events <- e
				e = sseEvent{}
			}
		}
	}()
	return events
}

func next(t *testing.T, events <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case e, ok := <-events:
		require.True(t, ok, "the stream ended")
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
		return sseEvent{}
	}
}

// A group set on the hub's page stays: through the instance's own events
// and a restart of the hub.
func TestHubGroups(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubSrv := startHubWith(t, following(statePath), true)
	event := func(id string) map[string]any {
		return map[string]any{
			"id": id, "type": "instance.heartbeat", "created_at": time.Now(),
			"instance": map[string]any{"id": "dev-1:3284", "name": "api", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
		}
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("g1"), hubSecret, time.Now()))
	path := hubSrv.URL + "/api/instances/" + url.PathEscape("dev-1:3284")

	var changed hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPatch, path, hubToken, map[string]string{"group": "  Client   work "}, &changed))
	assert.Equal(t, "Client work", changed.Group, "spaces are collapsed")
	assert.Equal(t, http.StatusBadRequest, request(t, http.MethodPatch, path, hubToken, map[string]string{"group": strings.Repeat("長", 41)}, nil))
	assert.Equal(t, http.StatusBadRequest, request(t, http.MethodPatch, path, hubToken, map[string]string{}, nil))
	assert.Equal(t, http.StatusNotFound, request(t, http.MethodPatch, hubSrv.URL+"/api/instances/nope", hubToken, map[string]string{"group": "x"}, nil))
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPatch, path, "", map[string]string{"group": "x"}, nil))

	// The instance's events don't touch it.
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("g2"), hubSecret, time.Now()))
	assert.Equal(t, "Client work", instances(t, hubSrv.URL)[0].Group)

	// A hub that starts again knows it.
	flushHub(t, hubSrv.URL)
	again := startHubWith(t, following(statePath), true)
	assert.Equal(t, "Client work", instances(t, again.URL)[0].Group)
	var ungrouped hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPatch, again.URL+"/api/instances/"+url.PathEscape("dev-1:3284"), hubToken, map[string]string{"group": ""}, &ungrouped))
	assert.Empty(t, ungrouped.Group)
}

// lockedBuffer is a log the hub's goroutines write while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A file of instances that doesn't parse (cut short by a crash or a full
// disk) doesn't stop the hub, which would stop again at every restart: it
// is moved aside, the hub says what is lost and starts empty, and the next
// change writes the file again.
func TestHubDamagedState(t *testing.T) {
	for name, content := range map[string]string{
		"cut":   `[{"id": "dev-1:3284", "url": "http://dev-1.vpn:3284", "token": "its-own", "gro`,
		"empty": "",
	} {
		t.Run(name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "instances.json")
			require.NoError(t, os.WriteFile(statePath, []byte(content), 0o600))
			var logs lockedBuffer
			h, err := hub.New(hub.Config{Token: hubToken, Secret: hubSecret, StatePath: statePath, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			require.NoError(t, err)
			srv := httptest.NewServer(h.Handler())
			t.Cleanup(srv.Close)
			assert.Empty(t, instances(t, srv.URL))

			aside, err := filepath.Glob(statePath + ".corrupt-*")
			require.NoError(t, err)
			require.Len(t, aside, 1)
			kept, err := os.ReadFile(aside[0])
			require.NoError(t, err)
			assert.Equal(t, content, string(kept), "kept as it was")
			info, err := os.Stat(aside[0])
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "it may hold tokens")
			assert.Contains(t, logs.String(), "added by hand")
			assert.Contains(t, logs.String(), aside[0])

			started := map[string]any{
				"id": "d1", "type": "instance.started", "created_at": time.Now(),
				"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
			}
			require.Equal(t, http.StatusNoContent, postEvent(t, srv.URL, started, hubSecret, time.Now()))
			require.NoError(t, h.Close())
			data, err := os.ReadFile(statePath)
			require.NoError(t, err)
			var stored struct {
				Version   int              `json:"version"`
				Instances []map[string]any `json:"instances"`
			}
			require.NoError(t, json.Unmarshal(data, &stored))
			assert.Equal(t, 1, stored.Version)
			assert.Len(t, stored.Instances, 1)
		})
	}
}

// startHubOnClock runs a hub on clock that follows its instances and
// returns its URL; retry traps the waits between its attempts to reach an
// instance, until the test ends.
func startHubOnClock(t *testing.T, clock *quartz.Mock) (string, *quartz.Trap) {
	t.Helper()
	srv := startHubWith(t, hub.Config{ConnectGrace: 10 * time.Second, Clock: clock}, true)
	retry := clock.Trap().NewTimer("hub", "retry")
	// Closed first: a watch waits in it.
	t.Cleanup(retry.Close)
	return srv.URL, retry
}

// passRetries lets the hub's waits between attempts pass, until the clock
// is at least until past start.
func passRetries(t *testing.T, clock *quartz.Mock, retry *quartz.Trap, start time.Time, until time.Duration) {
	t.Helper()
	for clock.Now().Sub(start) < until {
		call := retry.MustWait(t.Context())
		call.Release()
		clock.Advance(call.Duration).MustWait(t.Context())
	}
}

// An instance killed without instance.stopped (with its workspace) stops
// sending heartbeats: once it missed three while the hub couldn't reach
// it, it counts as stopped and the hub stops trying. Its next heartbeat
// follows it again.
func TestHubMissedHeartbeats(t *testing.T) {
	clock := quartz.NewMock(t)
	hubURL, retry := startHubOnClock(t, clock)
	heartbeat := func(id string) map[string]any {
		return map[string]any{
			"id": id, "type": "instance.heartbeat", "created_at": clock.Now(),
			"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
			"data":     map[string]any{"status": "stable", "heartbeat_seconds": 60},
		}
	}
	start := clock.Now()
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, heartbeat("h1"), hubSecret, start))
	passRetries(t, clock, retry, start, 2*time.Minute)
	gone := instances(t, hubURL)[0]
	assert.Equal(t, instanceapi.ConnectionUnreachable, gone.Connection, "two heartbeats missed")
	assert.Equal(t, 60, gone.HeartbeatSeconds)

	passRetries(t, clock, retry, start, 3*time.Minute)
	require.Eventually(t, func() bool {
		return instances(t, hubURL)[0].Connection == instanceapi.ConnectionStopped
	}, 5*time.Second, 10*time.Millisecond, "three missed")
	assert.Equal(t, instanceapi.StateStopped, instances(t, hubURL)[0].State)

	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, heartbeat("h2"), hubSecret, clock.Now()))
	assert.Equal(t, instanceapi.ConnectionConnecting, instances(t, hubURL)[0].Connection)
	retry.MustWait(t.Context()).Release()
}

// An AgentAPI that doesn't say how often it sends a heartbeat (an older
// one, or one with them off) isn't taken as stopped for missing them.
func TestHubNoHeartbeats(t *testing.T) {
	clock := quartz.NewMock(t)
	hubURL, retry := startHubOnClock(t, clock)
	started := map[string]any{
		"id": "o1", "type": "instance.started", "created_at": clock.Now(),
		"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
	}
	start := clock.Now()
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, started, hubSecret, start))
	passRetries(t, clock, retry, start, 10*time.Minute)
	assert.Equal(t, instanceapi.ConnectionUnreachable, instances(t, hubURL)[0].Connection)
}

// The API and the browser's stream say when each instance last changed what
// its row shows (changed_at, RFC 3339): when the hub first saw it, not again
// for a heartbeat, and when the hub can't reach it any more.
func TestHubChangedAt(t *testing.T) {
	clock := quartz.NewMock(t)
	hubURL, retry := startHubOnClock(t, clock)
	heartbeat := func(id string) map[string]any {
		return map[string]any{
			"id": id, "type": "instance.heartbeat", "created_at": clock.Now(),
			"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
		}
	}
	start := clock.Now()
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, heartbeat("c1"), hubSecret, start))
	firstSeen := `"changed_at":"` + start.Format(time.RFC3339Nano) + `"`
	req, err := http.NewRequest(http.MethodGet, hubURL+"/api/instances", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+hubToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	assert.Contains(t, string(body), firstSeen)
	events := openEvents(t, hubURL+"/api/events")
	first := next(t, events)
	require.Equal(t, "instances", first.event)
	assert.Contains(t, first.data, firstSeen)

	// advance moves the clock d on, stopping at each timer and ticker on
	// the way, such as the browser's heartbeats.
	advance := func(d time.Duration) {
		for d > 0 {
			step := d
			if next, ok := clock.Peek(); ok && next < step {
				step = next
			}
			clock.Advance(step).MustWait(t.Context())
			d -= step
		}
	}

	// Its first attempt to reach it failed; a heartbeat changes nothing.
	call := retry.MustWait(t.Context())
	advance(5 * time.Second)
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, heartbeat("c2"), hubSecret, clock.Now()))
	assert.True(t, instances(t, hubURL)[0].ChangedAt.Equal(start), "a heartbeat")
	call.Release()
	advance(call.Duration)

	// Unreachable once the grace is over.
	for clock.Now().Sub(start) < 15*time.Second {
		call := retry.MustWait(t.Context())
		call.Release()
		advance(call.Duration)
	}
	retry.MustWait(t.Context()).Release()
	gone := instances(t, hubURL)[0]
	require.Equal(t, instanceapi.ConnectionUnreachable, gone.Connection)
	assert.True(t, gone.ChangedAt.After(start))
	for {
		e := next(t, events)
		var changed hub.Instance
		if e.event != "instance" || json.Unmarshal([]byte(e.data), &changed) != nil || changed.Connection != instanceapi.ConnectionUnreachable {
			continue
		}
		assert.True(t, changed.ChangedAt.Equal(gone.ChangedAt))
		break
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, heartbeat("c3"), hubSecret, clock.Now()))
	assert.True(t, instances(t, hubURL)[0].ChangedAt.Equal(gone.ChangedAt), "heard of while unreachable")
}

// The hub keeps the delivery IDs it took next to its instances: a copy of
// an event replayed just after it restarts is refused, as before it.
func TestHubRefusesAReplayAfterARestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	first := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	event := map[string]any{
		"id": "r1", "type": "instance.heartbeat", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev-1:3284", "server_id": "s1", "url": "http://127.0.0.1:1", "agent_type": "claude"},
	}
	signedAt := time.Now()
	require.Equal(t, http.StatusNoContent, postEvent(t, first, event, hubSecret, signedAt))
	require.Equal(t, http.StatusNoContent, request(t, http.MethodDelete, first+"/api/instances/dev-1:3284", hubToken, nil, nil))
	flushHub(t, first)
	_, err := os.Stat(strings.TrimSuffix(statePath, ".json") + ".deliveries.json")
	require.NoError(t, err)

	again := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	require.Equal(t, http.StatusNoContent, postEvent(t, again, event, hubSecret, signedAt))
	assert.Empty(t, instances(t, again), "the copy is refused")
	event["id"] = "r2"
	require.Equal(t, http.StatusNoContent, postEvent(t, again, event, hubSecret, time.Now()))
	assert.Len(t, instances(t, again), 1, "a new event counts")
}
