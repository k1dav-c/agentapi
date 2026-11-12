package sessions

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog"
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

	d, err := newStore(t, "claude", "/w").Costs()
	require.NoError(t, err)
	s1 := d.Cost("s1")
	sub := opus.Cost(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100})
	assert.Equal(t, 3, s1.Calls, "one per call, the subagent's included, not the synthetic message")
	assert.InDelta(t, opus.Cost(first)+opus.Cost(second)+sub, s1.USD, 1e-9)
	assert.Equal(t, first.Add(second).Add(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100}), s1.Tokens)
	require.Len(t, s1.Hourly, 2)
	assert.Equal(t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), s1.Hourly[0].Hour)
	assert.InDelta(t, opus.Cost(first)+sub, s1.Hourly[0].USD, 1e-9)
	assert.Equal(t, first.Add(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100}), s1.Hourly[0].Tokens, "the call once, though logged on two lines, and the subagent's")
	assert.Equal(t, second, s1.Hourly[1].Tokens)
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
	// The hours of all the sessions count each call's tokens once: the
	// fork's copy of s1's call counts in s1.
	assert.Equal(t, []pricing.Tokens{
		first.Add(pricing.Tokens{Input: 3, CacheRead: 1000, Output: 100}),
		second,
		{Input: 7, CacheRead: 21000, Output: 300},
	}, hourTokens(d.Total.Hourly))
	assert.Equal(t, d.Total.Tokens, sumTokens(d.Total.Hourly))
}

// hourTokens are the tokens of each hour.
func hourTokens(hourly []HourCost) []pricing.Tokens {
	tokens := make([]pricing.Tokens, len(hourly))
	for i, h := range hourly {
		tokens[i] = h.Tokens
	}
	return tokens
}

// sumTokens adds up the hours' tokens.
func sumTokens(hourly []HourCost) pricing.Tokens {
	var sum pricing.Tokens
	for _, h := range hourly {
		sum = sum.Add(h.Tokens)
	}
	return sum
}

func TestClaudeCostReconciled(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	logged := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 20000, Output: 500})
	compacted := opus.Cost(pricing.Tokens{Input: 5000, Output: 400})
	// Claude Code counted more tokens than its log lists: a compaction,
	// say.
	report := fmt.Sprintf(`{"type":"cost-state","sessionId":"s1","totalCostUSD":%f,"modelUsage":{"claude-opus-5-5":{"inputTokens":5010,"outputTokens":900,"cacheReadInputTokens":20000,"cacheCreationInputTokens":0,"costUSD":%f}}}`, logged+compacted, logged+compacted)
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 500),
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-20T10:05:00.000Z","sessionId":"s1"}`,
		report,
		// Later calls aren't in the report yet.
		claudeCall("s1", "2026-10-20T10:10:00.000Z", "msg_2", 10, 20000, 0, 0, 500),
	)
	d, err := newStore(t, "claude", "/w").Costs()
	require.NoError(t, err)
	c := d.Cost("s1")
	assert.InDelta(t, 2*logged+compacted, c.USD, 1e-6)
	var unitemized []ModelCall
	for _, call := range d.Sessions["s1"] {
		if call.Unitemized {
			unitemized = append(unitemized, call)
		}
	}
	require.Len(t, unitemized, 1)
	assert.InDelta(t, compacted, unitemized[0].USD, 1e-9, "priced as any call")
	assert.Equal(t, time.Date(2026, 10, 20, 10, 5, 0, 0, time.UTC), unitemized[0].Time, "at the line before the report")
	assert.Equal(t, pricing.Tokens{Input: 5000, Output: 400}, unitemized[0].Tokens)
	// The tokens Claude Code counted but didn't log count in the hour, as
	// their cost does.
	require.Len(t, c.Hourly, 1)
	assert.Equal(t, pricing.Tokens{Input: 5020, CacheRead: 40000, Output: 1400}, c.Hourly[0].Tokens)
	assert.Equal(t, c.Tokens, c.Hourly[0].Tokens)
}

