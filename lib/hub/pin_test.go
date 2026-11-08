package hub_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
)

// heartbeat has the instance id join the hub, as its webhook does; delivery
// is the event's ID.
func heartbeat(t *testing.T, hubURL, id, delivery string) {
	t.Helper()
	event := map[string]any{
		"id": delivery, "type": "instance.heartbeat", "created_at": time.Now(),
		"instance": map[string]any{"id": id, "server_id": "s-" + id, "url": "http://127.0.0.1:1", "agent_type": "claude"},
	}
	require.Equal(t, http.StatusNoContent, postEvent(t, hubURL, event, hubSecret, time.Now()))
}

// pins is each instance's pin, by ID.
func pins(t *testing.T, hubURL string) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, instance := range instances(t, hubURL) {
		got[instance.ID] = instance.Pin
	}
	return got
}

func instancePath(hubURL, id string) string {
	return hubURL + "/api/instances/" + url.PathEscape(id)
}

// putPins orders the pinned instances, signed in, and returns the status
// and the answer's IDs.
func putPins(t *testing.T, hubURL string, body any) (int, []string) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, hubURL+"/api/pins", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+hubToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var answer struct {
		IDs []string `json:"ids"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	return resp.StatusCode, answer.IDs
}

// Pinning puts an instance below the pinned ones, and PUT /api/pins orders
// them. Pins stay through the instances' own events and a restart of the
// hub; unpinning or forgetting one closes its gap. None of it is a change
// of what the instances do (changed_at).
func TestHubPins(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	hubURL := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	for n, id := range []string{"dev-1:3284", "dev-2:3284", "dev-3:3284"} {
		heartbeat(t, hubURL, id, fmt.Sprintf("p%d", n))
	}
	changedAt := map[string]time.Time{}
	for _, instance := range instances(t, hubURL) {
		changedAt[instance.ID] = instance.ChangedAt
	}
	pin := func(hubURL, id string, pinned bool) hub.Instance {
		t.Helper()
		var changed hub.Instance
		require.Equal(t, http.StatusOK, request(t, http.MethodPatch, instancePath(hubURL, id), hubToken, map[string]bool{"pinned": pinned}, &changed))
		return changed
	}

	assert.Equal(t, 1, pin(hubURL, "dev-2:3284", true).Pin)
	assert.Equal(t, 2, pin(hubURL, "dev-1:3284", true).Pin, "below the pinned ones")
	assert.Equal(t, 2, pin(hubURL, "dev-1:3284", true).Pin, "pinned already: it stays")
	assert.Equal(t, map[string]int{"dev-1:3284": 2, "dev-2:3284": 1, "dev-3:3284": 0}, pins(t, hubURL))

	code, order := putPins(t, hubURL, map[string][]string{"ids": {"dev-1:3284", "dev-2:3284"}})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"dev-1:3284", "dev-2:3284"}, order)
	reordered := map[string]int{"dev-1:3284": 1, "dev-2:3284": 2, "dev-3:3284": 0}
	assert.Equal(t, reordered, pins(t, hubURL))

	// The list must be the pinned ones, each once: anything else changes
	// nothing.
	for name, ids := range map[string][]string{
		"one left out":   {"dev-1:3284"},
		"one not pinned": {"dev-1:3284", "dev-2:3284", "dev-3:3284"},
		"none":           {},
	} {
		code, order := putPins(t, hubURL, map[string][]string{"ids": ids})
		assert.Equal(t, http.StatusConflict, code, name)
		assert.Equal(t, []string{"dev-1:3284", "dev-2:3284"}, order, "%s: the pinned ones, in their order", name)
	}
	code, _ = putPins(t, hubURL, map[string][]string{"ids": {"dev-2:3284", "dev-1:3284", "nope"}})
	assert.Equal(t, http.StatusNotFound, code, "an instance the hub doesn't have")
	code, _ = putPins(t, hubURL, map[string][]string{"ids": {"dev-2:3284", "dev-2:3284", "dev-1:3284"}})
	assert.Equal(t, http.StatusBadRequest, code, "listed twice")
	code, _ = putPins(t, hubURL, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, code, "no ids")
	assert.Equal(t, http.StatusNotFound, request(t, http.MethodPatch, instancePath(hubURL, "nope"), hubToken, map[string]bool{"pinned": true}, nil))
	assert.Equal(t, reordered, pins(t, hubURL))

	for _, instance := range instances(t, hubURL) {
		assert.True(t, instance.ChangedAt.Equal(changedAt[instance.ID]), "%s: not a change of what it does", instance.ID)
	}

	// The instance's events don't touch it; a hub that starts again knows it.
	heartbeat(t, hubURL, "dev-1:3284", "p9")
	assert.Equal(t, reordered, pins(t, hubURL))
	flushHub(t, hubURL)
	again := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	assert.Equal(t, reordered, pins(t, again))

	assert.Equal(t, 3, pin(again, "dev-3:3284", true).Pin)
	assert.Zero(t, pin(again, "dev-1:3284", false).Pin)
	assert.Equal(t, map[string]int{"dev-1:3284": 0, "dev-2:3284": 1, "dev-3:3284": 2}, pins(t, again), "the gap closed")
	require.Equal(t, http.StatusNoContent, request(t, http.MethodDelete, instancePath(again, "dev-2:3284"), hubToken, nil, nil))
	assert.Equal(t, map[string]int{"dev-1:3284": 0, "dev-3:3284": 1}, pins(t, again))
	heartbeat(t, again, "dev-2:3284", "p10")
	assert.Equal(t, map[string]int{"dev-1:3284": 0, "dev-2:3284": 0, "dev-3:3284": 1}, pins(t, again), "forgotten, it comes back unpinned")
}

// The browsers' stream has each instance's pin, and an event for each
// instance a pin, an unpin or an order moves; none for an order that moves
// none.
func TestHubPinEvents(t *testing.T) {
	hubURL := startHubWith(t, hub.Config{}, false).URL
	ids := []string{"dev-1:3284", "dev-2:3284", "dev-3:3284"}
	for n, id := range ids {
		heartbeat(t, hubURL, id, fmt.Sprintf("e%d", n))
		require.Equal(t, http.StatusOK, request(t, http.MethodPatch, instancePath(hubURL, id), hubToken, map[string]bool{"pinned": true}, nil))
	}
	events := openEvents(t, hubURL+"/api/events")
	first := next(t, events)
	require.Equal(t, "instances", first.event)
	assert.Contains(t, first.data, `"pin":3`)
	// heard reads the stream's instance events until it heard of n
	// instances, and returns their pins by ID.
	heard := func(n int) map[string]int {
		t.Helper()
		pins := map[string]int{}
		for len(pins) < n {
			e := next(t, events)
			if e.event != "instance" {
				continue
			}
			var changed hub.Instance
			require.NoError(t, json.Unmarshal([]byte(e.data), &changed))
			pins[changed.ID] = changed.Pin
		}
		return pins
	}

	code, _ := putPins(t, hubURL, map[string][]string{"ids": {"dev-3:3284", "dev-1:3284", "dev-2:3284"}})
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, map[string]int{"dev-1:3284": 2, "dev-2:3284": 3, "dev-3:3284": 1}, heard(3))

	require.Equal(t, http.StatusOK, request(t, http.MethodPatch, instancePath(hubURL, "dev-3:3284"), hubToken, map[string]bool{"pinned": false}, nil))
	assert.Equal(t, map[string]int{"dev-1:3284": 1, "dev-2:3284": 2, "dev-3:3284": 0}, heard(3))

	code, _ = putPins(t, hubURL, map[string][]string{"ids": {"dev-1:3284", "dev-2:3284"}})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, http.StatusOK, request(t, http.MethodPatch, instancePath(hubURL, "dev-3:3284"), hubToken, map[string]string{"group": "Work"}, nil))
	assert.Equal(t, map[string]int{"dev-3:3284": 0}, heard(1), "the same order again moved nothing")
}

// fromPage sends method target with body as a browser does from a page of
// origin: Sec-Fetch-Site site and Origin origin.
func fromPage(t *testing.T, method, target, site, origin string, body any) int {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(method, target, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", site)
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Only a signed-in browser changes pins, and only from the hub's own page:
// another site's page can't, even on a hub without a token.
func TestHubPinsRefused(t *testing.T) {
	hubURL := startHubWith(t, hub.Config{}, false).URL
	heartbeat(t, hubURL, "dev-1:3284", "r1")
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPatch, instancePath(hubURL, "dev-1:3284"), "", map[string]bool{"pinned": true}, nil))
	assert.Equal(t, http.StatusUnauthorized, request(t, http.MethodPut, hubURL+"/api/pins", "", map[string][]string{"ids": {}}, nil))
	assert.Equal(t, map[string]int{"dev-1:3284": 0}, pins(t, hubURL))

	open := startHubWith(t, hub.Config{NoToken: true}, false).URL
	for n, id := range []string{"dev-1:3284", "dev-2:3284"} {
		heartbeat(t, open, id, fmt.Sprintf("o%d", n))
		assert.Equal(t, http.StatusForbidden, fromPage(t, http.MethodPatch, instancePath(open, id), "cross-site", "https://evil.example", map[string]bool{"pinned": true}))
		require.Equal(t, http.StatusOK, fromPage(t, http.MethodPatch, instancePath(open, id), "same-origin", open, map[string]bool{"pinned": true}))
	}
	reversed := map[string][]string{"ids": {"dev-2:3284", "dev-1:3284"}}
	assert.Equal(t, http.StatusForbidden, fromPage(t, http.MethodPut, open+"/api/pins", "cross-site", "https://evil.example", reversed))
	assert.Equal(t, http.StatusForbidden, fromPage(t, http.MethodPut, open+"/api/pins", "", "https://evil.example", reversed))
	assert.Equal(t, http.StatusForbidden, fromPage(t, http.MethodPatch, instancePath(open, "dev-1:3284"), "cross-site", "https://evil.example", map[string]bool{"pinned": false}))
	assert.Equal(t, map[string]int{"dev-1:3284": 1, "dev-2:3284": 2}, pins(t, open))
	assert.Equal(t, http.StatusOK, fromPage(t, http.MethodPut, open+"/api/pins", "same-origin", open, reversed))
	assert.Equal(t, map[string]int{"dev-1:3284": 2, "dev-2:3284": 1}, pins(t, open))
}

// A file edited by hand, with gaps and twins among the pins, is numbered
// again: by pin, then by ID.
func TestHubPinsFromAnEditedFile(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "instances.json")
	stored := `[
		{"id": "a", "url": "http://a", "source": "manual", "last_seen": "2026-11-01T09:00:00Z", "pin": 7},
		{"id": "b", "url": "http://b", "source": "manual", "last_seen": "2026-11-01T09:00:00Z", "pin": 3},
		{"id": "c", "url": "http://c", "source": "manual", "last_seen": "2026-11-01T09:00:00Z", "pin": 3},
		{"id": "d", "url": "http://d", "source": "manual", "last_seen": "2026-11-01T09:00:00Z", "pin": -2},
		{"id": "e", "url": "http://e", "source": "manual", "last_seen": "2026-11-01T09:00:00Z"}
	]`
	require.NoError(t, os.WriteFile(statePath, []byte(stored), 0o600))
	hubURL := startHubWith(t, hub.Config{StatePath: statePath}, false).URL
	assert.Equal(t, map[string]int{"a": 3, "b": 1, "c": 2, "d": 0, "e": 0}, pins(t, hubURL))
}
