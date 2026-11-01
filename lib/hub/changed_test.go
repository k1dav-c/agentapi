package hub

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
)

// ChangedAt moves when an instance changes what its row shows it does, and
// not for a heartbeat, what someone does on the hub's page, the figures
// that move on their own, or a stream that drops and comes back.
func TestChangedAt(t *testing.T) {
	clock := quartz.NewMock(t)
	st, err := newStore("", slog.New(slog.DiscardHandler), clock)
	require.NoError(t, err)
	first := clock.Now()
	instance, _, err := st.update("dev:1", true, func(i *Instance) { i.URL = "http://dev:1" })
	require.NoError(t, err)
	assert.Equal(t, first, instance.ChangedAt, "first seen")

	summary := instanceapi.SummaryBody{
		Instance:  instanceapi.WebhookInstance{ID: "dev:1", ServerID: "s1", Version: "2.1.0"},
		Status:    instanceapi.AgentStatusStable,
		Lifecycle: instanceapi.LifecycleReady,
		Attention: instanceapi.AttentionNone,
		Title:     "Migrate invoices",
		Task:      &instanceapi.Task{ID: 1, Status: instanceapi.TaskStatusCompleted},
	}
	at := func(change func(*Instance)) time.Time {
		t.Helper()
		clock.Advance(time.Minute)
		instance, _, err := st.update("dev:1", false, change)
		require.NoError(t, err)
		return instance.ChangedAt
	}
	connected := func(change func(*instanceapi.SummaryBody)) func(*Instance) {
		next := summary
		if change != nil {
			change(&next)
		}
		summary = next
		return func(i *Instance) {
			i.Connection = ConnectionConnected
			i.Summary = &next
			i.LastSeen = clock.Now()
		}
	}
	changed := at(connected(nil))
	assert.Equal(t, clock.Now(), changed, "its first summary: from connecting to what it does")

	steps := []struct {
		name    string
		change  func(*Instance)
		changes bool
	}{
		{"a heartbeat", func(i *Instance) { i.LastSeen = clock.Now() }, false},
		{"a group", func(i *Instance) { i.Group = "Work" }, false},
		{"pinned", func(i *Instance) { i.Pin = pinLast }, false},
		{"unpinned", func(i *Instance) { i.Pin = 0 }, false},
		{"the stream drops", func(i *Instance) { i.Connection = ConnectionConnecting; i.Error = "the stream ended" }, false},
		{"and comes back", connected(nil), false},
		{"spend, usage and reading it", connected(func(s *instanceapi.SummaryBody) {
			s.Spend = &instanceapi.SummarySpend{DayUSD: 4.2, Scope: "x", UpdatedAt: clock.Now()}
			s.Usage = &native.AgentUsage{}
			s.SeenAt = clock.Now()
		}), false},
		{"a task starts", connected(func(s *instanceapi.SummaryBody) {
			s.Status = instanceapi.AgentStatusRunning
			s.Task = &instanceapi.Task{ID: 2, Status: instanceapi.TaskStatusRunning, Prompt: "Add the tax column"}
			s.Tasks = 2
		}), true},
		{"the agent's messages", connected(func(s *instanceapi.SummaryBody) {
			task := *s.Task
			reply := 7
			task.ReplyID = &reply
			s.Task = &task
		}), false},
		{"a message queued", connected(func(s *instanceapi.SummaryBody) { s.QueuedMessages = 1 }), true},
		{"it asks", connected(func(s *instanceapi.SummaryBody) {
			s.Attention = instanceapi.AttentionRequest
			s.PendingRequests = 1
			s.Request = &instanceapi.SummaryRequest{ID: "r1", Kind: native.RequestKindApproval, Title: "make release"}
		}), true},
		{"muted", func(i *Instance) { i.Muted = instanceapi.AttentionKey(i.Summary) }, false},
		{"it asks something else", connected(func(s *instanceapi.SummaryBody) {
			s.PendingRequests = 2
		}), true},
		{"answered, and the task ends", connected(func(s *instanceapi.SummaryBody) {
			s.Status = instanceapi.AgentStatusStable
			s.Attention = instanceapi.AttentionNone
			s.PendingRequests = 0
			s.QueuedMessages = 0
			s.Request = nil
			s.Task = &instanceapi.Task{ID: 2, Status: instanceapi.TaskStatusCompleted}
			s.Unread = true
		}), true},
		{"read", connected(func(s *instanceapi.SummaryBody) { s.Unread = false; s.SeenAt = clock.Now() }), false},
		{"a new title", connected(func(s *instanceapi.SummaryBody) { s.Title = "Tax column" }), true},
		{"unreachable", func(i *Instance) { i.Connection = ConnectionUnreachable; i.Error = "connection refused" }, true},
		{"heard of while unreachable", func(i *Instance) { i.LastSeen = clock.Now(); i.Error = "timed out" }, false},
		{"back", connected(nil), true},
		{"an update waits", connected(func(s *instanceapi.SummaryBody) {
			s.Update = &instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateWaiting, Target: "2.2.0"}
		}), true},
		{"restarted in place on another version", connected(func(s *instanceapi.SummaryBody) {
			s.Instance.ServerID, s.Instance.Version = "s2", "2.2.0"
			s.Update = &instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle}
		}), true},
		{"stopped", func(i *Instance) { i.Connection = ConnectionStopped }, true},
		{"a group while stopped", func(i *Instance) { i.Group = "" }, false},
	}
	for _, step := range steps {
		got := at(step.change)
		if step.changes {
			assert.Equal(t, clock.Now(), got, step.name)
			changed = got
		} else {
			assert.Equal(t, changed, got, step.name)
		}
	}
}

