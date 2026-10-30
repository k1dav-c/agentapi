package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// Muting an instance silences what it waits for now, until that changes:
// a login, until it logs in or asks something else; a request, until
// another request comes.
func TestMute(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	set := func(summary instanceapi.SummaryBody) {
		_, _, err := h.store.update("dev:1", true, func(i *Instance) {
			i.URL = "http://dev:1"
			i.Summary = &summary
		})
		require.NoError(t, err)
	}
	patch := func(body string) (int, Instance) {
		req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/instances/dev:1", bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer t")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		var instance Instance
		_ = json.NewDecoder(resp.Body).Decode(&instance)
		return resp.StatusCode, instance
	}
	muted := func() string {
		instance, ok := h.store.get("dev:1")
		require.True(t, ok)
		return instance.Muted
	}

	set(instanceapi.SummaryBody{Attention: instanceapi.AttentionNone})
	code, _ := patch(`{"muted": true}`)
	assert.Equal(t, http.StatusConflict, code, "nothing to mute")

	login := instanceapi.SummaryBody{Attention: instanceapi.AttentionLogin}
	set(login)
	code, instance := patch(`{"muted": true}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "login", instance.Muted)
	h.unmuteChanged("dev:1", &login)
	assert.Equal(t, "login", muted(), "still waits for the login")
	h.unmuteChanged("dev:1", &instanceapi.SummaryBody{Attention: instanceapi.AttentionNone})
	assert.Empty(t, muted(), "logged in")

	first := instanceapi.SummaryBody{Attention: instanceapi.AttentionRequest, Request: &instanceapi.SummaryRequest{ID: "r1"}}
	set(first)
	_, instance = patch(`{"muted": true}`)
	assert.Equal(t, "request:r1", instance.Muted)
	h.unmuteChanged("dev:1", &first)
	assert.Equal(t, "request:r1", muted())
	h.unmuteChanged("dev:1", &instanceapi.SummaryBody{Attention: instanceapi.AttentionRequest, Request: &instanceapi.SummaryRequest{ID: "r2"}})
	assert.Empty(t, muted(), "another request")

	set(login)
	_, _ = patch(`{"muted": true}`)
	code, instance = patch(`{"muted": false}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, instance.Muted, "unmuted by hand")
}

// A summary that comes after the instance stopped, from a URL it left, or
// from a watch that ended, is from a watch on its way out: it changes
// nothing.
func TestSummaryFromAnEndingWatch(t *testing.T) {
	h, err := New(Config{Token: "t", Secret: "s"})
	require.NoError(t, err)
	_, _, err = h.store.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.Connection = ConnectionStopped
	})
	require.NoError(t, err)
	summary := instanceapi.SummaryBody{Instance: instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s1"}}
	connection := func() Connection {
		instance, _ := h.store.get("dev:1")
		return instance.Connection
	}
	assert.False(t, h.applySummary(t.Context(), "dev:1", "http://dev:1", summary))
	assert.Equal(t, ConnectionStopped, connection())

	_, _, _ = h.store.update("dev:1", false, func(i *Instance) {
		i.URL = "http://dev:2"
		i.Connection = ConnectionConnecting
	})
	assert.False(t, h.applySummary(t.Context(), "dev:1", "http://dev:1", summary), "a URL it left")
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	assert.False(t, h.applySummary(ended, "dev:1", "http://dev:2", summary), "a watch that ended")
	assert.Equal(t, ConnectionConnecting, connection())

	assert.True(t, h.applySummary(t.Context(), "dev:1", "http://dev:2", summary))
	instance, _ := h.store.get("dev:1")
	assert.Equal(t, ConnectionConnected, instance.Connection)
	assert.Equal(t, "http://dev:2", instance.URL)
	assert.Equal(t, "s1", instance.ServerID)
}

// Browsers hear of a change they would see, not of a heartbeat that
// changes nothing on the page; when an instance was last heard of shows
// only while it is offline.
func TestStoreTellsWhatBrowsersSee(t *testing.T) {
	s, err := newStore("")
	require.NoError(t, err)
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	_, _, err = s.update("dev:1", true, func(i *Instance) {
		i.URL = "http://dev:1"
		i.Connection = ConnectionConnected
		i.Summary = &instanceapi.SummaryBody{Status: instanceapi.AgentStatusStable, Attention: instanceapi.AttentionNone}
		i.LastSeen = now
	})
	require.NoError(t, err)
	_, events := s.subscribe()
	heard := func() bool {
		select {
		case <-events:
			return true
		default:
			return false
		}
	}
	_, _, _ = s.update("dev:1", false, func(i *Instance) { i.LastSeen = now.Add(time.Minute) })
	_, _, _ = s.update("dev:1", false, func(i *Instance) { i.refusedRun = "twin" })
	assert.False(t, heard(), "a heartbeat")
	_, _, _ = s.update("dev:1", false, func(i *Instance) { i.Group = "Work" })
	assert.True(t, heard())

	_, _, _ = s.update("dev:1", false, func(i *Instance) { i.Connection = ConnectionUnreachable })
	assert.True(t, heard())
	_, _, _ = s.update("dev:1", false, func(i *Instance) { i.LastSeen = now.Add(2 * time.Minute) })
	assert.True(t, heard(), "offline since then")
}
