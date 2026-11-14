package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/instanceapi"
)

func spendOf(scope string, hours ...instanceapi.SummaryHour) *instanceapi.SummarySpend {
	return &instanceapi.SummarySpend{Scope: scope, Hours: hours}
}

func hourOf(at time.Time, usd float64, input int) instanceapi.SummaryHour {
	return instanceapi.SummaryHour{Hour: at.UTC().Truncate(time.Hour), USD: usd, Tokens: instanceapi.SummaryTokens{Input: input, Total: input}}
}

// seenService has a job whose run on machine a started an hour before now.
func seenService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	s, now := newService(t, nil)
	job := &Job{Key: "acme/widget#1", Host: "a", State: JobWorking, Round: 1, Runs: []Run{{Round: 1, Agent: "claude", Host: "a", InstanceID: "a~acme~widget~1", ServerID: "server-1", StartedAt: now.Add(-time.Hour)}}}
	s.mu.Lock()
	s.jobs[job.Key] = job
	s.hosts["a"] = &Host{HostInfo: host.HostInfo{Hostname: "a", URL: "http://192.0.2.1:3291"}}
	s.mu.Unlock()
	return s, now
}

// The run's spend is what its AgentAPI's summary says of its hours, and
// its work time what the hub saw: a task event the hub missed lowers
// neither.
func TestSawSpend(t *testing.T) {
	ctx := context.Background()
	s, now := seenService(t)
	start := now.Add(-time.Hour)
	// One task's event reached the hub.
	s.taskEvent("a~acme~widget~1", "server-1", "task.completed", json.RawMessage(`{"duration_seconds": 60, "cost": {"usd": 0.5, "calls": 2, "tokens": {"input": 10}}}`))

	s.Saw(ctx, Sighting{
		InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100",
		Spend: spendOf("scope-1",
			hourOf(start.Add(-2*time.Hour), 9, 900), // before the run
			hourOf(start, 1.25, 100), hourOf(*now, 2, 200)),
		Work: []WorkSpan{
			{Start: start.Add(-time.Hour), End: start.Add(-30 * time.Minute)}, // before the run
			{Start: start.Add(-time.Minute), End: start.Add(10 * time.Minute)},
			{Start: start.Add(20 * time.Minute), End: start.Add(25 * time.Minute)},
		},
	})
	run := s.job("acme/widget#1").Runs[0]
	assert.InDelta(t, 3.25, run.Cost.USD, 1e-9)
	assert.Equal(t, int64(300), run.Cost.Tokens.Input)
	assert.Equal(t, 1, run.Cost.Tasks, "tasks come from the events")
	assert.Equal(t, 2, run.Cost.Calls)
	assert.InDelta(t, 15*60, run.Cost.WorkSeconds, 1e-9, "from the run's start")
	assert.InDelta(t, 0.5, run.Events.USD, 1e-9)

	// The same spans again add nothing; an hour that grows is replaced.
	s.Saw(ctx, Sighting{
		InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100",
		Spend: spendOf("scope-1", hourOf(start, 1.25, 100), hourOf(*now, 3, 300)),
		Work: []WorkSpan{
			{Start: start.Add(-time.Minute), End: start.Add(10 * time.Minute)},
			{Start: start.Add(20 * time.Minute), End: start.Add(25 * time.Minute)},
			{Start: start.Add(40 * time.Minute), End: start.Add(50 * time.Minute)},
		},
	})
	run = s.job("acme/widget#1").Runs[0]
	assert.InDelta(t, 4.25, run.Cost.USD, 1e-9)
	assert.InDelta(t, 25*60, run.Cost.WorkSeconds, 1e-9)

	// Another process at that instance ID, or one read on another
	// machine, counts nothing.
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-2", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(*now, 50, 1))})
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.9:4100", Spend: spendOf("scope-1", hourOf(*now, 50, 1))})
	assert.InDelta(t, 4.25, s.job("acme/widget#1").Runs[0].Cost.USD, 1e-9)

	// A day later the hours have left the summary: the run keeps them.
	*now = now.Add(30 * time.Hour)
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(*now, 1, 1))})
	run = s.job("acme/widget#1").Runs[0]
	assert.InDelta(t, 5.25, run.Cost.USD, 1e-9)
	assert.Len(t, run.Seen.Hours, 1)
	assert.InDelta(t, 4.25, run.Seen.Earlier.USD, 1e-9)
	// A summary that still lists an hour already final doesn't count it
	// twice.
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(start, 1.25, 100), hourOf(*now, 1, 1))})
	assert.InDelta(t, 5.25, s.job("acme/widget#1").Runs[0].Cost.USD, 1e-9)
}

