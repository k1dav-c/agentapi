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
	"github.com/coder/agentapi/lib/providerlog"
)

// callProviders are the providers of calls, in their order.
func callProviders(calls []ModelCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Provider
	}
	return out
}

// providerCosts are the calls and tokens of each provider of c, in its
// order.
func providerCosts(c SessionCost) []string {
	out := make([]string, len(c.Providers))
	for i, p := range c.Providers {
		out[i] = fmt.Sprintf("%s:%d:%d", p.Provider, p.Calls, tokenTotal(p.Tokens))
	}
	return out
}

// A Claude Code call goes to the provider the ledger last recorded its
// session on before it: a session that switched splits its calls by
// time; a subagent's calls are its session's; a fork's copy of an earlier
// session's call counts in that one, on its provider; a session the
// ledger doesn't know of, and a call before its first entry, are the
// agent's own.
func TestClaudeCallsByProvider(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now().Add(-time.Hour),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 0, 0, 0, 100),
		claudeCall("s1", "2026-10-20T10:30:00.000Z", "msg_2", 20, 0, 0, 0, 100),
		claudeCall("s1", "2026-10-20T11:30:00.000Z", "msg_3", 30, 0, 0, 0, 100))
	writeLog(t, filepath.Join(dir, "s1", "subagents", "agent-a1.jsonl"), time.Now(),
		claudeCall("s1", "2026-10-20T10:40:00.000Z", "msg_sub", 40, 0, 0, 0, 100))
	// A fork of s1, later: it copies msg_1 and makes a call of its own.
	writeLog(t, filepath.Join(dir, "s2.jsonl"), time.Now(),
		claudePromptLine("s2", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s2", "2026-10-20T10:00:05.000Z", "msg_1", 10, 0, 0, 0, 100),
		claudeCall("s2", "2026-10-20T12:00:00.000Z", "msg_4", 50, 0, 0, 0, 100))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "s2.jsonl"), time.Now(), time.Now()))
	writeLog(t, filepath.Join(dir, "s3.jsonl"), time.Now(),
		claudePromptLine("s3", "2026-10-21T10:00:00.000Z", "Elsewhere"),
		claudeCall("s3", "2026-10-21T10:00:05.000Z", "msg_5", 60, 0, 0, 0, 100))

	ledger := providerlog.Open(filepath.Join(t.TempDir(), providerlog.FileName), providerlog.Options{})
	at := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return tm
	}
	for _, e := range []providerlog.Entry{
		{At: at("2026-10-20T10:20:00Z"), Agent: "claude", Session: "s1", Provider: "gw"},
		{At: at("2026-10-20T11:00:00Z"), Agent: "claude", Session: "s1", Provider: "default"},
		{At: at("2026-10-20T11:50:00Z"), Agent: "claude", Session: "s2", Provider: "local"},
		// Another agent's session of the same ID counts nothing here.
		{At: at("2026-10-20T09:00:00Z"), Agent: "codex", Session: "s3", Provider: "gw"},
	} {
		require.NoError(t, ledger.Record(e))
	}
	store, err := New("claude", "/w", Config{Providers: ledger})
	require.NoError(t, err)
	d, err := store.Costs()
	require.NoError(t, err)

	// msg_1 before the switch, msg_2 after it, msg_3 after the switch
	// back, then the subagent's call, made on gw.
	assert.Equal(t, []string{"", "gw", "", "gw"}, callProviders(d.Sessions["s1"]))
	assert.Equal(t, []string{"local"}, callProviders(d.Sessions["s2"]), "the fork's own call; the copy counts in s1")
	assert.Equal(t, []string{""}, callProviders(d.Sessions["s3"]))
	assert.Equal(t, []string{"gw:2:260", "default:2:240"}, providerCosts(d.Cost("s1")))
	assert.Equal(t, []string{"local:1:150"}, providerCosts(d.Cost("s2")))
	total := d.Total
	assert.Equal(t, []string{"default:3:400", "gw:2:260", "local:1:150"}, providerCosts(total))
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	assert.InDelta(t, opus.Cost(pricing.Tokens{Input: 60, Output: 200}), total.Providers[1].USD, 1e-12)
	assert.True(t, total.Providers[1].Priced)
	gwHours := total.Providers[1].Hourly()
	require.Len(t, gwHours, 1)
	assert.Equal(t, time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), gwHours[0].Hour)
	assert.Equal(t, pricing.Tokens{Input: 60, Output: 200}, gwHours[0].Tokens)
	assert.Len(t, total.Providers[0].Hourly(), 3, "the agent's own: three hours")

	// A switch the ledger records later attributes the calls after it
	// again, though no log changed; what was returned before stays.
	before := d.Sessions["s3"]
	require.NoError(t, ledger.Record(providerlog.Entry{At: at("2026-10-21T10:00:00Z"), Agent: "claude", Session: "s3", Provider: "gw"}))
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, []string{"gw"}, callProviders(d.Sessions["s3"]))
	assert.Equal(t, []string{""}, callProviders(before))
	assert.Equal(t, []string{"gw:3:420", "default:2:240", "local:1:150"}, providerCosts(d.Total))

	// A call appended after another switch: read incrementally, as a new
	// Store reads it.
	require.NoError(t, ledger.Record(providerlog.Entry{At: at("2026-10-20T12:10:00Z"), Agent: "claude", Session: "s1", Provider: "local"}))
	appendLines(t, filepath.Join(dir, "s1.jsonl"), claudeCall("s1", "2026-10-20T12:30:00.000Z", "msg_6", 70, 0, 0, 0, 100)+"\n")
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, []string{"", "gw", "", "local", "gw"}, callProviders(d.Sessions["s1"]))
	fresh, err := New("claude", "/w", Config{Providers: ledger})
	require.NoError(t, err)
	want, err := fresh.Costs()
	require.NoError(t, err)
	assert.Equal(t, want, d)

	// Without the ledger, every call is the agent's own.
	plain, err := newStore(t, "claude", "/w").Costs()
	require.NoError(t, err)
	assert.Equal(t, []string{"", "", "", "", ""}, callProviders(plain.Sessions["s1"]))
	assert.Equal(t, []string{"default:7:980"}, providerCosts(plain.Total))
}