// Claude Code's cost of the same tokens at other prices (a price it
// changed, a --prices file) isn't a call it didn't log: no unitemized call
// is added, the costs are AgentAPI's, and the difference is logged once.
func TestClaudeCostPriceDriftAddsNoCall(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	logged := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 20000, Output: 500})
	report := fmt.Sprintf(`{"type":"cost-state","sessionId":"s1","modelUsage":{"claude-opus-5-5":{"inputTokens":10,"outputTokens":500,"cacheReadInputTokens":20000,"cacheCreationInputTokens":0,"costUSD":%f}}}`, logged+0.5)
	path := filepath.Join(dir, "s1.jsonl")
	writeLog(t, path, time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 500),
		report,
	)
	var logs bytes.Buffer
	store, err := New("claude", "/w", Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	d, err := store.Costs()
	require.NoError(t, err)
	c := d.Cost("s1")
	assert.InDelta(t, logged, c.USD, 1e-9)
	assert.Equal(t, 1, c.Calls)
	for _, call := range d.Sessions["s1"] {
		assert.False(t, call.Unitemized)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "differs from AgentAPI's prices"), logs.String())
	// The session goes on; the next reading finds the difference again.
	appendLines(t, path, report+"\n")
	_, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(logs.String(), "differs from AgentAPI's prices"), "once per model")
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
	d, err := newStore(t, "codex", "/w").Costs()
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
	var logs strings.Builder
	store, err := New("pi", "/w", Config{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	require.NoError(t, err)
	d, err := store.Costs()
	require.NoError(t, err)
	c := d.Cost("p1")
	assert.Equal(t, 2, c.Calls)
	// The model without a price is logged once, however many calls it has.
	assert.Equal(t, 1, strings.Count(logs.String(), "model=local-llama"), logs.String())
	writeLog(t, filepath.Join(dir, "2026-10-20T11-00-00-000Z_p2.jsonl"), time.Now(),
		`{"type":"session","version":3,"id":"p2","timestamp":"2026-10-20T11:00:00.000Z","cwd":"/w"}`,
		`{"type":"message","id":"a3","timestamp":"2026-10-20T11:00:03.000Z","message":{"role":"assistant","model":"local-llama","content":[{"type":"text","text":"Hi"}],"usage":{"input":500,"output":5,"cacheRead":0,"cacheWrite":0}}}`,
	)
	_, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(logs.String(), "model=local-llama"), logs.String())
	assert.InDelta(t, 0.0123, c.USD, 1e-12)
	require.Len(t, c.Models, 2)
	assert.Equal(t, "gpt-6.1-sol", c.Models[0].Model, "the most expensive first")
	assert.True(t, c.Models[0].Priced)
	assert.False(t, c.Models[1].Priced)
	assert.Equal(t, 500, c.Models[1].Tokens.Input)
}

// What a session spent counts in the directory's total whether or not
// its log holds a prompt the session list shows.
func TestCostsOfSessionsWithoutPrompts(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 200))
	// Only a slash command, which Claude Code ran a model call for.
	writeLog(t, filepath.Join(dir, "s2.jsonl"), time.Now(),
		claudePromptLine("s2", "2026-10-20T11:00:00.000Z", "<command-name>/compact</command-name>"),
		claudeCall("s2", "2026-10-20T11:00:05.000Z", "msg_2", 10, 20000, 0, 0, 300))
	store := newStore(t, "claude", "/w")
	list, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1, "a session the user sent nothing in isn't listed")
	d, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 2, d.Total.Calls)
	assert.Equal(t, 1, d.Cost("s2").Calls)
}

