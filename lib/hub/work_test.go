package hub

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// workClock is a mock clock at a whole second, as the file keeps times.
func workClock(t *testing.T) *quartz.Mock {
	t.Helper()
	clock := quartz.NewMock(t)
	clock.Set(time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)).MustWait(t.Context())
	return clock
}

var (
	idleSummary    = instanceapi.SummaryBody{Status: instanceapi.AgentStatusStable, Attention: instanceapi.AttentionNone}
	workingSummary = instanceapi.SummaryBody{Status: instanceapi.AgentStatusRunning, Attention: instanceapi.AttentionNone}
	askingSummary  = instanceapi.SummaryBody{Status: instanceapi.AgentStatusRunning, Attention: instanceapi.AttentionRequest, Request: &instanceapi.SummaryRequest{ID: "r1"}}
)

// summarize has the store take summary of the instance id, connected.
func summarize(t *testing.T, s *store, id string, summary instanceapi.SummaryBody) Instance {
	t.Helper()
	instance, ok := s.update(id, false, func(i *Instance) {
		i.Connection = ConnectionConnected
		i.Summary = &summary
	})
	require.True(t, ok)
	return instance
}

func workOf(t *testing.T, s *store, clock quartz.Clock, id string) WorkTime {
	t.Helper()
	instance, ok := s.get(id)
	require.True(t, ok)
	return *instance.sent(clock.Now()).Work
}

// Only working counts: a span starts when the instance starts working and
// ends when it does anything else, needs you included. What the hub sends
// is the spans that ended, and since when it works now.
func TestWorkTime(t *testing.T) {
	clock := workClock(t)
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	summarize(t, s, "dev:1", idleSummary)
	clock.Advance(time.Hour)
	assert.Equal(t, WorkTime{}, workOf(t, s, clock, "dev:1"), "idle")

	start := clock.Now()
	instance := summarize(t, s, "dev:1", workingSummary)
	assert.Equal(t, instanceapi.StateWorking, instance.State)
	clock.Advance(10 * time.Minute)
	assert.Equal(t, WorkTime{WorkingSince: start}, workOf(t, s, clock, "dev:1"), "the page adds the span going on")

	summarize(t, s, "dev:1", askingSummary)
	assert.Equal(t, WorkTime{Day: 600, Week: 600}, workOf(t, s, clock, "dev:1"), "it needs you")
	clock.Advance(5 * time.Minute)
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(5 * time.Minute)
	summarize(t, s, "dev:1", idleSummary)
	assert.Equal(t, WorkTime{Day: 900, Week: 900}, workOf(t, s, clock, "dev:1"))

	// A day later, the spans are out of the 24 hours.
	clock.Advance(24 * time.Hour)
	assert.Equal(t, WorkTime{Day: 0, Week: 900}, workOf(t, s, clock, "dev:1"))
	// One that crosses the window counts its part in it.
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(2 * time.Hour)
	summarize(t, s, "dev:1", idleSummary)
	clock.Advance(23 * time.Hour)
	assert.Equal(t, WorkTime{Day: 3600, Week: 900 + 7200}, workOf(t, s, clock, "dev:1"))

	// A week on, the first ones go with the next span.
	clock.Advance(6 * 24 * time.Hour)
	assert.Equal(t, WorkTime{Week: 3600}, workOf(t, s, clock, "dev:1"))
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(time.Minute)
	summarize(t, s, "dev:1", idleSummary)
	kept, _ := s.get("dev:1")
	require.Len(t, kept.work.spans, 2, "pruned")
}

