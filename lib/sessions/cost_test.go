package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/pricing"
)

// claudeCall is an assistant line of a Claude Code log.
func claudeCall(session, at, id string, input, cacheRead, write5m, write1h, output int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":%q,"requestId":"req_%s","message":{"id":%q,"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],`+
		`"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		at, session, id, id, input, cacheRead, write5m+write1h, output, write5m, write1h)
}

func claudePromptLine(session, at, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":%q,"message":{"role":"user","content":%q}}`, at, session, text)
}

func TestClaudeCosts(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	first := pricing.Tokens{Input: 10, CacheRead: 20000, CacheWrite: 1000, CacheWrite1h: 3000, Output: 500}
	second := pricing.Tokens{Input: 5, CacheRead: 24000, Output: 800}
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now().Add(-time.Hour),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		// One call logged on two lines (thinking, then text), each with
		// its usage.
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 1000, 3000, 200),
		claudeCall("s1", "2026-10-20T10:00:06.000Z", "msg_1", 10, 20000, 1000, 3000, 500),
		`{"type":"assistant","timestamp":"2026-10-20T10:00:07.000Z","sessionId":"s1","message":{"id":"msg_x","model":"<synthetic>","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}`,
		claudePromptLine("s1", "2026-10-20T11:30:00.000Z", "And the tests"),
		claudeCall("s1", "2026-10-20T11:30:05.000Z", "msg_2", 5, 24000, 0, 0, 800),
	)
	// A subagent of s1.
	subDir := filepath.Join(dir, "s1", "subagents")
	writeLog(t, filepath.Join(subDir, "agent-a1.jsonl"), time.Now(),
		`{"isSidechain":true,"agentId":"a1","type":"user","timestamp":"2026-10-20T10:01:00.000Z","sessionId":"s1","message":{"role":"user","content":"Look"}}`,
		claudeCall("s1", "2026-10-20T10:01:02.000Z", "msg_sub", 3, 1000, 0, 0, 100),
	)
	// A fork of s1, made later: it copies s1's first call, then makes one.
	writeLog(t, filepath.Join(dir, "s2.jsonl"), time.Now(),
		claudePromptLine("s2", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s2", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 1000, 3000, 500),
		claudePromptLine("s2", "2026-10-20T12:00:00.000Z", "Try another way"),
		claudeCall("s2", "2026-10-20T12:00:04.000Z", "msg_3", 7, 21000, 0, 0, 300),
	)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "s2.jsonl"), time.Now(), time.Now()))

	d, err := DirectoryCosts("claude", "/w", pricing.Builtin())
	require.NoError(t, err)
	s1 := d.Cost("s1")
	sub := opus.Cost(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100})
	assert.Equal(t, 3, s1.Calls, "one per call, the subagent's included, not the synthetic message")
	assert.InDelta(t, opus.Cost(first)+opus.Cost(second)+sub, s1.USD, 1e-9)
	assert.Equal(t, first.Add(second).Add(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100}), s1.Tokens)
	require.Len(t, s1.Hourly, 2)
	assert.Equal(t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), s1.Hourly[0].Hour)
	assert.InDelta(t, opus.Cost(first)+sub, s1.Hourly[0].USD, 1e-9)
	require.Len(t, s1.Models, 1)
	assert.True(t, s1.Models[0].Priced)

	var subCalls int
	for _, c := range d.Sessions["s1"] {
		if c.SubagentID == "a1" {
			subCalls++
		}
	}
	assert.Equal(t, 1, subCalls)

	s2 := d.Cost("s2")
	assert.Equal(t, 1, s2.Calls, "the copied call counts in s1")
	assert.InDelta(t, s1.USD+s2.USD, d.Total.USD, 1e-9)
}

