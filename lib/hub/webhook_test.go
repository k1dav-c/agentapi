package hub

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// An event of a run the hub knows is of that run; an unknown current run
// (the hub's first sighting) is no conflict, but an event that names no
// run is of none.
func TestSameRun(t *testing.T) {
	assert.True(t, sameRun("s1", "s1"))
	assert.True(t, sameRun("", "s1"), "the hub's first sighting")
	assert.False(t, sameRun("s1", "s2"))
	assert.False(t, sameRun("s1", ""), "an event that names no run")
}

// An instance added by hand keeps its URL and token: an event that names
// another URL is refused, logged once per run. One at its URL updates its
// run, but instance.stopped never stops it: the hub watches it directly,
// and nothing would follow it again.
func TestAddedInstanceKeepsItsURL(t *testing.T) {
	var logs bytes.Buffer
	h, err := New(Config{Token: "t", Secret: "s", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	_, _ = h.store.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.ServerID = "s1"
		i.Source = SourceManual
		i.Token = "its-own"
		i.Connection = ConnectionUnreachable
	})
	get := func() Instance {
		instance, ok := h.store.get("dev:1")
		require.True(t, ok)
		return instance
	}

	for range 3 {
		h.upsertFromWebhook(instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s2"}, "http://elsewhere:1", 60)
	}
	h.stopped("dev:1", "s1")
	instance := get()
	assert.Equal(t, "http://dev:1", instance.URL)
	assert.Equal(t, "its-own", instance.Token)
	assert.Equal(t, "s1", instance.ServerID)
	assert.Equal(t, ConnectionUnreachable, instance.Connection)
	assert.Equal(t, 1, strings.Count(logs.String(), "added by hand"), "once per run")
	h.upsertFromWebhook(instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s3"}, "http://elsewhere:1", 60)
	assert.Equal(t, 2, strings.Count(logs.String(), "added by hand"), "another run")

	h.upsertFromWebhook(instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s4"}, "http://dev:1", 60)
	instance = get()
	assert.Equal(t, "s4", instance.ServerID)
	assert.Equal(t, 60, instance.HeartbeatSeconds)
	assert.Equal(t, "its-own", instance.Token)
	assert.Equal(t, SourceManual, instance.Source)
	h.stopped("dev:1", "s4")
	assert.NotEqual(t, ConnectionStopped, get().Connection, "a forged instance.stopped would stop it for good")
}

// An instance that moves leaves behind the token given for its old URL;
// the hub's --instance-token still applies.
func TestMovedInstanceLeavesItsToken(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s", InstanceToken: "everyone's"})
	require.NoError(t, err)
	_, _ = h.store.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.ServerID = "s1"
		i.Source = SourceWebhook
		i.Token = "for-dev-1"
		i.Connection = ConnectionUnreachable
	})
	h.upsertFromWebhook(instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s2"}, "http://dev:2", 60)
	instance, _ := h.store.get("dev:1")
	assert.Equal(t, "http://dev:2", instance.URL)
	assert.Empty(t, instance.Token)
	assert.Equal(t, "everyone's", h.tokenFor(instance))
}

// The hub's own client gives up connecting to an instance that doesn't
// answer, rather than waiting for the system's TCP timeout, but sets no
// limit on a response's body: the summary streams stay open.
func TestDefaultClientTimeouts(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	assert.Zero(t, h.client.Timeout, "the summary streams stay open")
	transport, ok := h.client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.NotNil(t, transport.DialContext, "a dialer with a timeout")
	assert.NotNil(t, transport.Proxy)
	assert.Positive(t, transport.TLSHandshakeTimeout)
	assert.Positive(t, transport.IdleConnTimeout)
	assert.Positive(t, transport.MaxIdleConns)
	assert.Equal(t, 15*time.Second, transport.ResponseHeaderTimeout)
}

// Events refused for their signature are logged at most once a minute,
// with how many were refused in between.
func TestBadSignaturesLogOnceAMinute(t *testing.T) {
	var logs bytes.Buffer
	clock := quartz.NewMock(t)
	h, err := New(Config{Token: "t", Secret: "s", Clock: clock, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	post := func() {
		req := httptest.NewRequest(http.MethodPost, "/hooks", strings.NewReader("{}"))
		w := httptest.NewRecorder()
		h.receiveWebhook(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	for range 5 {
		post()
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "invalid or missing signature"))
	clock.Advance(time.Minute)
	post()
	assert.Equal(t, 2, strings.Count(logs.String(), "invalid or missing signature"))
	assert.Contains(t, logs.String(), "refused_since_last_logged=4")
}

// A sender that trickles in an event's body is cut off: it can't hold the
// connection and its handler.
func TestWebhookBodyReadTimeout(t *testing.T) {
	defer func(d time.Duration) { webhookReadTimeout = d }(webhookReadTimeout)
	webhookReadTimeout = 200 * time.Millisecond
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("POST /hooks HTTP/1.1\r\nHost: hub\r\nContent-Length: 100\r\n\r\n{"))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "the hub answers before the client's deadline")
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Less(t, time.Since(start), 4*time.Second)
}