// Events that add up to more than the hub read (it reached the AgentAPI
// after hours had left its summary) are what the run shows; the comment
// then says the hub may have missed some.
func TestSawLessThanEvents(t *testing.T) {
	ctx := context.Background()
	s, now := seenService(t)
	s.taskEvent("a~acme~widget~1", "server-1", "task.completed", json.RawMessage(`{"duration_seconds": 600, "cost": {"usd": 5}}`))
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(*now, 1, 1))})
	job := s.job("acme/widget#1")
	assert.InDelta(t, 5, job.Runs[0].Cost.USD, 1e-9)
	assert.InDelta(t, 600, job.Runs[0].Cost.WorkSeconds, 1e-9)
	job.Runs[0].Missed = true
	assert.Contains(t, costComment(job, time.UTC), "hub 未收到部分事件")

	// One whose spend the hub read misses nothing.
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(*now, 6, 1))})
	job = s.job("acme/widget#1")
	job.Runs[0].Missed = true
	assert.InDelta(t, 6, job.Runs[0].Cost.USD, 1e-9)
	assert.NotContains(t, costComment(job, time.UTC), "hub 未收到部分事件")
}

// A run that follows one of the same agent in the worktree reports the
// hour they share: it is the later run's, not counted twice.
func TestSawSharedHour(t *testing.T) {
	ctx := context.Background()
	s, now := seenService(t)
	start := now.Add(-time.Hour)
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-1", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(start, 1, 1), hourOf(*now, 2, 1))})
	s.mu.Lock()
	job := s.jobs["acme/widget#1"]
	job.Runs[0].EndedAt, job.Runs[0].EndReason = *now, "replaced"
	job.Runs = append(job.Runs, Run{Round: 1, Agent: "claude", Host: "a", InstanceID: "a~acme~widget~1", ServerID: "server-2", StartedAt: *now})
	s.mu.Unlock()
	s.Saw(ctx, Sighting{InstanceID: "a~acme~widget~1", ServerID: "server-2", URL: "http://192.0.2.1:4100", Spend: spendOf("scope-1", hourOf(start, 1, 1), hourOf(*now, 2.5, 1))})
	got := s.job("acme/widget#1")
	assert.InDelta(t, 1, got.Runs[0].Cost.USD, 1e-9)
	assert.InDelta(t, 2.5, got.Runs[1].Cost.USD, 1e-9)
}

// A dispatch.json of a hub before 5.5 has the events' cost as the run's:
// it becomes the run's Events.
func TestLoadEventsCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	data := `{"version": 1, "jobs": [{"key": "acme/widget#1", "repo": "acme/widget", "issue": 1, "state": "done", "runs": [
		{"round": 1, "agent": "claude", "host": "a", "instance_id": "x", "started_at": "2026-11-08T09:00:00Z", "ended_at": "2026-11-08T10:00:00Z", "cost": {"usd": 2, "tasks": 3, "work_seconds": 60}}]}]}`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	s, err := New(Config{StatePath: path})
	require.NoError(t, err)
	t.Cleanup(s.Close)
	run := s.job("acme/widget#1").Runs[0]
	assert.Equal(t, Cost{USD: 2, Tasks: 3, WorkSeconds: 60}, run.Events)
	assert.Equal(t, run.Events, run.Cost)
}

// The comment of a round that ended says how long it took, from its first
// run's start to its last one's end.
func TestCostCommentElapsed(t *testing.T) {
	start := time.Date(2026, 11, 14, 8, 19, 0, 0, time.UTC)
	job := &Job{Round: 1, Runs: []Run{
		{Round: 1, Agent: "claude", Host: "a", StartedAt: start, EndedAt: start.Add(time.Hour), Cost: Cost{USD: 1}},
		{Round: 1, Agent: "codex", Host: "a", StartedAt: start.Add(time.Hour), EndedAt: start.Add(6*time.Hour + 15*time.Minute), Cost: Cost{USD: 1}},
	}}
	taipei := time.FixedZone("CST", 8*3600)
	assert.Contains(t, costComment(job, taipei), "總經歷時間：6 小時 15 分（11/14 16:19 開始，11/14 22:34 結束）")

	job.Runs[1].EndedAt = time.Time{}
	assert.NotContains(t, costComment(job, taipei), "總經歷時間", "not while a run goes on")
}