func TestClaudeCostReconciled(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	logged := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 20000, Output: 500})
	// Claude Code counted more than its log lists: a compaction, say.
	report := fmt.Sprintf(`{"type":"cost-state","sessionId":"s1","totalCostUSD":%f,"modelUsage":{"claude-opus-5-5":{"inputTokens":5010,"outputTokens":900,"cacheReadInputTokens":20000,"cacheCreationInputTokens":0,"costUSD":%f}}}`, logged+0.5, logged+0.5)
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 500),
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-20T10:05:00.000Z","sessionId":"s1"}`,
		report,
		// Later calls aren't in the report yet.
		claudeCall("s1", "2026-10-20T10:10:00.000Z", "msg_2", 10, 20000, 0, 0, 500),
	)
	d, err := DirectoryCosts("claude", "/w", pricing.Builtin())
	require.NoError(t, err)
	c := d.Cost("s1")
	assert.InDelta(t, 2*logged+0.5, c.USD, 1e-6)
	var unitemized []ModelCall
	for _, call := range d.Sessions["s1"] {
		if call.Unitemized {
			unitemized = append(unitemized, call)
		}
	}
	require.Len(t, unitemized, 1)
	assert.InDelta(t, 0.5, unitemized[0].USD, 1e-6)
	assert.Equal(t, time.Date(2026, 10, 20, 10, 5, 0, 0, time.UTC), unitemized[0].Time, "at the line before the report")
	assert.Equal(t, pricing.Tokens{Input: 5000, Output: 400}, unitemized[0].Tokens)
}

func TestCodexCosts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "20")
	count := func(at string, total, last [4]int) string {
		u := func(v [4]int) string {
			return fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":%d,"output_tokens":%d}`, v[0], v[1], v[2], v[3])
		}
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s,"last_token_usage":%s}}}`, at, u(total), u(last))
	}
	writeLog(t, filepath.Join(day, "rollout-1-t1.jsonl"), time.Now(),
		`{"timestamp":"2026-10-20T10:00:00.000Z","type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w"}}`,
		`{"timestamp":"2026-10-20T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		`{"timestamp":"2026-10-20T10:00:01.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Go"}]}}`,
		count("2026-10-20T10:00:02.000Z", [4]int{10000, 8000, 0, 100}, [4]int{10000, 8000, 0, 100}),
		// The same count again (a rate limit update): nothing new.
		count("2026-10-20T10:00:02.500Z", [4]int{10000, 8000, 0, 100}, [4]int{10000, 8000, 0, 100}),
		count("2026-10-20T10:00:03.000Z", [4]int{22000, 18000, 0, 150}, [4]int{12000, 10000, 0, 50}),
	)
	sol, _ := pricing.Builtin().Lookup("gpt-6.1-sol")
	d, err := DirectoryCosts("codex", "/w", pricing.Builtin())
	require.NoError(t, err)
	c := d.Cost("t1")
	assert.Equal(t, 2, c.Calls)
	want := sol.Cost(pricing.Tokens{Input: 2000, CacheRead: 8000, Output: 100}) + sol.Cost(pricing.Tokens{Input: 2000, CacheRead: 10000, Output: 50})
	assert.InDelta(t, want, c.USD, 1e-12)
}

func TestPiCostsAndUnknownPrices(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	dir := filepath.Join(agentDir, "sessions", "--w--")
	writeLog(t, filepath.Join(dir, "2026-10-20T10-00-00-000Z_p1.jsonl"), time.Now(),
		`{"type":"session","version":3,"id":"p1","timestamp":"2026-10-20T10:00:00.000Z","cwd":"/w"}`,
		`{"type":"message","id":"u1","timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Hi"}]}}`,
		// Pi prices its calls itself.
		`{"type":"message","id":"a1","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"assistant","model":"gpt-6.1-sol","content":[{"type":"text","text":"Hello"}],"usage":{"input":1000,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.0123}}}}`,
		// A model without a price, which Pi didn't price either.
		`{"type":"message","id":"a2","timestamp":"2026-10-20T10:00:03.000Z","message":{"role":"assistant","model":"local-llama","content":[{"type":"text","text":"Hi"}],"usage":{"input":500,"output":5,"cacheRead":0,"cacheWrite":0}}}`,
	)
	d, err := DirectoryCosts("pi", "/w", pricing.Builtin())
	require.NoError(t, err)
	c := d.Cost("p1")
	assert.Equal(t, 2, c.Calls)
	assert.InDelta(t, 0.0123, c.USD, 1e-12)
	require.Len(t, c.Models, 2)
	assert.Equal(t, "gpt-6.1-sol", c.Models[0].Model, "the most expensive first")
	assert.True(t, c.Models[0].Priced)
	assert.False(t, c.Models[1].Priced)
	assert.Equal(t, 500, c.Models[1].Tokens.Input)
}
