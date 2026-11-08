package hub_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// The hub passes an instance's spend on as its summary has it, the tokens
// included, to the list and the browser's stream; one it no longer reaches
// keeps its last.
func TestHubSpendTokens(t *testing.T) {
	instance := startFakeAgentAPI(t, "dev-1:3284", "run-1", "2.4.0")
	instance.spend = &instanceapi.SummarySpend{
		DayUSD: 1.5, WeekUSD: 9, Scope: "s1", UpdatedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		DayTokens:  &instanceapi.SummaryTokens{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4, Total: 10},
		WeekTokens: &instanceapi.SummaryTokens{Input: 10, Output: 20, CacheWrite: 30, CacheRead: 40, Total: 100},
	}
	hubSrv := startHub(t, "")
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": instance.URL}, nil))
	require.Eventually(t, func() bool {
		list := instances(t, hubSrv.URL)
		return len(list) == 1 && list[0].Connection == instanceapi.ConnectionConnected && list[0].Summary != nil
	}, 10*time.Second, 20*time.Millisecond)
	spend := instances(t, hubSrv.URL)[0].Summary.Spend
	require.NotNil(t, spend)
	assert.True(t, spend.Equal(*instance.spend), "%+v", spend)

	first := next(t, openEvents(t, hubSrv.URL+"/api/events"))
	require.Equal(t, "instances", first.event)
	var snapshot struct {
		Instances []struct {
			Summary struct {
				Spend map[string]json.RawMessage `json:"spend"`
			} `json:"summary"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal([]byte(first.data), &snapshot))
	require.Len(t, snapshot.Instances, 1)
	assert.JSONEq(t, `{"input":1,"output":2,"cache_write":3,"cache_read":4,"total":10}`, string(snapshot.Instances[0].Summary.Spend["day_tokens"]))
	assert.JSONEq(t, `{"input":10,"output":20,"cache_write":30,"cache_read":40,"total":100}`, string(snapshot.Instances[0].Summary.Spend["week_tokens"]))

	// Gone: the last spend stays, tokens and all.
	_ = instance.Listener.Close()
	instance.CloseClientConnections()
	require.Eventually(t, func() bool {
		return instances(t, hubSrv.URL)[0].Connection == instanceapi.ConnectionUnreachable
	}, 10*time.Second, 20*time.Millisecond)
	last := instances(t, hubSrv.URL)[0].Summary
	require.NotNil(t, last)
	require.NotNil(t, last.Spend)
	assert.True(t, last.Spend.Equal(*instance.spend))
}
