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

// activityAnswer is GET /api/activity as the page reads it.
type activityAnswer struct {
	Days []struct {
		Date        string                    `json:"date"`
		WorkSeconds int64                     `json:"work_seconds"`
		USD         float64                   `json:"usd"`
		Tokens      instanceapi.SummaryTokens `json:"tokens"`
	} `json:"days"`
	Since    string `json:"since"`
	TimeZone string `json:"time_zone"`
}

// GET /api/activity: the days in the time zone asked for, with the spend
// of the hours the instances' summaries list; a time zone or a count of
// days it doesn't take is 400, in the hub's error format.
func TestHubActivity(t *testing.T) {
	now := time.Now().UTC()
	instance := startFakeAgentAPI(t, "dev-1:3284", "run-1", "4.2.0")
	instance.spend = &instanceapi.SummarySpend{
		DayUSD: 1.5, WeekUSD: 1.5, Scope: "s1", UpdatedAt: now.Truncate(time.Second),
		Hours: []instanceapi.SummaryHour{{Hour: now.Truncate(time.Hour), USD: 1.5, Tokens: instanceapi.SummaryTokens{Output: 7, Total: 7}}},
	}
	hubSrv := startHubWith(t, following(""), true)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", hubToken, map[string]string{"url": instance.URL}, nil))

	var body activityAnswer
	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/api/activity", hubToken, nil, &body))
	assert.Equal(t, "UTC", body.TimeZone)
	require.Len(t, body.Days, 371)
	today := body.Days[len(body.Days)-1]
	assert.Equal(t, now.Format(time.DateOnly), today.Date)
	assert.InDelta(t, 1.5, today.USD, 1e-9)
	assert.Equal(t, 7, today.Tokens.Total)
	assert.Equal(t, now.Format(time.DateOnly), body.Since)

	require.Equal(t, http.StatusOK, request(t, http.MethodGet, hubSrv.URL+"/api/activity?tz=Asia/Taipei&days=7", hubToken, nil, &body))
	assert.Equal(t, "Asia/Taipei", body.TimeZone)
	assert.Len(t, body.Days, 7)

	for _, query := range []string{"?tz=Mars/Olympus", "?tz=Local", "?days=0", "?days=372", "?days=x"} {
		resp, err := http.Get(hubSrv.URL + "/api/activity" + query)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "signed in only")

		req, err := http.NewRequest(http.MethodGet, hubSrv.URL+"/api/activity"+query, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+hubToken)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		var refusal map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&refusal))
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
		assert.Equal(t, "bad_request", refusal["code"], query)
		assert.Equal(t, refusal["message"], refusal["error"], query)
	}
}