// Pi keeps the prompt cache warm between turns with calls of its own,
// which it logs as usage entries.
func TestPiCacheWarmingCosts(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	writeLog(t, filepath.Join(agentDir, "sessions", "--w--", "2026-10-20T10-00-00-000Z_p1.jsonl"), time.Now(),
		`{"type":"session","version":3,"id":"p1","timestamp":"2026-10-20T10:00:00.000Z","cwd":"/w"}`,
		`{"type":"message","id":"u1","timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Hi"}]}}`,
		`{"type":"message","id":"a1","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Hello"}],"usage":{"input":10,"output":10,"cacheRead":0,"cacheWrite":20000,"cost":{"total":0.13}}}}`,
		`{"type":"usage","id":"w1","parentId":"a1","timestamp":"2026-10-20T10:04:30.000Z","kind":"cache_warm","provider":"anthropic","model":"claude-opus-5-5","usage":{"input":1,"output":1,"cacheRead":20000,"cacheWrite":0,"totalTokens":20002,"cost":{"total":0.01}}}`,
	)
	d, err := newStore(t, "pi", "/w").Costs()
	require.NoError(t, err)
	c := d.Cost("p1")
	assert.Equal(t, 2, c.Calls)
	assert.InDelta(t, 0.14, c.USD, 1e-12)
}

// matchesAFreshReading checks that what store reads now is what a new
// Store reads of the same logs, and returns it.
func matchesAFreshReading(t *testing.T, store *Store, agentType, cwd, step string) DirectoryCost {
	t.Helper()
	got, err := store.Costs()
	require.NoError(t, err, step)
	want, err := newStore(t, agentType, cwd).Costs()
	require.NoError(t, err, step)
	require.Equal(t, want, got, step)
	return got
}

