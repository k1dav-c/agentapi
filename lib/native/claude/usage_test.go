package claude

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

func TestRateLimits(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	_, sink := started(t, Config{}, proc)
	// As Claude Code 2.1.302 sends it.
	proc.send(t, []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1792258800,"rateLimitType":"five_hour",`+
		`"overageStatus":"rejected","isUsingOverage":false,`+
		`"unifiedWindows":{"seven_day":{"utilization":0.29,"resetsAt":1792598400},"five_hour":{"utilization":0.12,"resetsAt":1792258800}}}}`))
	// An older version: only the limit that applies, whose next window
	// resets later.
	proc.send(t, []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1792600000,"rateLimitType":"seven_day_opus"}}`))
	proc.send(t, []byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793200000,"rateLimitType":"seven_day_opus"}}`))
	proc.barrier(t)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	require.Len(t, sink.usage, 3)
	limits := sink.usage[0].RateLimits
	require.Len(t, limits, 2)
	assert.Equal(t, "five_hour", limits[0].ID, "sorted")
	assert.Equal(t, "5-hour limit", limits[0].Label)
	assert.InDelta(t, 12.0, *limits[0].UsedPercent, 0.001)
	assert.Equal(t, "allowed", limits[0].Status)
	assert.Equal(t, time.Unix(1792258800, 0).UTC(), *limits[0].ResetsAt)
	assert.Equal(t, "Weekly limit", limits[1].Label)
	assert.Empty(t, limits[1].Status)

	limits = sink.usage[1].RateLimits
	require.Len(t, limits, 3)
	assert.Equal(t, 29.0, *limits[1].UsedPercent, "rounded")
	assert.Equal(t, "Weekly Opus limit", limits[2].Label)
	assert.Equal(t, "allowed_warning", limits[2].Status)
	assert.Nil(t, limits[2].UsedPercent)
	assert.InDelta(t, 29.0, *limits[1].UsedPercent, 0.001, "the others stay")

	limits = sink.usage[2].RateLimits
	assert.Equal(t, "allowed", limits[2].Status)
	assert.Equal(t, time.Unix(1793200000, 0).UTC(), *limits[2].ResetsAt)
	assert.Equal(t, time.Unix(1792600000, 0).UTC(), *sink.usage[1].RateLimits[2].ResetsAt, "what was reported stays")
}

func TestContextUsageAfterATurn(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	proc.contextUsage = `{"totalTokens":41000,"maxTokens":200000,"percentage":20.5}`
	a, sink := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(context.Background(), native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1"}`))
	proc.send(t, []byte(`{"type":"result","subtype":"success","is_error":false,"result":"done"}`))
	require.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.usage) == 1
	}, 5*time.Second, time.Millisecond)
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, &native.ContextUsage{Used: 41000, Size: 200000}, sink.usage[0].Context)
}
