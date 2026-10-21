package pricing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookup(t *testing.T) {
	t.Parallel()
	table := Builtin()
	for model, want := range map[string]float64{
		"claude-opus-5-5":            4,
		"Claude-Opus-5-5":            4,
		"anthropic/claude-opus-5-5":  4,
		"claude-opus-5-5[1m]":        4,
		"claude-haiku-4-5-20251001":  1,
		"claude-sonnet-5-5-20261001": 2,
		"gpt-6.1-sol":                2,
		"gpt-6.1-sol-2026-09-01":     2,
		"gpt-5.4-mini":               0.75, // not gpt-5.4's
	} {
		p, ok := table.Lookup(model)
		require.True(t, ok, model)
		assert.InDelta(t, want, p.Input, 1e-9, model)
	}
	_, ok := table.Lookup("<synthetic>")
	assert.False(t, ok)
	_, ok = table.Lookup("kimi-k2")
	assert.False(t, ok)
}

func TestCost(t *testing.T) {
	t.Parallel()
	opus, _ := Builtin().Lookup("claude-opus-5-5")
	// A long Claude Code session: 1-hour cache writes cost twice the input.
	usage := Tokens{Input: 1012, Output: 408622, CacheRead: 225504598, CacheWrite1h: 3264363}
	// Claude Code itself counted $79.22 for it.
	assert.InDelta(t, 79.39, opus.Cost(usage), 0.01)
	assert.InDelta(t, 1.25*4/1e6*1000, opus.Cost(Tokens{CacheWrite: 1000}), 1e-12)

	// A long input takes the higher tier for the whole call.
	sol, _ := Builtin().Lookup("gpt-6.1-sol")
	short := Tokens{Input: 100_000, Output: 1000}
	long := Tokens{Input: 100_000, CacheRead: 200_000, Output: 1000}
	assert.InDelta(t, (100_000*2+1000*10)/1e6, sol.Cost(short), 1e-9)
	assert.InDelta(t, (100_000*4+200_000*0.2+1000*15)/1e6, sol.Cost(long), 1e-9)
}

func TestWithFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "prices.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "claude-opus-5-5": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75},
  "my-model": {"input": 1, "output": 2, "cache_read": 0, "cache_write": 0}
}`), 0o600))
	table, err := Builtin().WithFile(path)
	require.NoError(t, err)
	p, _ := table.Lookup("claude-opus-5-5")
	assert.InDelta(t, 3, p.Input, 1e-9, "replaced")
	_, ok := table.Lookup("my-model")
	assert.True(t, ok, "added")
	_, ok = table.Lookup("gpt-6.1-sol")
	assert.True(t, ok, "the built-in prices stay")

	require.NoError(t, os.WriteFile(path, []byte(`{not json`), 0o600))
	_, err = Builtin().WithFile(path)
	assert.Error(t, err)
}