// A Codex call goes to the provider its log names; the ledger counts
// nothing.
func TestCodexCallsByProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "20")
	count := func(at string, total, last int) string {
		return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":10},"last_token_usage":{"input_tokens":%d,"output_tokens":10}}}}`, at, total, last)
	}
	writeLog(t, filepath.Join(day, "rollout-1-t1.jsonl"), time.Now(),
		`{"timestamp":"2026-10-20T10:00:00.000Z","type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w","model_provider":"agentapi-gw"}}`,
		`{"timestamp":"2026-10-20T10:00:01.000Z","type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`,
		`{"timestamp":"2026-10-20T10:00:01.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Go"}]}}`,
		count("2026-10-20T10:00:02.000Z", 1000, 1000),
		`{"timestamp":"2026-10-20T10:00:03.000Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_id":"t1","thread_settings":{"model":"gpt-6.1-sol","model_provider_id":"openai"}}}`,
		count("2026-10-20T10:00:04.000Z", 3000, 2000),
	)
	ledger := providerlog.Open(filepath.Join(t.TempDir(), providerlog.FileName), providerlog.Options{})
	require.NoError(t, ledger.Record(providerlog.Entry{At: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), Agent: "codex", Session: "t1", Provider: "other"}))
	store, err := New("codex", "/w", Config{Providers: ledger})
	require.NoError(t, err)
	d, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, []string{"gw", ""}, callProviders(d.Sessions["t1"]))
	assert.Equal(t, []string{"default:1:2000", "gw:1:1010"}, providerCosts(d.Total))
}

// The calls' provider is in their digest only when it isn't the agent's
// own: the digests of calls without one stay what they were.
func TestCallsDigestOfProviders(t *testing.T) {
	call := ModelCall{Time: time.Unix(100, 0), Model: "m", Tokens: pricing.Tokens{Input: 1}}
	own := CallsDigest([]ModelCall{call})
	call.Provider = "gw"
	assert.NotEqual(t, own, CallsDigest([]ModelCall{call}))
}

// SessionCost's providers add up across sessions as its models do.
func TestProviderCostsMerge(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := CostOf([]ModelCall{
		{Time: at, Model: "m", Tokens: pricing.Tokens{Input: 10}, USD: 1, Priced: true, Provider: "gw"},
		{Time: at.Add(time.Hour), Model: "m", Tokens: pricing.Tokens{Input: 5}, USD: 0.5, Priced: true},
	})
	b := CostOf([]ModelCall{
		{Time: at.Add(time.Minute), Model: "x", Tokens: pricing.Tokens{Input: 7}, Provider: "gw"},
		{Time: at.Add(2 * time.Hour), Model: "m", Tokens: pricing.Tokens{Input: 1}, USD: 2, Priced: true, Provider: "local"},
	})
	sum := newCostSum()
	sum.merge(a)
	sum.merge(b)
	total := sum.c.sorted()
	assert.Equal(t, []string{"local:1:1", "gw:2:17", "default:1:5"}, providerCosts(total))
	gw := total.Providers[1]
	assert.False(t, gw.Priced, "a call without a price")
	assert.InDelta(t, 1, gw.USD, 1e-12)
	assert.Equal(t, []HourCost{{Hour: at, USD: 1, Tokens: pricing.Tokens{Input: 17}}}, gw.Hourly())
	assert.Equal(t, []ProviderCost{}, CostOf(nil).Providers, "none: empty, not null")
}
