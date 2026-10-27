package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	e := NewEventEmitter()
	assert.Nil(t, e.Summary().Spend, "not worked out yet")
	e.SetSpend(SummarySpend{DayUSD: 1.5, WeekUSD: 9})
	spend := e.Summary().Spend
	require.NotNil(t, spend)
	assert.Equal(t, SummarySpend{DayUSD: 1.5, WeekUSD: 9}, *spend)
	spend.DayUSD = 99
	assert.Equal(t, 1.5, e.Summary().Spend.DayUSD, "a copy")
}
