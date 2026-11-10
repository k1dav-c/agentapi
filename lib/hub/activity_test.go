package hub

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// dayOf is the day date of body; it fails the test when body hasn't it.
func dayOf(t *testing.T, body activityBody, date string) activityDay {
	t.Helper()
	for _, day := range body.Days {
		if day.Date == date {
			return day
		}
	}
	require.Failf(t, "no such day", "%s", date)
	return activityDay{}
}

// spendSummary is a summary of scope that lists hours.
func spendSummary(status instanceapi.AgentStatus, scope string, hours ...instanceapi.SummaryHour) instanceapi.SummaryBody {
	return instanceapi.SummaryBody{Status: status, Attention: instanceapi.AttentionNone, Spend: &instanceapi.SummarySpend{Scope: scope, Hours: hours}}
}

func spendHour(at time.Time, usd float64, input int) instanceapi.SummaryHour {
	return instanceapi.SummaryHour{Hour: at, USD: usd, Tokens: instanceapi.SummaryTokens{Input: input, Total: input}}
}

// The work time of each span that ends goes into the hours it covers; the
// span going on counts up to the request.
func TestActivityWork(t *testing.T) {
	clock := workClock(t) // 09:00 UTC
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	clock.Advance(50 * time.Minute)
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(20 * time.Minute)
	summarize(t, s, "dev:1", idleSummary)
	today := dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03")
	assert.Equal(t, int64(1200), today.WorkSeconds)
	assert.Equal(t, int64(600), s.activity.hours[time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC).Unix()].work, "split across the hours")

	// Going on: counted as of the request, and once it ends, once.
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(5 * time.Minute)
	assert.Equal(t, int64(1500), dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03").WorkSeconds)
	summarize(t, s, "dev:1", idleSummary)
	assert.Equal(t, int64(1500), dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03").WorkSeconds)

	// Two instances add up.
	_, _ = s.update("dev:2", true, func(i *Instance) { i.URL = "http://dev:2" })
	summarize(t, s, "dev:1", workingSummary)
	summarize(t, s, "dev:2", workingSummary)
	clock.Advance(time.Minute)
	assert.Equal(t, int64(1620), dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03").WorkSeconds)

	// Forgetting one at work keeps what it did.
	require.True(t, s.remove("dev:2"))
	assert.Equal(t, int64(1620), dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03").WorkSeconds)
}

// A hub that starts again on its files counts no work twice: the work the
// instances' logs have that the activity doesn't, and nothing else. The
// first start with an activity counts the week the logs have.
func TestActivityWorkRestart(t *testing.T) {
	clock := workClock(t)
	path := filepath.Join(t.TempDir(), "instances.json")
	s, err := newStore(path, slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(10 * time.Minute)
	summarize(t, s, "dev:1", idleSummary)
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(5 * time.Minute)
	// Killed: the span going on ends where the hub last heard of the
	// instance, which these summaries don't say, so it doesn't count.
	require.NoError(t, s.flush())

	again, err := newStore(path, slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	worked := dayOf(t, again.activityDays(time.UTC, 1), "2026-11-03").WorkSeconds
	assert.Equal(t, int64(600), worked)
	require.NoError(t, again.close())

	// An activity file lost: the logs' week comes back.
	require.NoError(t, os.Remove(activityPath(path)))
	third, err := newStore(path, slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	assert.Equal(t, worked, dayOf(t, third.activityDays(time.UTC, 1), "2026-11-03").WorkSeconds)
	require.NoError(t, third.close())
}

// A scope's spend in an hour is what its latest summary says of it:
// growing within the hour, the same from two instances of one scope, and
// nothing from a summary without hours.
func TestActivitySpend(t *testing.T) {
	clock := workClock(t) // 09:00 UTC
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c", "old"} {
		_, _ = s.update(id, true, func(i *Instance) { i.URL = "http://" + id })
	}
	eight, nine := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	summarize(t, s, "a", spendSummary(instanceapi.AgentStatusStable, "s1", spendHour(eight, 1, 100), spendHour(nine, 0.5, 10)))
	summarize(t, s, "b", spendSummary(instanceapi.AgentStatusStable, "s1", spendHour(eight, 1, 100), spendHour(nine, 0.5, 10)))
	summarize(t, s, "c", spendSummary(instanceapi.AgentStatusStable, "s2", spendHour(nine, 2, 1000)))
	old := spendSummary(instanceapi.AgentStatusStable, "s3")
	old.Spend.DayUSD = 50
	summarize(t, s, "old", old)
	day := dayOf(t, s.activityDays(time.UTC, 1), "2026-11-03")
	assert.InDelta(t, 3.5, day.USD, 1e-9)
	assert.Equal(t, instanceapi.SummaryTokens{Input: 1110, Total: 1110}, day.Tokens)

	// The hour grows.
	summarize(t, s, "a", spendSummary(instanceapi.AgentStatusStable, "s1", spendHour(eight, 1, 100), spendHour(nine, 0.75, 20)))
	day = dayOf(t, s.activityDays(time.UTC, 1), "2026-11-03")
	assert.InDelta(t, 3.75, day.USD, 1e-9)
	assert.Equal(t, 1120, day.Tokens.Total)

	// Two days on, the hours are final: the totals stay, and a summary that
	// still lists them doesn't count them again.
	clock.Advance(49 * time.Hour)
	summarize(t, s, "b", spendSummary(instanceapi.AgentStatusStable, "s1", spendHour(eight, 1, 100), spendHour(nine, 0.9, 30)))
	day = dayOf(t, s.activityDays(time.UTC, 7), "2026-11-03")
	assert.InDelta(t, 3.75, day.USD, 1e-9)
	assert.Empty(t, s.activity.recent)
	assert.Equal(t, "2026-11-03", s.activityDays(time.UTC, 7).Since)
}

// The days are those of the time zone asked for: an hour counts in the
// day it starts in there.
func TestActivityTimeZone(t *testing.T) {
	a := newActivity("", slog.New(slog.DiscardHandler), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	now := time.Date(2026, 11, 3, 20, 30, 0, 0, time.UTC)
	a.setSpend("s", []instanceapi.SummaryHour{
		spendHour(time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC), 1, 1),
		spendHour(time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC), 2, 2),
	}, now)
	taipei, err := time.LoadLocation("Asia/Taipei")
	require.NoError(t, err)
	body := a.days(taipei, 3, now, nil)
	assert.Equal(t, "Asia/Taipei", body.TimeZone)
	require.Len(t, body.Days, 3)
	assert.Equal(t, []string{"2026-11-02", "2026-11-03", "2026-11-04"}, []string{body.Days[0].Date, body.Days[1].Date, body.Days[2].Date})
	assert.InDelta(t, 1.0, body.Days[1].USD, 1e-9, "15:00 UTC is 23:00 in Taipei")
	assert.InDelta(t, 2.0, body.Days[2].USD, 1e-9, "16:00 UTC is midnight")
	assert.Equal(t, "2026-11-03", body.Since)
}

// The activity keeps a year and a day; the scopes it keeps apart are
// bounded.
func TestActivityPrune(t *testing.T) {
	start := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	a := newActivity("", slog.New(slog.DiscardHandler), start)
	a.addWork("dev:1", start, start.Add(time.Hour), start)
	a.setSpend("s", []instanceapi.SummaryHour{spendHour(start, 1, 1)}, start)
	later := start.Add(activityKeep + 2*time.Hour)
	a.addWork("dev:1", later.Add(-time.Hour), later, later)
	assert.Len(t, a.hours, 1, "the year-old hours went")
	assert.Empty(t, a.recent)

	for n := range maxActivityScopes + 5 {
		a.setSpend(string(rune('a'+n%26))+time.Duration(n).String(), []instanceapi.SummaryHour{spendHour(later.Truncate(time.Hour), 1, 1)}, later)
	}
	assert.Len(t, a.recent, maxActivityScopes)
}

// The activity's file: written owner-only, read back the same, with a
// copy of what was read; one that doesn't parse moves aside.
func TestActivityFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.json")
	now := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	a := newActivity(path, slog.New(slog.DiscardHandler), now)
	a.addWork("dev:1", now.Add(-2*time.Hour), now, now)
	a.setSpend("s", []instanceapi.SummaryHour{spendHour(now.Truncate(time.Hour), 1.25, 40)}, now)
	a.setSpend("old", []instanceapi.SummaryHour{spendHour(now.Add(-47*time.Hour).Truncate(time.Hour), 2, 2)}, now)
	require.NoError(t, a.close())
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	read := newActivity(path, slog.New(slog.DiscardHandler), now)
	assert.Equal(t, a.days(time.UTC, 3, now, nil), read.days(time.UTC, 3, now, nil))
	assert.Equal(t, a.counted["dev:1"].Unix(), read.counted["dev:1"].Unix())
	_, err = os.Stat(path + ".bak")
	require.NoError(t, err)
	require.NoError(t, read.close())

	// Read two days on: the old scope's hour is final.
	later := newActivity(path, slog.New(slog.DiscardHandler), now.Add(24*time.Hour))
	assert.NotContains(t, later.recent, "old")
	require.Contains(t, later.hours, now.Add(-47*time.Hour).Truncate(time.Hour).Unix())
	assert.InDelta(t, 2.0, later.hours[now.Add(-47*time.Hour).Truncate(time.Hour).Unix()].usd, 1e-9)
	require.NoError(t, later.close())

	require.NoError(t, os.WriteFile(path, []byte("{nope"), 0o600))
	fresh := newActivity(path, slog.New(slog.DiscardHandler), now)
	assert.Empty(t, fresh.hours)
	matches, err := filepath.Glob(path + ".corrupt-*")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	require.NoError(t, fresh.close())
}
