package pilog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/agentlog"
)

// A message that failed or was aborted may have used nothing; any other
// assistant message or usage entry without a model, usage or tokens is
// drift.
func TestCallsCountDrift(t *testing.T) {
	t.Parallel()
	drift := agentlog.NewDrift(Format)
	c := NewCalls(drift)
	for _, line := range []string{
		`{"type":"message","id":"a1","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","stopReason":"stop","usage":{"input":1000,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}}}}`,
		`{"type":"message","id":"a2","timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","stopReason":"error","errorMessage":"overloaded","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}`,
		`{"type":"message","id":"a3","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","stopReason":"aborted","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}`,
		// Renamed fields.
		`{"type":"message","id":"a4","timestamp":"2026-10-20T10:00:03.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","stopReason":"stop","usage":{"inputTokens":1000,"outputTokens":10}}}`,
		`{"type":"message","id":"a5","timestamp":"2026-10-20T10:00:04.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","stopReason":"stop","tokens":{"input":1000,"output":10}}}`,
		`{"type":"usage","id":"w1","timestamp":"2026-10-20T10:04:30.000Z","kind":"cache_warm","modelId":"claude-opus-5-5","usage":{"input":1,"output":1,"cacheRead":20000}}`,
		`{"type":"message","id":"u1","timestamp":"2026-10-20T10:05:00.000Z","message":{"role":"user","content":[{"type":"text","text":"what did the assistant say?"}]}}`,
	} {
		c.Line([]byte(line))
	}
	from, calls := c.Changed()
	assert.Zero(t, from)
	assert.Len(t, calls, 1)
	assert.InDelta(t, 0.01, calls[0].Cost, 1e-12)
	assert.Equal(t, 1, drift.Calls(agentlog.NoTokens))
	assert.Equal(t, 2, drift.Calls(agentlog.NoModelOrUsage))
}
