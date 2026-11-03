package claudelog

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	from, calls := c.Changed()
	assert.Zero(t, from)
	assert.Len(t, calls, 2)
	assert.Equal(t, pricing.Tokens{Input: 3, CacheRead: 100, Output: 20}, calls[0].Tokens)
	assert.Equal(t, pricing.Tokens{Output: 20}, calls[1].Tokens)
	assert.Equal(t, 1, drift.Calls(agentlog.NoTokens))
	assert.Equal(t, 2, drift.Calls(agentlog.NoModelOrUsage))
}

// Changed returns the calls read since it last returned, and those a
// later line of theirs completed.
func TestCallsChanged(t *testing.T) {
	t.Parallel()
	c := NewCalls(nil)
	call := func(id string, output int) []byte {
		return []byte(`{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"id":"` + id + `","model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":` + strconv.Itoa(output) + `,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20}}}}`)
	}
	c.Line(call("m1", 5))
	c.Line(call("m2", 5))
	from, calls := c.Changed()
	assert.Zero(t, from)
	require.Len(t, calls, 2)
	assert.Equal(t, pricing.Tokens{Input: 3, CacheWrite: 10, CacheWrite1h: 20, Output: 5}, calls[0].Tokens, "the cache written for 5 minutes and for an hour")
	from, calls = c.Changed()
	assert.Equal(t, 2, from)
	assert.Empty(t, calls)

	c.Line(call("m2", 5))
	from, calls = c.Changed()
	assert.Equal(t, 2, from, "the same usage again changes nothing")
	assert.Empty(t, calls)

	c.Line(call("m3", 7))
	c.Line(call("m2", 9))
	from, calls = c.Changed()
	assert.Equal(t, 1, from)
	require.Len(t, calls, 2)
	assert.Equal(t, 9, calls[0].Tokens.Output)
	assert.Equal(t, "claude:m3", calls[1].Key)
}

func TestInteger(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]int{"0": 0, "12345": 12345, "-3": -3, "1.5": 0, `"7"`: 0, "": 0, "null": 0, "1e3": 0} {
		assert.Equal(t, want, integer([]byte(value)), value)
	}
}

// A call with neither a message ID nor a request ID has no key: every one
// counts, rather than all of them as one.
func TestCallsWithoutIDsHaveNoKey(t *testing.T) {
	t.Parallel()
	c := NewCalls(nil)
	for range 3 {
		c.Line([]byte(`{"type":"assistant","timestamp":"2026-10-20T10:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":5}}}`))
	}
	_, calls := c.Changed()
	require.Len(t, calls, 3)
	for _, call := range calls {
		assert.Empty(t, call.Key)
	}
}

func TestPreamble(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`{"type":"file-history-snapshot","messageId":"m0","snapshot":{}}`:           true,
		`{"type":"summary","summary":"Earlier work","leafUuid":"u0"}`:               true,
		`{"type":"user","sessionId":"s1","message":{"role":"user","content":"Hi"}}`: false,
		`{"type":"user","isSidechain":true,"message":{"role":"user"}}`:              false,
		`{"type":"file-history-snapshot"`:                                           false,
		`not json`:                                                                  false,
	} {
		assert.Equal(t, want, Preamble([]byte(line)), line)
	}
}