// A Store that read the logs before reads only what changed since, and
// finds what a new one finds, whatever changed.
func TestClaudeCostsMatchAFreshReading(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	report := func(session string, usd float64) string {
		return fmt.Sprintf(`{"type":"cost-state","sessionId":%q,"modelUsage":{"claude-opus-5-5":{"inputTokens":9000,"outputTokens":900,"cacheReadInputTokens":50000,"cacheCreationInputTokens":0,"costUSD":%f},"claude-haiku-5-5":{"inputTokens":100,"outputTokens":10,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.25}}}`, session, usd)
	}
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 1000, 3000, 200),
		claudeCall("s1", "2026-10-20T10:00:06.000Z", "msg_1", 10, 20000, 1000, 3000, 500),
		claudeCall("s1", "2026-10-20T10:01:00.000Z", "msg_2", 5, 24000, 0, 0, 800),
		report("s1", 2.5))
	// A fork of s1: it copies msg_1.
	writeLog(t, filepath.Join(dir, "s2.jsonl"), time.Now(),
		claudePromptLine("s2", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s2", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 1000, 3000, 500),
		claudeCall("s2", "2026-10-20T12:00:00.000Z", "msg_3", 7, 21000, 0, 0, 300))
	writeLog(t, filepath.Join(dir, "s3.jsonl"), time.Now(),
		claudePromptLine("s3", "2026-10-21T09:00:00.000Z", "Look around"),
		claudeCall("s3", "2026-10-21T09:00:05.000Z", "msg_4", 3, 1000, 0, 0, 100))
	writeLog(t, filepath.Join(dir, "s3", "subagents", "agent-a1.jsonl"), time.Now(),
		claudeCall("s3", "2026-10-21T09:00:07.000Z", "msg_sub1", 2, 500, 0, 0, 50))
	s4 := filepath.Join(dir, "s4.jsonl")
	writeLog(t, s4, time.Now(),
		claudePromptLine("s4", "2026-10-22T09:00:00.000Z", "And the tests"),
		claudeCall("s4", "2026-10-22T09:00:05.000Z", "msg_5", 3, 1000, 0, 0, 100))

	store := newStore(t, "claude", "/w")
	d := matchesAFreshReading(t, store, "claude", "/w", "first reading")
	assert.Equal(t, 1, d.Cost("s2").Calls, "the copied call counts in s1")

	steps := []struct {
		name   string
		change func()
	}{
		{"a call appended", func() {
			appendLines(t, s4, claudeCall("s4", "2026-10-22T09:01:00.000Z", "msg_6", 3, 1100, 0, 0, 10)+"\n")
		}},
		{"the call's later line", func() {
			appendLines(t, s4, claudeCall("s4", "2026-10-22T09:01:01.000Z", "msg_6", 3, 1100, 0, 0, 90)+"\n")
		}},
		{"a line being written", func() {
			appendLines(t, s4, claudeCall("s4", "2026-10-22T09:02:00.000Z", "msg_7", 3, 1200, 0, 0, 40)[:50])
		}},
		{"the line written", func() {
			appendLines(t, s4, claudeCall("s4", "2026-10-22T09:02:00.000Z", "msg_7", 3, 1200, 0, 0, 40)[50:]+"\n")
		}},
		{"an earlier session gets a call a later one has", func() {
			appendLines(t, filepath.Join(dir, "s1.jsonl"), claudeCall("s1", "2026-10-20T12:00:00.000Z", "msg_3", 7, 21000, 0, 0, 300)+"\n")
		}},
		{"a subagent starts", func() {
			writeLog(t, filepath.Join(dir, "s3", "subagents", "agent-a2.jsonl"), time.Now(),
				claudeCall("s3", "2026-10-21T09:10:00.000Z", "msg_sub2", 2, 600, 0, 0, 60))
		}},
		{"the subagent goes on", func() {
			appendLines(t, filepath.Join(dir, "s3", "subagents", "agent-a2.jsonl"), claudeCall("s3", "2026-10-21T09:11:00.000Z", "msg_sub3", 2, 700, 0, 0, 70)+"\n")
		}},
		{"Claude Code's own count, after a call later than the subagents' first", func() {
			appendLines(t, filepath.Join(dir, "s3.jsonl"),
				claudeCall("s3", "2026-10-21T09:05:00.000Z", "msg_10", 3, 1000, 0, 0, 30)+"\n",
				report("s3", 1.5)+"\n")
		}},
		{"a later session gets a call an earlier one has", func() {
			appendLines(t, filepath.Join(dir, "s3.jsonl"), claudeCall("s3", "2026-10-21T10:00:00.000Z", "msg_2", 5, 24000, 0, 0, 800)+"\n")
		}},
		{"a new session", func() {
			writeLog(t, filepath.Join(dir, "s5.jsonl"), time.Now(),
				claudePromptLine("s5", "2026-10-23T09:00:00.000Z", "Next"),
				claudeCall("s5", "2026-10-23T09:00:05.000Z", "msg_8", 3, 1000, 0, 0, 100))
		}},
		{"a session gone", func() {
			require.NoError(t, os.Remove(filepath.Join(dir, "s2.jsonl")))
		}},
		{"a log rewritten", func() {
			writeLog(t, s4, time.Now(),
				claudePromptLine("s4", "2026-10-22T09:00:00.000Z", "Again"),
				claudeCall("s4", "2026-10-22T09:00:05.000Z", "msg_9", 3, 1000, 0, 0, 100))
		}},
		{"a subagent logs a call of its session's", func() {
			writeLog(t, filepath.Join(dir, "s4", "subagents", "agent-a3.jsonl"), time.Now(),
				claudeCall("s4", "2026-10-22T09:00:05.000Z", "msg_9", 3, 1000, 0, 0, 100),
				claudeCall("s4", "2026-10-22T09:00:09.000Z", "msg_sub4", 1, 100, 0, 0, 10))
		}},
		{"nothing", func() {}},
	}
	for _, step := range steps {
		step.change()
		matchesAFreshReading(t, store, "claude", "/w", step.name)
	}
	d = matchesAFreshReading(t, store, "claude", "/w", "again")
	assert.Equal(t, 2, d.Cost("s4").Calls, "the call logged twice counts once")
	assert.Equal(t, int64(len(steps)+2), store.costPasses.Load())
}

