package httpapi

import (
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
)

// summaries returns the summaries among events.
func summaries(t *testing.T, events []Event) []SummaryBody {
	t.Helper()
	var out []SummaryBody
	for _, event := range events {
		require.Equal(t, EventTypeSummaryUpdate, event.Type)
		out = append(out, event.Payload.(SummaryUpdateBody).SummaryBody)
	}
	return out
}

func TestSummary(t *testing.T) {
	t.Parallel()
	e := NewEventEmitter(WithInstance(func() WebhookInstance { return WebhookInstance{ID: "host:3284", Name: "repo"} }))
	e.EmitStatus(conv.StatusStable)
	summary := e.Summary()
	assert.Equal(t, WebhookInstance{ID: "host:3284", Name: "repo"}, summary.Instance)
	assert.Equal(t, AttentionNone, summary.Attention)
	assert.Empty(t, summary.Title)
	assert.Nil(t, summary.Task)

	// The title is the latest task's prompt until the agent names the
	// session.
	start := time.Now()
	e.EmitMessages(taskMessages(start, "", ">Fix the bug in the parser\nand add a test"))
	e.EmitStatus(conv.StatusChanging)
	summary = e.Summary()
	assert.Equal(t, AgentStatusRunning, summary.Status)
	require.NotNil(t, summary.Task)
	assert.Equal(t, TaskStatusRunning, summary.Task.Status)
	assert.Equal(t, 1, summary.Tasks)
	assert.Equal(t, "Fix the bug in the parser and add a test", summary.Title)
	e.SetSessionName("Parser fix")
	assert.Equal(t, "Parser fix", e.Summary().Title)

	// What the agent waits for, the most pressing first.
	e.EmitStatus(conv.StatusStable)
	e.SetLimitStop(&native.LimitStop{Message: "limit", StoppedAt: start})
	assert.Equal(t, AttentionLimit, e.Summary().Attention)
	e.EmitRequest(native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusPending})
	assert.Equal(t, AttentionRequest, e.Summary().Attention)
	assert.Equal(t, 1, e.Summary().PendingRequests)
	e.SetLoginRequired(&native.LoginRequired{Agent: "Claude Code", Command: "claude auth login"})
	assert.Equal(t, AttentionLogin, e.Summary().Attention)
	e.SetLoginRequired(nil)
	e.EmitRequest(native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusResolved})
	e.SetLimitStop(nil)
	assert.Equal(t, AttentionNone, e.Summary().Attention)
	e.SetLifecycle(LifecycleExited)
	assert.Equal(t, AttentionExited, e.Summary().Attention)
}

// The summary goes only to subscriptions that ask for it, once when they
// subscribe and then when it changes.
func TestSummaryEvents(t *testing.T) {
	t.Parallel()
	e := NewEventEmitter()
	e.EmitStatus(conv.StatusStable)
	_, others, _ := e.SubscribeTo(ConversationEvents, 0, "")
	_, ch, replay := e.SubscribeTo(SummaryEvents, 0, "")
	require.Len(t, summaries(t, replay), 1)
	assert.Equal(t, AgentStatusStable, summaries(t, replay)[0].Status)

	start := time.Now()
	e.EmitMessages(taskMessages(start, "", ">Fix the bug"))
	e.EmitStatus(conv.StatusChanging)
	got := summaries(t, drain(ch))
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, AgentStatusRunning, last.Status)
	require.NotNil(t, last.Task)
	assert.Equal(t, "Fix the bug", last.Task.Prompt)

	// The agent's reply growing changes the conversation, not the summary.
	e.EmitMessages(taskMessages(start, "", ">Fix the bug", "Looking"))
	got = summaries(t, drain(ch))
	require.NotEmpty(t, got, "the task has a reply")
	require.NotNil(t, got[len(got)-1].Task.ReplyID)
	e.EmitMessages(taskMessages(start, "", ">Fix the bug", "Looking at it"))
	e.EmitScreen("Looking at it")
	assert.Empty(t, drain(ch))

	// A reset leaves nothing: the summary says so.
	e.Reset()
	got = summaries(t, drain(ch))
	require.NotEmpty(t, got)
	assert.Nil(t, got[len(got)-1].Task)
	assert.Zero(t, got[len(got)-1].Tasks)

	// The instance changing outside the conversation is sent when told.
	e.instance = func() WebhookInstance { return WebhookInstance{ID: "renamed"} }
	e.SummaryChanged()
	got = summaries(t, drain(ch))
	require.Len(t, got, 1)
	assert.Equal(t, "renamed", got[0].Instance.ID)
	e.SummaryChanged()
	assert.Empty(t, drain(ch), "nothing changed")

	for _, event := range drain(others) {
		assert.NotEqual(t, EventTypeSummaryUpdate, event.Type)
	}
}

// A task that ends after a client last showed the conversation is unread
// until one shows it again.
func TestSummaryUnread(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	e := NewEventEmitter(WithClock(clock))
	e.EmitStatus(conv.StatusStable)
	_, ch, _ := e.SubscribeTo(SummaryEvents, 0, "")

	e.EmitMessages(taskMessages(clock.Now(), "", ">Fix the bug"))
	e.EmitStatus(conv.StatusChanging)
	assert.False(t, e.Summary().Unread, "a running task")
	clock.Advance(time.Minute)
	e.EmitStatus(conv.StatusStable)
	assert.True(t, e.Summary().Unread)
	got := summaries(t, drain(ch))
	assert.True(t, got[len(got)-1].Unread, "summary clients hear of it")

	clock.Advance(time.Second)
	seen := e.MarkSeen()
	assert.False(t, seen.Unread)
	assert.Equal(t, clock.Now(), seen.SeenAt)
	got = summaries(t, drain(ch))
	require.Len(t, got, 1)
	assert.False(t, got[0].Unread)
}

// Restored tasks are read unless the state database says when they were
// last seen.
func TestSummaryUnreadRestored(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	task := Task{ID: 0, Status: TaskStatusCompleted, Prompt: "Fix the bug", StartedAt: start, EndedAt: &end}
	state := EmitterState{
		Messages:    taskMessages(start, ">Fix the bug", "Fixed"),
		MessageSeqs: []uint64{1, 2},
		Tasks:       []Task{task},
		TaskSeqs:    []uint64{3},
		Seq:         3,
	}
	e := NewEventEmitter()
	e.Restore(state)
	assert.False(t, e.Summary().Unread, "a database that never recorded it")

	state.SeenAt = start
	e = NewEventEmitter()
	e.Restore(state)
	assert.True(t, e.Summary().Unread, "it ended after it was last seen")
}
