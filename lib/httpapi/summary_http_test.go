package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
)

// A client that watches many AgentAPIs follows each through
// /events?only=summary: the summary and its changes, and nothing else.
func TestEvents_OnlySummary(t *testing.T) {
	n := newNativeServer(t, nil)
	var summary httpapi.SummaryBody
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/summary", nil, &summary))
	assert.Equal(t, httpapi.AttentionNone, summary.Attention)
	assert.NotEmpty(t, summary.Instance.ID)
	assert.NotEmpty(t, summary.Instance.ServerID)

	events := openEvents(t, n.url+"/events?only=summary")
	first := nextEvent(t, events)
	require.Equal(t, "summary_update", first.event)

	n.factory.Last().Sink().RequestOpened(approval("perm-1"))
	for {
		event := nextEvent(t, events)
		require.Equal(t, "summary_update", event.event)
		var body httpapi.SummaryUpdateBody
		require.NoError(t, json.Unmarshal([]byte(event.data), &body))
		if body.Attention == httpapi.AttentionRequest {
			assert.Equal(t, 1, body.PendingRequests)
			break
		}
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/summary", nil, &summary))
	assert.Equal(t, httpapi.AttentionRequest, summary.Attention)

	// Other clients don't get it.
	assert.NotContains(t, eventNames(t, n.url+"/events?sync=1"), "summary_update")
	assert.NotContains(t, eventNames(t, n.url+"/events"), "summary_update")
}

// PUT /webhook changes the instance's ID: summary clients hear of it.
func TestEvents_SummaryInstanceChange(t *testing.T) {
	n := newNativeServer(t, nil)
	events := openEvents(t, n.url+"/events?only=summary")
	require.Equal(t, "summary_update", nextEvent(t, events).event)
	id := "build-box"
	require.Equal(t, http.StatusOK, n.do(http.MethodPut, "/webhook", map[string]any{"url": "", "timeout_seconds": 10, "max_attempts": 3, "instance_id": id}, nil))
	for {
		var body httpapi.SummaryUpdateBody
		require.NoError(t, json.Unmarshal([]byte(nextEvent(t, events).data), &body))
		if body.Instance.ID == id {
			break
		}
	}
}