func TestCodexCostsMatchAFreshReading(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "20")
	count := func(at string, input, output int) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":%d}}}}`, at, input, output, input, output)
	}
	t1 := filepath.Join(day, "rollout-2026-10-20T10-00-00-t1.jsonl")
	writeLog(t, t1, time.Now(),
		`{"timestamp":"2026-10-20T10:00:00.000Z","type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w"}}`,
		`{"timestamp":"2026-10-20T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		count("2026-10-20T10:00:02.000Z", 1000, 10))
	store := newStore(t, "codex", "/w")
	matchesAFreshReading(t, store, "codex", "/w", "first reading")
	appendLines(t, t1, count("2026-10-20T10:01:00.000Z", 3000, 30)+"\n")
	matchesAFreshReading(t, store, "codex", "/w", "a count appended")
	sub := filepath.Join(day, "rollout-2026-10-20T10-02-00-t2.jsonl")
	writeLog(t, sub, time.Now(),
		`{"timestamp":"2026-10-20T10:02:00.000Z","type":"session_meta","payload":{"id":"t2","session_id":"t1","parent_thread_id":"t1","cwd":"/w","agent_nickname":"Sub"}}`,
		`{"timestamp":"2026-10-20T10:02:01.000Z","type":"turn_context","payload":{"model":"gpt-6.1-sol-mini"}}`,
		count("2026-10-20T10:02:02.000Z", 500, 5))
	matchesAFreshReading(t, store, "codex", "/w", "a subagent")
	appendLines(t, sub, count("2026-10-20T10:03:00.000Z", 900, 9)+"\n")
	d := matchesAFreshReading(t, store, "codex", "/w", "the subagent goes on")
	assert.Equal(t, 4, d.Cost("t1").Calls)
}

func TestPiCostsMatchAFreshReading(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	dir := filepath.Join(agentDir, "sessions", "--w--")
	call := func(id, at string, input int) string {
		return fmt.Sprintf(`{"type":"message","id":%q,"timestamp":%q,"message":{"role":"assistant","model":"gpt-6.1-sol","content":[{"type":"text","text":"ok"}],"usage":{"input":%d,"output":10,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.01}}}}`, id, at, input)
	}
	p1 := filepath.Join(dir, "2026-10-20T10-00-00-000Z_p1.jsonl")
	writeLog(t, p1, time.Now(),
		`{"type":"session","version":3,"id":"p1","timestamp":"2026-10-20T10:00:00.000Z","cwd":"/w"}`,
		call("a1", "2026-10-20T10:00:02.000Z", 1000))
	// A fork of p1: it copies a1.
	p2 := filepath.Join(dir, "2026-10-20T11-00-00-000Z_p2.jsonl")
	writeLog(t, p2, time.Now(),
		`{"type":"session","version":3,"id":"p2","timestamp":"2026-10-20T11:00:00.000Z","cwd":"/w"}`,
		call("a1", "2026-10-20T10:00:02.000Z", 1000),
		call("a2", "2026-10-20T11:00:02.000Z", 2000))
	store := newStore(t, "pi", "/w")
	matchesAFreshReading(t, store, "pi", "/w", "first reading")
	appendLines(t, p2, call("a3", "2026-10-20T11:01:00.000Z", 3000)+"\n")
	matchesAFreshReading(t, store, "pi", "/w", "the fork goes on")
	appendLines(t, p1, call("a4", "2026-10-20T11:02:00.000Z", 4000)+"\n")
	d := matchesAFreshReading(t, store, "pi", "/w", "the session goes on")
	assert.Equal(t, 2, d.Cost("p1").Calls)
	assert.Equal(t, 2, d.Cost("p2").Calls)
}

// A reading that finds no log changed returns the last one's result.
func TestCostsUnchangedAreTheLastReadings(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	path := filepath.Join(config, "projects", "-w", "s1.jsonl")
	writeLog(t, path, time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 200))
	store := newStore(t, "claude", "/w")
	first, err := store.Costs()
	require.NoError(t, err)
	again, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(first.Sessions).Pointer(), reflect.ValueOf(again.Sessions).Pointer(), "the same result")

	// Lines that hold no call change the log, not its calls.
	appendLines(t, path, claudePromptLine("s1", "2026-10-20T10:01:00.000Z", "And the tests")+"\n")
	again, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(first.Sessions).Pointer(), reflect.ValueOf(again.Sessions).Pointer())
	appendLines(t, path, claudeCall("s1", "2026-10-20T10:01:05.000Z", "msg_2", 5, 21000, 0, 0, 100)+"\n")
	again, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 2, again.Cost("s1").Calls)
	assert.Equal(t, 1, first.Cost("s1").Calls, "an earlier result stays as it was")
	assert.Len(t, first.Sessions["s1"], 1)

	// The call's later line completes it.
	appendLines(t, path, claudeCall("s1", "2026-10-20T10:01:06.000Z", "msg_2", 5, 21000, 0, 0, 900)+"\n")
	last, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 900, last.Sessions["s1"][1].Tokens.Output)
	assert.Equal(t, 100, again.Sessions["s1"][1].Tokens.Output, "an earlier result stays as it was")
}

// reconcileSorting is reconcile as it was, sorting all the calls twice.
func reconcileSorting(calls []ModelCall, prices *pricing.Table) []ModelCall {
	var reports []ModelCall
	calls = slices.DeleteFunc(calls, func(c ModelCall) bool {
		if c.report != nil {
			reports = append(reports, c)
			return true
		}
		return false
	})
	if len(reports) == 0 {
		return calls
	}
	slices.SortStableFunc(calls, byTime)
	counted := make(map[string]agentlog.ModelReport)
	next := 0
	var added []ModelCall
	for _, r := range reports {
		if r.Time.IsZero() {
			continue
		}
		for ; next < len(calls) && !calls[next].Time.After(r.Time); next++ {
			c := counted[calls[next].Model]
			c.USD += calls[next].USD
			c.Tokens = c.Tokens.Add(calls[next].Tokens)
			counted[calls[next].Model] = c
		}
		for _, model := range slices.Sorted(maps.Keys(r.report)) {
			reported := r.report[model]
			c := counted[model]
			tokens := pricing.Tokens{
				Input:      max(0, reported.Tokens.Input-c.Tokens.Input),
				CacheRead:  max(0, reported.Tokens.CacheRead-c.Tokens.CacheRead),
				CacheWrite: max(0, reported.Tokens.CacheWrite-c.Tokens.CacheWrite-c.Tokens.CacheWrite1h),
				Output:     max(0, reported.Tokens.Output-c.Tokens.Output),
			}
			price, known := prices.Lookup(model)
			if tokens.IsZero() || (known && price.Cost(tokens) < unitemizedMin) {
				continue
			}
			call := ModelCall{Time: r.Time, Model: model, Tokens: tokens, Unitemized: true, cost: -1}
			if known {
				call.USD, call.Priced = price.Cost(tokens), true
			}
			added = append(added, call)
			c.USD += call.USD
			c.Tokens = c.Tokens.Add(tokens)
			counted[model] = c
		}
	}
	calls = append(calls, added...)
	slices.SortStableFunc(calls, byTime)
	return calls
}

// reconcile sorts the calls only when they aren't in order, and merges
// what it adds into them: as sorting all of them did.
func TestReconcileAsSorting(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	models := []string{"claude-opus-5-5", "claude-haiku-5-5"}
	for range 500 {
		var calls []ModelCall
		at := start
		for i := range rng.IntN(40) {
			if rng.IntN(4) > 0 {
				at = at.Add(time.Duration(rng.IntN(3)) * time.Minute)
			} else {
				// Out of order, as a subagent's calls are after the
				// session's.
				at = start.Add(time.Duration(rng.IntN(60)) * time.Minute)
			}
			if rng.IntN(6) == 0 {
				report := map[string]agentlog.ModelReport{}
				for _, m := range models[:1+rng.IntN(2)] {
					report[m] = agentlog.ModelReport{USD: rng.Float64() * float64(i), Tokens: pricing.Tokens{Input: rng.IntN(1000 * (i + 1)), Output: rng.IntN(100 * (i + 1))}}
				}
				calls = append(calls, ModelCall{Time: at, report: report, cost: -1})
				continue
			}
			calls = append(calls, ModelCall{Time: at, Model: models[rng.IntN(2)], Tokens: pricing.Tokens{Input: rng.IntN(1000), Output: rng.IntN(100)}, USD: rng.Float64(), Priced: true, key: fmt.Sprint(i), cost: -1})
		}
		want := reconcileSorting(slices.Clone(calls), pricing.Builtin())
		got, _ := reconcile(slices.Clone(calls), pricing.Builtin())
		require.Equal(t, len(want), len(got))
		for i := range want {
			require.Equal(t, want[i], got[i], i)
		}
	}
}

// CallsDigest hashes what it did when it formatted each call with
// Fprintf: the digests clients hold stay theirs.
func TestCallsDigestAsFormatted(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	formatted := func(calls []ModelCall) string {
		h := fnv.New64a()
		for _, call := range calls {
			tk := call.Tokens
			_, _ = fmt.Fprintf(h, "%d|%s|%d|%d|%d|%d|%d|%x|%s|%t", call.Time.UnixNano(), call.Model,
				tk.Input, tk.CacheRead, tk.CacheWrite, tk.CacheWrite1h, tk.Output, math.Float64bits(call.USD), call.SubagentID, call.Unitemized)
			if call.Provider != "" {
				_, _ = fmt.Fprintf(h, "|%s", call.Provider)
			}
			_, _ = fmt.Fprint(h, "\n")
		}
		return strconv.FormatUint(h.Sum64(), 16)
	}
	var calls []ModelCall
	for i := range 200 {
		calls = append(calls, ModelCall{
			Time:   time.Unix(rng.Int64N(4e9)-1e9, rng.Int64N(1e9)),
			Model:  []string{"claude-opus-5-5", "gpt-6.1-sol", "", "ünïcode"}[rng.IntN(4)],
			Tokens: pricing.Tokens{Input: rng.IntN(1e6), CacheRead: rng.IntN(1e6), CacheWrite: -rng.IntN(3), CacheWrite1h: rng.IntN(9), Output: rng.IntN(1e5)},
			USD:    rng.NormFloat64(), SubagentID: []string{"", "a1"}[rng.IntN(2)], Unitemized: i%7 == 0,
			Provider: []string{"", "", "gw"}[rng.IntN(3)],
		})
		require.Equal(t, formatted(calls), CallsDigest(calls))
	}
	assert.Empty(t, CallsDigest(nil))
}

// Readings of the costs, of the list and of the subagents can run while
// the agent writes: each finds what a new Store finds of the logs then.
func TestCostsWhileTheAgentWrites(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	path := filepath.Join(dir, "s1.jsonl")
	writeLog(t, path, time.Now(), claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"))
	sub := filepath.Join(dir, "s1", "subagents", "agent-a1.jsonl")
	writeLog(t, sub, time.Now(), claudeCall("s1", "2026-10-20T10:00:01.000Z", "msg_sub0", 1, 100, 0, 0, 10))
	store := newStore(t, "claude", "/w")
	var (
		wg   sync.WaitGroup
		done = make(chan struct{})
	)
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				switch i {
				case 0:
					d, err := store.Costs()
					assert.NoError(t, err)
					// A result stays as it was.
					calls := slices.Clone(d.Sessions["s1"])
					runtime.Gosched()
					assert.Equal(t, calls, d.Sessions["s1"])
				case 1:
					_, err := store.List(10)
					assert.NoError(t, err)
				case 2:
					_, err := store.Subagents("s1")
					assert.NoError(t, err)
				}
			}
		}()
	}
	for i := range 200 {
		at := time.Date(2026, 10, 20, 10, 1, 0, 0, time.UTC).Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		id := fmt.Sprintf("msg_%d", i/2)
		// A call's two lines, the second completing it.
		appendLines(t, path, claudeCall("s1", at, id, 3, 1000, 0, 0, 10+i%2)+"\n")
		if i%10 == 0 {
			appendLines(t, sub, claudeCall("s1", at, fmt.Sprintf("msg_sub%d", i+1), 1, 100, 0, 0, 10)+"\n")
		}
	}
	close(done)
	wg.Wait()
	d := matchesAFreshReading(t, store, "claude", "/w", "after the writes")
	assert.Equal(t, 121, d.Cost("s1").Calls)
}

// Calls a log doesn't identify all count in the session's cost.
func TestClaudeCostsOfCallsWithoutIDs(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	call := func(at string) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":"s1","message":{"role":"assistant","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":10,"output_tokens":20}}}`, at)
	}
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Go"),
		call("2026-10-20T10:00:01.000Z"),
		call("2026-10-20T10:00:02.000Z"),
		call("2026-10-20T10:00:03.000Z"),
	)
	d, err := newStore(t, "claude", "/w").Costs()
	require.NoError(t, err)
	assert.Equal(t, 3, d.Cost("s1").Calls)
}