// Working while the hub doesn't see it doesn't count: connecting again
// (with its last summary) or unreachable ends the span when its stream
// last said anything, and muting doesn't apply to working.
func TestWorkTimeLostConnection(t *testing.T) {
	clock := workClock(t)
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	changed := summarize(t, s, "dev:1", workingSummary).ChangedAt

	clock.Advance(10 * time.Minute)
	heard := clock.Now()
	clock.Advance(45 * time.Second)
	dropped, _ := s.update("dev:1", false, func(i *Instance) {
		i.Connection = ConnectionConnecting
		i.Error = "the event stream went silent"
		i.heard = heard
	})
	assert.Equal(t, instanceapi.StateWorking, dropped.State, "its last summary")
	assert.Equal(t, changed, dropped.ChangedAt, "bookkeeping isn't a change of its row")
	assert.Equal(t, WorkTime{Day: 600, Week: 600}, workOf(t, s, clock, "dev:1"), "until the stream last said anything")

	clock.Advance(time.Minute)
	_, _ = s.update("dev:1", false, func(i *Instance) { i.Connection = ConnectionUnreachable })
	clock.Advance(time.Minute)
	again := summarize(t, s, "dev:1", workingSummary)
	assert.Equal(t, clock.Now(), again.work.since, "counts again from its next summary")
	clock.Advance(time.Minute)
	_, _ = s.update("dev:1", false, func(i *Instance) { i.Connection = ConnectionStopped })
	assert.Equal(t, WorkTime{Day: 660, Week: 660}, workOf(t, s, clock, "dev:1"))
}

