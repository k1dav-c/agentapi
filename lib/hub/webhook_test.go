package hub

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

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
// another URL is refused, logged once per run, also when it says the
// instance stopped. One at its URL counts.
func TestAddedInstanceKeepsItsURL(t *testing.T) {
	var logs bytes.Buffer
	h, err := New(Config{Token: "t", Secret: "s", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	_, _, err = h.store.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.ServerID = "s1"
		i.Source = SourceManual
		i.Token = "its-own"
		i.Connection = ConnectionUnreachable
	})
	require.NoError(t, err)
	get := func() Instance {
		instance, ok := h.store.get("dev:1")
		require.True(t, ok)
		return instance
	}

	for range 3 {
		h.upsertFromWebhook(instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s2"}, "http://elsewhere:1", 60)
	}
	h.stopped("dev:1", "s1", "http://elsewhere:1")
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
	h.stopped("dev:1", "s4", "http://dev:1")
	assert.Equal(t, ConnectionStopped, get().Connection)
}

// An instance that moves leaves behind the token given for its old URL;
// the hub's --instance-token still applies.
func TestMovedInstanceLeavesItsToken(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s", InstanceToken: "everyone's"})
	require.NoError(t, err)
	_, _, err = h.store.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.ServerID = "s1"
		i.Source = SourceWebhook
		i.Token = "for-dev-1"
		i.Connection = ConnectionUnreachable
	})
	require.NoError(t, err)
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