// A model's cost is priced only when all its calls are: Pi prices some
// calls of a model itself, and not others.
func TestModelPricedByAllItsCalls(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	calls := []ModelCall{
		{Time: at, Model: "local-llama", USD: 0.01, Priced: true},
		{Time: at.Add(time.Second), Model: "local-llama"},
		{Time: at.Add(2 * time.Second), Model: "claude-opus-5-5", USD: 0.02, Priced: true},
	}
	c := CostOf(calls)
	require.Len(t, c.Models, 2)
	assert.Equal(t, "claude-opus-5-5", c.Models[0].Model)
	assert.True(t, c.Models[0].Priced)
	assert.False(t, c.Models[1].Priced, "a call of it had no price")

	// Summed over sessions, the one with the priced call first.
	slices.Reverse(calls)
	assert.False(t, CostOf(calls).Models[1].Priced)
	sum := newCostSum()
	sum.merge(CostOf(calls[2:]))
	sum.merge(CostOf(calls[1:2]))
	sum.merge(CostOf(calls[:1]))
	total := sum.c.sorted()
	require.Len(t, total.Models, 2)
	assert.False(t, total.Models[1].Priced)
}

// A fork's own calls follow those it copied: they are a part of its calls,
// not a copy; calls of another session among them make a copy.
func TestOwnCallsShareTheCalls(t *testing.T) {
	t.Parallel()
	parent, fork := &sessionCost{}, &sessionCost{}
	call := func(key string) ModelCall { return ModelCall{Model: "m", key: key} }
	fork.calls = []ModelCall{call("a"), call("b"), call("c"), call(""), call("d")}
	owner := map[string]*sessionCost{"a": parent, "b": parent, "c": fork, "d": fork}
	own := fork.ownCalls(owner)
	require.Len(t, own, 3)
	assert.Same(t, &fork.calls[2], &own[0], "shared")
	assert.Equal(t, 3, cap(own), "appending to it leaves the calls alone")

	owner["c"] = parent
	owner["b"] = fork
	own = fork.ownCalls(owner)
	assert.Equal(t, []string{"b", "", "d"}, []string{own[0].key, own[1].key, own[2].key})
	assert.NotSame(t, &fork.calls[1], &own[0], "a copy")
}
