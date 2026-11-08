package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/sessions"
)

// The spend adds up the hours that end within the last day and week.
func TestSpendOf(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 30, 0, 0, time.UTC)
	hour := func(ago time.Duration, usd float64, tokens pricing.Tokens) sessions.HourCost {
		return sessions.HourCost{Hour: now.Add(-ago).Truncate(time.Hour), USD: usd, Tokens: tokens}
	}
	spend := spendOf([]sessions.HourCost{
		hour(8*24*time.Hour, 100, pricing.Tokens{Input: 1e6}),                // more than a week ago
		hour(3*24*time.Hour, 20.004, pricing.Tokens{Input: 100, Output: 50}), // this week
		hour(25*time.Hour, 5, pricing.Tokens{CacheRead: 9000}),               // ends 24h30m ago: this week only
		hour(23*time.Hour+59*time.Minute, 1.5, pricing.Tokens{CacheWrite: 300, CacheWrite1h: 200}),
		hour(0, 0.25, pricing.Tokens{Input: 7, CacheRead: 1000, Output: 3}), // the hour under way
		hour(time.Hour, 0, pricing.Tokens{Output: 40}),                      // a model without a price
	}, now)
	assert.Equal(t, instanceapi.SummarySpend{
		DayUSD: 1.75, WeekUSD: 26.75,
		DayTokens:  &instanceapi.SummaryTokens{Input: 7, Output: 43, CacheWrite: 500, CacheRead: 1000, Total: 1550},
		WeekTokens: &instanceapi.SummaryTokens{Input: 107, Output: 93, CacheWrite: 500, CacheRead: 10000, Total: 10700},
	}, spend)
	zero := &instanceapi.SummaryTokens{}
	assert.Equal(t, instanceapi.SummarySpend{DayTokens: zero, WeekTokens: zero}, spendOf(nil, now), "none: zero, not missing")
}

// The summary's spend as JSON: the tokens of each window next to its cost.
func TestSpendJSON(t *testing.T) {
	spend := instanceapi.SummarySpend{
		DayUSD: 1.5, WeekUSD: 9, Scope: "s1", UpdatedAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		DayTokens:  &instanceapi.SummaryTokens{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4, Total: 10},
		WeekTokens: &instanceapi.SummaryTokens{Input: 10, Output: 20, CacheWrite: 30, CacheRead: 40, Total: 100},
	}
	data, err := json.Marshal(instanceapi.SummaryBody{Spend: &spend})
	require.NoError(t, err)
	var body struct {
		Spend map[string]any `json:"spend"`
	}
	require.NoError(t, json.Unmarshal(data, &body))
	assert.Equal(t, map[string]any{
		"day_usd": 1.5, "week_usd": 9.0, "scope": "s1", "updated_at": "2026-11-03T12:00:00Z",
		"day_tokens":  map[string]any{"input": 1.0, "output": 2.0, "cache_write": 3.0, "cache_read": 4.0, "total": 10.0},
		"week_tokens": map[string]any{"input": 10.0, "output": 20.0, "cache_write": 30.0, "cache_read": 40.0, "total": 100.0},
	}, body.Spend)

	// An earlier release's spend has no tokens.
	old, err := json.Marshal(instanceapi.SummarySpend{DayUSD: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(old), "tokens")
}

// The summary carries the latest spend; the same spend again changes
// nothing.
func TestSummarySpend(t *testing.T) {
	e := convstate.NewEventEmitter()
	assert.Nil(t, e.Summary().Spend, "not worked out yet")
	e.SetSpend(instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 9})
	spend := e.Summary().Spend
	require.NotNil(t, spend)
	assert.Equal(t, instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 9}, *spend)
	spend.DayUSD = 99
	assert.Equal(t, 1.5, e.Summary().Spend.DayUSD, "a copy")

	e.SetSpend(instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 9, DayTokens: &instanceapi.SummaryTokens{Input: 5, Total: 5}})
	spend = e.Summary().Spend
	require.NotNil(t, spend.DayTokens)
	assert.Equal(t, 5, spend.DayTokens.Total)
	spend.DayTokens.Total = 99
	assert.Equal(t, 5, e.Summary().Spend.DayTokens.Total, "a copy of the tokens too")
}

// Two AgentAPIs of one agent in one directory add up the same sessions:
// one scope. Another agent or directory has another.
func TestSpendScope(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	work := t.TempDir()
	scope := func(agentType, cwd string) string {
		store, err := sessions.New(agentType, cwd, sessions.Config{})
		require.NoError(t, err)
		return spendScope(store)
	}
	claude := scope("claude", work)
	assert.Regexp(t, `^[0-9a-f]{16}$`, claude)
	assert.Equal(t, claude, scope("claude", work))
	assert.NotEqual(t, claude, scope("codex", work))
	assert.NotEqual(t, claude, scope("claude", t.TempDir()))
}

// The spend says when its amounts last changed: worked out again to the
// same amounts, it is the same, and the summary doesn't change.
func TestNextSpend(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 500, time.UTC)
	first := nextSpend(instanceapi.SummarySpend{}, instanceapi.SummarySpend{DayUSD: 1, WeekUSD: 2}, "s1", at)
	assert.Equal(t, instanceapi.SummarySpend{DayUSD: 1, WeekUSD: 2, Scope: "s1", UpdatedAt: at.Truncate(time.Second)}, first)
	same := nextSpend(first, instanceapi.SummarySpend{DayUSD: 1, WeekUSD: 2}, "s1", at.Add(time.Minute))
	assert.Equal(t, first, same)
	more := nextSpend(same, instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 2}, "s1", at.Add(2*time.Minute))
	assert.Equal(t, at.Add(2*time.Minute).Truncate(time.Second), more.UpdatedAt)

	// The tokens alone change too: calls to a model without a price.
	tokens := nextSpend(more, instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 2, DayTokens: &instanceapi.SummaryTokens{Output: 40, Total: 40}}, "s1", at.Add(3*time.Minute))
	assert.Equal(t, at.Add(3*time.Minute).Truncate(time.Second), tokens.UpdatedAt)
	again := nextSpend(tokens, instanceapi.SummarySpend{DayUSD: 1.5, WeekUSD: 2, DayTokens: &instanceapi.SummaryTokens{Output: 40, Total: 40}}, "s1", at.Add(4*time.Minute))
	assert.Equal(t, tokens.UpdatedAt, again.UpdatedAt)

	e := convstate.NewEventEmitter()
	e.SetSpend(first)
	e.SetSpend(same)
	assert.Equal(t, first, *e.Summary().Spend)
}
