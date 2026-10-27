package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
