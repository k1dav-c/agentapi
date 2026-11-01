package claudelog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// A call is left out, and counted as drift, when its entry has no model
// or usage, or usage of no tokens: what a renamed field looks like.
// Claude Code's own messages have neither, and aren't calls.
func TestCallsCountDrift(t *testing.T) {
	t.Parallel()
	drift := agentlog.NewDrift(Format)
	c := NewCalls(drift)
	for _, line := range []string{
		`{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":3,"cache_read_input_tokens":100,"output_tokens":20}}}`,
		// An API error, which Claude Code writes itself.
		`{"type":"assistant","timestamp":"2026-10-20T10:00:01Z","isApiErrorMessage":true,"message":{"id":"m2","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}`,
		// Renamed usage fields.
		`{"type":"assistant","timestamp":"2026-10-20T10:00:02Z","message":{"id":"m3","model":"claude-opus-5-5","usage":{"inputTokens":3,"outputTokens":20}}}`,
		// A renamed usage, a missing model.
		`{"type":"assistant","timestamp":"2026-10-20T10:00:03Z","message":{"id":"m4","model":"claude-opus-5-5","token_usage":{"input_tokens":3}}}`,
		`{"type":"assistant","timestamp":"2026-10-20T10:00:04Z","message":{"id":"m5","usage":{"input_tokens":3,"output_tokens":20}}}`,
		// A field of another type: the rest is read.
		`{"type":"assistant","timestamp":"2026-10-20T10:00:05Z","message":{"id":"m6","model":"claude-opus-5-5","usage":{"input_tokens":"3","output_tokens":20}}}`,
	} {
		c.Line([]byte(line))
	}
	calls := c.Calls()
	assert.Len(t, calls, 2)
	assert.Equal(t, pricing.Tokens{Input: 3, CacheRead: 100, Output: 20}, calls[0].Tokens)
	assert.Equal(t, pricing.Tokens{Output: 20}, calls[1].Tokens)
	assert.Equal(t, 1, drift.Calls(agentlog.NoTokens))
	assert.Equal(t, 2, drift.Calls(agentlog.NoModelOrUsage))
}
