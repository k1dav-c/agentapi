package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/sessions"
)

// The spend adds up the hours that end within the last day and week.
func TestSpendOf(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 30, 0, 0, time.UTC)
	hour := func(ago time.Duration, usd float64) sessions.HourCost {
		return sessions.HourCost{Hour: now.Add(-ago).Truncate(time.Hour), USD: usd}
	}
	spend := spendOf([]sessions.HourCost{
		hour(8*24*time.Hour, 100),    // more than a week ago
		hour(3*24*time.Hour, 20.004), // this week
		hour(25*time.Hour, 5),        // ends 24h30m ago: this week only
		hour(23*time.Hour+59*time.Minute, 1.5),
		hour(0, 0.25), // the hour under way
	}, now)
	assert.Equal(t, SummarySpend{DayUSD: 1.75, WeekUSD: 26.75}, spend)
	assert.Equal(t, SummarySpend{}, spendOf(nil, now))
}

// The summary carries the latest spend; the same spend again changes
// nothing.
func TestSummarySpend(t *testing.T) {
	e := convstate.NewEventEmitter()
	assert.Nil(t, e.Summary().Spend, "not worked out yet")
	e.SetSpend(SummarySpend{DayUSD: 1.5, WeekUSD: 9})
	spend := e.Summary().Spend
	require.NotNil(t, spend)
	assert.Equal(t, SummarySpend{DayUSD: 1.5, WeekUSD: 9}, *spend)
	spend.DayUSD = 99
	assert.Equal(t, 1.5, e.Summary().Spend.DayUSD, "a copy")
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
	first := nextSpend(SummarySpend{}, SummarySpend{DayUSD: 1, WeekUSD: 2}, "s1", at)
	assert.Equal(t, SummarySpend{DayUSD: 1, WeekUSD: 2, Scope: "s1", UpdatedAt: at.Truncate(time.Second)}, first)
	same := nextSpend(first, SummarySpend{DayUSD: 1, WeekUSD: 2}, "s1", at.Add(time.Minute))
	assert.Equal(t, first, same)
	more := nextSpend(same, SummarySpend{DayUSD: 1.5, WeekUSD: 2}, "s1", at.Add(2*time.Minute))
	assert.Equal(t, at.Add(2*time.Minute).Truncate(time.Second), more.UpdatedAt)

	e := convstate.NewEventEmitter()
	e.SetSpend(first)
	e.SetSpend(same)
	assert.Equal(t, first, *e.Summary().Spend)
}