// A restart ends the spans going on when the hub stopped; the file keeps
// them. One a killed hub left going on ends when the hub last kept the
// instance.
func TestWorkTimeAcrossRestarts(t *testing.T) {
	clock := workClock(t)
	logger := slog.New(slog.DiscardHandler)
	path := filepath.Join(t.TempDir(), "instances.json")
	s, err := newStore(path, logger, clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(20 * time.Minute)
	require.NoError(t, s.close())

	clock.Advance(time.Hour)
	again, err := newStore(path, logger, clock)
	require.NoError(t, err)
	assert.Equal(t, WorkTime{Day: 1200, Week: 1200}, workOf(t, again, clock, "dev:1"), "not the hour it was down")
	summarize(t, again, "dev:1", workingSummary)
	require.NoError(t, again.flush())

	// Killed: the file says it worked, as of when it last saw it.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file struct {
		Instances []map[string]any `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	first := float64(clock.Now().Add(-80 * time.Minute).Unix())
	assert.Equal(t, map[string]any{"spans": []any{[]any{first, first + 1200}}, "since": float64(clock.Now().Unix())}, file.Instances[0]["work"])
	clock.Advance(time.Hour)
	var kept []storedInstance
	require.NoError(t, json.Unmarshal(data, &struct {
		Instances *[]storedInstance `json:"instances"`
	}{&kept}))
	kept[0].LastSeen = clock.Now().Add(-30 * time.Minute)
	killed, err := encodeState(kept, nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, killed, 0o600))
	after, err := newStore(path, logger, clock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = after.close() })
	assert.Equal(t, WorkTime{Day: 1200 + 1800, Week: 1200 + 1800}, workOf(t, after, clock, "dev:1"))
}

// A file without work time (an earlier hub's) reads as none; a hub that
// doesn't know the field (2.4) reads a file with it as before.
func TestWorkTimeFiles(t *testing.T) {
	clock := workClock(t)
	logger := slog.New(slog.DiscardHandler)
	path := filepath.Join(t.TempDir(), "instances.json")
	earlier := `{"version": 1, "instances": [{"id": "dev:1", "url": "http://dev:1", "source": "webhook", "last_seen": "2026-11-01T09:00:00Z", "group": "Work"}]}`
	require.NoError(t, os.WriteFile(path, []byte(earlier), 0o600))
	s, err := newStore(path, logger, clock)
	require.NoError(t, err)
	assert.Equal(t, WorkTime{}, workOf(t, s, clock, "dev:1"))
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(time.Minute)
	require.NoError(t, s.close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	// The file as a 2.4 hub reads it: its envelope and instance, without
	// the fields it doesn't know.
	var old struct {
		Version   int `json:"version"`
		Instances []struct {
			record
			Stopped  bool   `json:"stopped,omitempty"`
			Activity string `json:"activity,omitempty"`
		} `json:"instances"`
	}
	require.NoError(t, json.Unmarshal(data, &old))
	assert.Equal(t, 1, old.Version)
	require.Len(t, old.Instances, 1)
	assert.Equal(t, "Work", old.Instances[0].Group)

	// Damaged spans (by hand): one that ends before it starts, or
	// overlaps the one before, is dropped.
	stored := &storedWork{Spans: [][2]int64{{100, 50}, {1000, 2000}, {1500, 2500}, {3000, 3100}}}
	log := stored.log(time.Time{}, time.Unix(4000, 0))
	assert.Equal(t, []workSpan{{time.Unix(1000, 0), time.Unix(2000, 0)}, {time.Unix(3000, 0), time.Unix(3100, 0)}}, log.spans)
}

// The store keeps at most maxWorkSpans of an instance, the newest.
func TestWorkTimeBounded(t *testing.T) {
	var w workLog
	now := time.Unix(1_000_000, 0)
	for n := range maxWorkSpans + 10 {
		w.since = now.Add(time.Duration(n) * time.Second)
		w.end(w.since.Add(time.Second/2+time.Second/4), now.Add(time.Duration(n)*time.Second))
	}
	require.Len(t, w.spans, maxWorkSpans)
	assert.Equal(t, now.Add(10*time.Second), w.spans[0].start)
}

// The subscribers hear of an instance again once its work time moved by a
// minute without a change of it (a span slides out of a window), not for
// the span going on, which the page adds itself.
func TestRefreshWork(t *testing.T) {
	clock := workClock(t)
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	summarize(t, s, "dev:1", workingSummary)
	clock.Advance(time.Hour)
	summarize(t, s, "dev:1", idleSummary)
	_, _ = s.update("dev:2", true, func(i *Instance) { i.URL = "http://dev:2" })
	summarize(t, s, "dev:2", workingSummary)
	_, _, events := s.subscribe()
	t.Cleanup(func() { s.unsubscribe(events) })

	clock.Advance(23 * time.Hour)
	s.refreshWork()
	assert.Empty(t, events, "nothing moved")
	clock.Advance(time.Second)
	s.refreshWork()
	require.Len(t, events, 1, "a span slides out")
	event := <-events
	assert.Equal(t, "dev:1", event.Instance.ID)
	assert.Equal(t, int64(3599), event.Instance.work.at(clock.Now()).Day)
	s.refreshWork()
	assert.Empty(t, events, "told once")
	clock.Advance(30 * time.Second)
	s.refreshWork()
	assert.Empty(t, events, "within the minute")
	clock.Advance(30 * time.Second)
	s.refreshWork()
	require.Len(t, events, 1)
	<-events
	assert.Empty(t, events, "told once")
}

// The browser gets the work time as JSON, with working_since only while it
// works; the token stays out.
func TestWorkTimeSent(t *testing.T) {
	clock := workClock(t)
	s, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	_, _ = s.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1"; i.Token = "secret" })
	instance := summarize(t, s, "dev:1", workingSummary)
	data, err := json.Marshal(instance.sent(clock.Now()))
	require.NoError(t, err)
	var sent map[string]any
	require.NoError(t, json.Unmarshal(data, &sent))
	assert.Equal(t, map[string]any{"last_24h_seconds": 0.0, "last_7d_seconds": 0.0, "working_since": "2026-11-03T09:00:00Z"}, sent["work"])
	assert.NotContains(t, sent, "token")

	clock.Advance(90 * time.Second)
	instance = summarize(t, s, "dev:1", idleSummary)
	data, err = json.Marshal(instance.sent(clock.Now()))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"work":{"last_24h_seconds":90,"last_7d_seconds":90}`)
	assert.NotContains(t, string(instance.seen()), `"work"`, "what browsers see of a change leaves it out")
}
