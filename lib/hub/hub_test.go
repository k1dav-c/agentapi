package hub_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
)

const (
	hubToken      = "hub-token"
	hubSecret     = "hub-secret"
	instanceToken = "instance-token"
)

// startHub runs a hub keeping its instances in statePath.
// startHub runs a hub that calls an instance unreachable soon after it
// fails.
func startHub(t *testing.T, statePath string) *httptest.Server {
	t.Helper()
	return startHubGrace(t, statePath, 200*time.Millisecond)
}

func startHubGrace(t *testing.T, statePath string, grace time.Duration) *httptest.Server {
	t.Helper()
	h, err := hub.New(hub.Config{Token: hubToken, Secret: hubSecret, InstanceToken: instanceToken, StatePath: statePath, ConnectGrace: grace})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-done
	})
	return srv
}

// startInstance runs an AgentAPI with a fake native agent that needs
// instanceToken.
func startInstance(t *testing.T) (*httptest.Server, *httpapi.Server) {
	t.Helper()
	return startInstanceOn(t, nil)
}

// startInstanceOn runs it on listener; nil is a new one.
func startInstanceOn(t *testing.T, listener net.Listener) (*httptest.Server, *httpapi.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		Native:         httpapi.NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            "/work/api",
		APIToken:       instanceToken,
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
	hubSrv := startHub(t, statePath)

	// What agentapi hub join does to a running instance.
	webhook := map[string]any{
		"url": hubSrv.URL + "/hooks", "timeout_seconds": 5, "max_attempts": 3,
		"events": []string{"instance.*", "task.*"}, "secret": hubSecret, "public_url": instSrv.URL,
	}
	require.Equal(t, http.StatusOK, request(t, http.MethodPut, instSrv.URL+"/webhook", instanceToken, webhook, nil))

	var joined hub.Instance
	require.Eventually(t, func() bool {
		list := instances(t, hubSrv.URL)
		if len(list) == 1 && list[0].Connection == hub.ConnectionConnected {
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

	// The browser reaches the instance through the hub, which sends the
	// instance's token, not the browser's credentials.
	id := joined.ID
	var status httpapi.StatusChangeBody
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", hubToken, nil, &status))
	assert.Equal(t, joined.ServerID, status.ServerID)
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", "", nil, nil), "not signed in")
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodGet, hubSrv.URL+"/i/"+id+"/status", instanceToken, nil, nil), "the instance's token isn't the hub's")
	assert.Equal(t, http.StatusNotFound, request(t, http.MethodGet, hubSrv.URL+"/i/nope/status", hubToken, nil, nil))
	// As a browser escapes it (encodeURIComponent).
	escaped := strings.ReplaceAll(url.PathEscape(id), ":", "%3A")
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/i/"+escaped+"/status", hubToken, nil, nil))

	// The file keeps the instance, readable by its owner only.
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
			break
		}
	}

	// A hub that starts again knows the instance.
	again := startHub(t, statePath)
	require.Eventually(t, func() bool {
		list := instances(t, again.URL)
		return len(list) == 1 && list[0].Connection == hub.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)

	// Forgetting it.
	require.Equal(t, http.StatusNoContent, request(t, http.MethodDelete, again.URL+"/api/instances/"+id, hubToken, nil, nil))
	assert.Empty(t, instances(t, again.URL))
}

// sign signs body as an AgentAPI does at time at.
func sign(body []byte, secret string, at time.Time) (string, string) {
	timestamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return timestamp, "sha256=" + hex.EncodeToString(mac.Sum(nil))
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
	hubSrv := startHub(t, "")
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
		return instances(t, hubSrv.URL)[0].Connection == hub.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond, "nothing listens there")

	// The same delivery again changes nothing; instance.stopped stops it.
	stopped := event("e3", "instance.stopped")
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	assert.NotEqual(t, hub.ConnectionStopped, instances(t, hubSrv.URL)[0].Connection, "a replayed delivery")
	assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, event("e4", "instance.stopped"), hubSecret, time.Now()))
	assert.Equal(t, hub.ConnectionStopped, instances(t, hubSrv.URL)[0].Connection)
}

// An instance that isn't listening yet when its first event comes (it is
// starting) is connecting, not unreachable, until it does; one that never
// does is unreachable once the connect grace is over.
func TestHubConnectGrace(t *testing.T) {
	hubSrv := startHubGrace(t, "", 3*time.Second)
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
		assert.Equal(t, hub.ConnectionConnecting, instances(t, hubSrv.URL)[0].Connection)
		time.Sleep(50 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	startInstanceOn(t, listener)
	// Stopped before the instance closes (cleanups run last first): its
	// server waits for the hub's stream to end. No server ID: the hub took
	// the instance's own from its summary.
	t.Cleanup(func() {
		stopped := map[string]any{"id": "e3", "type": "instance.stopped", "created_at": time.Now(), "instance": map[string]any{"id": "dev-1:3284", "url": "http://" + addr}}
		assert.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, stopped, hubSecret, time.Now()))
	})
	require.Eventually(t, func() bool {
		connection := instances(t, hubSrv.URL)[0].Connection
		assert.NotEqual(t, hub.ConnectionUnreachable, connection)
		return connection == hub.ConnectionConnected
	}, 10*time.Second, 50*time.Millisecond)

	// One that never listens is unreachable after the grace.
	other := map[string]any{
		"id": "e2", "type": "instance.started", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev-2:3284", "server_id": "s2", "url": "http://127.0.0.1:1", "agent_type": "codex"},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubSrv.URL, other, hubSecret, time.Now()))
	connection := func() hub.Connection {
		for _, instance := range instances(t, hubSrv.URL) {
			if instance.ID == "dev-2:3284" {
				return instance.Connection
			}
		}
		return ""
	}
	time.Sleep(time.Second)
	assert.Equal(t, hub.ConnectionConnecting, connection(), "within the grace")
	require.Eventually(t, func() bool { return connection() == hub.ConnectionUnreachable }, 20*time.Second, 100*time.Millisecond)
}

// An instance added by its URL, with its own token.
func TestHubAddInstance(t *testing.T) {
	instSrv, _ := startInstance(t)
	hubSrv := startHub(t, "")
	assert.Equal(t, http.StatusBadRequest, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": "ftp://x"}, nil))
	var added hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": instSrv.URL + "/", "token": instanceToken}, &added))
	assert.Equal(t, hub.SourceManual, added.Source)
	assert.Equal(t, instSrv.URL, added.URL)
	assert.Empty(t, added.Token)
	require.Eventually(t, func() bool {
		list := instances(t, hubSrv.URL)
		return len(list) == 1 && list[0].Connection == hub.ConnectionConnected
	}, 10*time.Second, 20*time.Millisecond)
}

// Signing in trades the hub token for a cookie, which the hub's pages and
// the proxy accept.
func TestHubSession(t *testing.T) {
	hubSrv := startHub(t, "")
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
	hubSrv := startHub(t, statePath)
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
	again := startHub(t, statePath)
	assert.Equal(t, "Client work", instances(t, again.URL)[0].Group)
	var ungrouped hub.Instance
	require.Equal(t, http.StatusOK, request(t, http.MethodPatch, again.URL+"/api/instances/"+url.PathEscape("dev-1:3284"), hubToken, map[string]string{"group": ""}, &ungrouped))
	assert.Empty(t, ungrouped.Group)
}
