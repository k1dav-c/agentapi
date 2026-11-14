package sessions

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerlog"
)

// mutablePrices is a ProviderPrices a test changes between readings.
type mutablePrices struct {
	mu    sync.Mutex
	table provider.PriceTable
}

func (m *mutablePrices) get() provider.PriceTable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.table
}

func (m *mutablePrices) set(table provider.PriceTable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.table = table
}

// A call to a provider with a price of its model costs that, before the
// store's prices; the agent's own calls and a provider's models without a
// price cost what they did. A change of the prices prices the calls again,
// though no log changed, and a price taken away leaves the store's.
func TestCallsAtProviderPrices(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	kimi := func(line string) string {
		return strings.Replace(line, `"model":"claude-opus-5-5"`, `"model":"Kimi-K3-NVFP4"`, 1)
	}
	writeLog(t, filepath.Join(dir, "s1.jsonl"), time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Go"),
		// The agent's own, before the switch.
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 1000, 0, 0, 0, 100),
		// On gw: a model with gw's price, and one without.
		kimi(claudeCall("s1", "2026-10-20T10:30:00.000Z", "msg_2", 1_000_000, 2_000_000, 0, 0, 100_000)),
		claudeCall("s1", "2026-10-20T10:40:00.000Z", "msg_3", 1000, 0, 0, 0, 100))
	ledger := providerlog.Open(filepath.Join(t.TempDir(), providerlog.FileName), providerlog.Options{})
	require.NoError(t, ledger.Record(providerlog.Entry{At: time.Date(2026, 10, 20, 10, 20, 0, 0, time.UTC), Agent: "claude", Session: "s1", Provider: "gw"}))
	prices := &mutablePrices{}
	store, err := New("claude", "/w", Config{Providers: ledger, ProviderPrices: prices.get})
	require.NoError(t, err)
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	own := opus.Cost(pricing.Tokens{Input: 1000, Output: 100})

	d, err := store.Costs()
	require.NoError(t, err)
	calls := d.Sessions["s1"]
	require.Len(t, calls, 3)
	assert.False(t, calls[1].Priced, "no price of Kimi-K3-NVFP4 yet")
	assert.InDelta(t, 2*own, d.Total.USD, 1e-12)
	before := d

	prices.set(provider.PriceTable{"gw": {
		"kimi-k3-nvfp4": {Input: 0.19, Output: 16, CacheRead: 0.18, Source: "openrouter:moonshotai/kimi-k3"},
		// Not a model gw served: the agent's own claude-opus-5-5 keeps
		// the store's price.
		"claude-opus-5-6": {Input: 1, Output: 1},
	}})
	d, err = store.Costs()
	require.NoError(t, err)
	calls = d.Sessions["s1"]
	assert.Equal(t, []string{"", "gw", "gw"}, callProviders(calls))
	assert.True(t, calls[1].Priced)
	kimiUSD := 0.19 + 2*0.18 + 0.1*16
	assert.InDelta(t, kimiUSD, calls[1].USD, 1e-9)
	assert.InDelta(t, own, calls[2].USD, 1e-12, "gw has no price of claude-opus-5-5: the store's")
	assert.InDelta(t, 2*own+kimiUSD, d.Total.USD, 1e-9)
	assert.True(t, d.Total.Providers[0].Priced)
	assert.Equal(t, "gw", d.Total.Providers[0].Provider)
	assert.NotEqual(t, CallsDigest(before.Sessions["s1"]), CallsDigest(calls), "a client asks for the calls again")
	assert.False(t, before.Sessions["s1"][1].Priced, "what was returned before stays")

	// Unchanged prices: the last reading.
	again, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, d, again)

	// A new price, then none.
	prices.set(provider.PriceTable{"gw": {"Kimi-K3-NVFP4": {Input: 1, Output: 1}}})
	d, err = store.Costs()
	require.NoError(t, err)
	assert.InDelta(t, 1.1, d.Sessions["s1"][1].USD, 1e-9)
	prices.set(nil)
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, before, d)
}

// A Codex call names its provider in the log, and is priced at it.
func TestCodexCallsAtProviderPrices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "20")
	writeLog(t, filepath.Join(day, "rollout-1-t1.jsonl"), time.Now(),
		`{"timestamp":"2026-10-20T10:00:00.000Z","type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w","model_provider":"agentapi-gw"}}`,
		`{"timestamp":"2026-10-20T10:00:01.000Z","type":"turn_context","payload":{"model":"Kimi-K3.1-Pro-NVFP4"}}`,
		`{"timestamp":"2026-10-20T10:00:01.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Go"}]}}`,
		`{"timestamp":"2026-10-20T10:00:02.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":1000000},"last_token_usage":{"input_tokens":1000000,"output_tokens":1000000}}}}`,
	)
	table := provider.PriceTable{"gw": {"Kimi-K3.1-Pro-NVFP4": {Input: 2, Output: 10}}}
	store, err := New("codex", "/w", Config{ProviderPrices: func() provider.PriceTable { return table }})
	require.NoError(t, err)
	d, err := store.Costs()
	require.NoError(t, err)
	require.Len(t, d.Sessions["t1"], 1)
	assert.True(t, d.Sessions["t1"][0].Priced)
	assert.InDelta(t, 12.0, d.Total.USD, 1e-9)
}
