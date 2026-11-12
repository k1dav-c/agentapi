package codexlog

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// tokenCount is a token_count event with the running total and the last
// call's input and output.
func tokenCount(totalKey string, totalIn, totalOut, lastIn, lastOut int) string {
	return fmt.Sprintf(`{"timestamp":"2026-10-20T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{%q:{"input_tokens":%d,"output_tokens":%d},"last_token_usage":{"input_tokens":%d,"output_tokens":%d}}}}`,
		totalKey, totalIn, totalOut, lastIn, lastOut)
}

// Counts with only the rate limits, and the same count again, are no
// calls; a running total that reads as no tokens, or a call without the
// turn's model, is drift.
func TestCallsCountDrift(t *testing.T) {
	t.Parallel()
	drift := agentlog.NewDrift(Format)
	c := NewCalls(drift)
	for _, line := range []string{
		`{"timestamp":"2026-10-20T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{}}}`,
		// No turn context yet: a call without a model.
		tokenCount("total_token_usage", 1000, 10, 1000, 10),
		`{"timestamp":"2026-10-20T10:00:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		tokenCount("total_token_usage", 1000, 10, 1000, 10),
		tokenCount("total_token_usage", 3000, 30, 2000, 20),
		// Renamed fields.
		tokenCount("total_usage", 5000, 50, 2000, 20),
		`{"timestamp":"2026-10-20T10:00:00Z","type":"turn_context","payload":{"model_id":"gpt-6.1-sol"}}`,
	} {
		c.Line([]byte(line))
	}
	from, calls := c.Changed()
	assert.Zero(t, from)
	assert.Len(t, calls, 2)
	assert.Empty(t, calls[0].Model)
	assert.Equal(t, "gpt-6.1-sol", calls[1].Model)
	assert.Equal(t, pricing.Tokens{Input: 2000, Output: 20}, calls[1].Tokens)
	assert.Equal(t, 1, drift.Calls(agentlog.NoTokens))
	assert.Equal(t, 2, drift.Calls(agentlog.NoModelOrUsage))

	// The first count of a log, whose last call reads as no tokens.
	drift = agentlog.NewDrift(Format)
	c = NewCalls(drift)
	c.Line([]byte(`{"timestamp":"2026-10-20T10:00:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`))
	c.Line([]byte(`{"timestamp":"2026-10-20T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":10},"last":{"input_tokens":1000,"output_tokens":10}}}}`))
	c.Line([]byte(tokenCount("total_token_usage", 3000, 30, 2000, 20)))
	_, calls = c.Changed()
	assert.Len(t, calls, 1, "the next call is the total's change")
	assert.Equal(t, 1, drift.Calls(agentlog.NoTokens))

	c.Line([]byte(tokenCount("total_token_usage", 4000, 40, 1000, 10)))
	from, calls = c.Changed()
	assert.Equal(t, 1, from, "the calls read since")
	assert.Len(t, calls, 1)
}

// A running total that went down started over: its call is the last one,
// not a difference clamped to nothing.
func TestCallsAfterTheTotalStartedOver(t *testing.T) {
	t.Parallel()
	c := NewCalls(nil)
	c.Line([]byte(`{"timestamp":"2026-10-20T10:00:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`))
	c.Line([]byte(tokenCount("total_token_usage", 50000, 500, 2000, 20)))
	c.Line([]byte(tokenCount("total_token_usage", 3000, 30, 3000, 30)))
	c.Line([]byte(tokenCount("total_token_usage", 4000, 40, 1000, 10)))
	_, calls := c.Changed()
	if assert.Len(t, calls, 3) {
		assert.Equal(t, pricing.Tokens{Input: 3000, Output: 30}, calls[1].Tokens)
		assert.Equal(t, pricing.Tokens{Input: 1000, Output: 10}, calls[2].Tokens, "the change of the new total")
	}
}

// A call goes to the thread's provider: its session_meta's, then that of
// each thread_settings_applied event. AgentAPI's providers are named
// agentapi-<id>; any other is the agent's own.
func TestCallsProvider(t *testing.T) {
	t.Parallel()
	c := NewCalls(nil)
	settings := func(provider string) string {
		return `{"timestamp":"2026-10-20T10:00:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-6.1-sol","model_provider_id":"` + provider + `","approval_policy":"never"}}}`
	}
	total := 0
	call := func() {
		total += 1000
		c.Line([]byte(tokenCount("total_token_usage", total, total/100, 1000, 10)))
	}
	for _, line := range []string{
		`{"timestamp":"2026-10-20T10:00:00Z","type":"session_meta","payload":{"id":"t1","cwd":"/work/app","originator":"agentapi","model_provider":"agentapi-gateway","base_instructions":{"text":"no \"thread_settings_applied\" here"}}}`,
		`{"timestamp":"2026-10-20T10:00:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
	} {
		c.Line([]byte(line))
	}
	call()
	c.Line([]byte(settings("openai")))
	call()
	c.Line([]byte(settings("agentapi-local")))
	call()
	// Another thread's settings that don't name a provider change none.
	c.Line([]byte(`{"timestamp":"2026-10-20T10:00:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-6.1-sol"}}}`))
	call()
	// A session_meta line of a resumed thread names its provider again.
	c.Line([]byte(`{"timestamp":"2026-10-20T10:00:00Z","type":"session_meta","payload":{"id":"t1","model_provider":"my-own"}}`))
	call()
	_, calls := c.Changed()
	providers := make([]string, len(calls))
	for i, call := range calls {
		providers[i] = call.Provider
	}
	assert.Equal(t, []string{"gateway", "", "local", "local", ""}, providers)
}

func TestProviderID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "gw", ProviderID("agentapi-gw"))
	assert.Equal(t, "", ProviderID("openai"))
	assert.Equal(t, "", ProviderID(""))
	assert.Equal(t, "", ProviderID("agentapi"))
}