// The file keeps when each instance changed, and what it did: a summary
// after a restart that says the same isn't a change. One a hub kept before
// it recorded changes gets its first with its next change.
func TestChangedAtAfterARestart(t *testing.T) {
	clock := quartz.NewMock(t)
	path := filepath.Join(t.TempDir(), "instances.json")
	logger := slog.New(slog.DiscardHandler)
	idle := instanceapi.SummaryBody{Status: instanceapi.AgentStatusStable, Attention: instanceapi.AttentionNone, Title: "Ready"}
	working := idle
	working.Status = instanceapi.AgentStatusRunning
	follow := func(s *store, id string, summary instanceapi.SummaryBody) Instance {
		t.Helper()
		instance, ok, err := s.update(id, false, func(i *Instance) {
			i.Connection = ConnectionConnected
			i.Summary = &summary
			i.LastSeen = clock.Now()
		})
		require.NoError(t, err)
		require.True(t, ok)
		return instance
	}

	s, err := newStore(path, logger, clock)
	require.NoError(t, err)
	first := clock.Now()
	for _, id := range []string{"dev:1", "dev:2"} {
		_, _, err = s.update(id, true, func(i *Instance) { i.URL = "http://" + id })
		require.NoError(t, err)
		follow(s, id, idle)
	}
	clock.Advance(time.Hour)
	_, _, err = s.update("dev:2", false, func(i *Instance) { i.Connection = ConnectionStopped })
	require.NoError(t, err)
	stopped := clock.Now()

	clock.Advance(time.Hour)
	again, err := newStore(path, logger, clock)
	require.NoError(t, err)
	kept, _ := again.get("dev:1")
	assert.Equal(t, first, kept.ChangedAt.UTC())
	assert.Equal(t, first, follow(again, "dev:1", idle).ChangedAt.UTC(), "it says what it did")
	gone, _ := again.get("dev:2")
	assert.Equal(t, stopped, gone.ChangedAt.UTC())
	clock.Advance(time.Minute)
	assert.Equal(t, clock.Now(), follow(again, "dev:1", working).ChangedAt)

	// A file of an earlier hub.
	require.NoError(t, os.WriteFile(path, []byte(`[{"id": "dev:3", "url": "http://dev:3", "source": "webhook", "last_seen": "2026-10-30T09:00:00Z"}]`), 0o600))
	earlier, err := newStore(path, logger, clock)
	require.NoError(t, err)
	assert.True(t, follow(earlier, "dev:3", idle).ChangedAt.IsZero(), "nothing to differ from")
	clock.Advance(time.Minute)
	assert.Equal(t, clock.Now(), follow(earlier, "dev:3", working).ChangedAt)
}
