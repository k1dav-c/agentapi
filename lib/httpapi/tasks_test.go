package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
)

func taskMessages(start time.Time, entries ...string) []conv.Message {
	msgs := make([]conv.Message, len(entries))
	for i, entry := range entries {
		role := conv.RoleAgent
		if text, ok := strings.CutPrefix(entry, ">"); ok {
			role, entry = conv.RoleUser, text
		}
		msgs[i] = conv.Message{Id: i, Role: role, Message: entry, Time: start.Add(time.Duration(i) * time.Second)}
	}
	return msgs
}

func TestTaskTracker(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	tr := newTaskTracker(start)
	stable := StatusChangeBody{Status: AgentStatusStable, Lifecycle: LifecycleReady}
	running := StatusChangeBody{Status: AgentStatusRunning, Lifecycle: LifecycleRunning}
	status := func(next StatusChangeBody, at time.Time) []int {
		changed, _ := tr.statusChanged(next, at)
		return changed
	}
	status(stable, start)

	// The message arrives before the agent runs.
	msgs := taskMessages(start, "", ">Fix the bug")
	assert.Equal(t, []int{0}, tr.messages(msgs))
	require.Len(t, tr.tasks, 1)
	assert.Equal(t, Task{ID: 1, Status: TaskStatusRunning, Prompt: "Fix the bug", StartedAt: msgs[1].Time}, tr.tasks[0])
	assert.Empty(t, status(stable, start), "it hasn't run yet")
	assert.Empty(t, status(running, start))

	// Steering adds to it; the reply is the agent's last message.
	msgs = taskMessages(start, "", ">Fix the bug", "Looking", ">Also the test", "Fixed both")
	assert.Equal(t, []int{0}, tr.messages(msgs))
	require.Len(t, tr.tasks, 1)
	assert.Equal(t, []int{3}, tr.tasks[0].Steered)
	assert.Equal(t, 4, *tr.tasks[0].ReplyID)

	// Waiting for an approval isn't the end.
	assert.Empty(t, status(StatusChangeBody{Status: AgentStatusStable, PendingRequests: 1}, start))
	assert.Empty(t, status(running, start))
	assert.Empty(t, tr.agentError(ErrorBody{Message: "warned", Level: conv.ErrorLevelWarning}))
	end := start.Add(time.Minute)
	assert.Equal(t, []int{0}, status(stable, end))
	assert.Equal(t, TaskStatusCompleted, tr.tasks[0].Status)
	assert.Equal(t, end, *tr.tasks[0].EndedAt)
	assert.Equal(t, 1, *tr.taskAt(start.Add(30 * time.Second)))

	// A task an error or a usage limit ends.
	msgs = append(msgs, taskMessages(start, "", "", "", "", "", ">Deploy")[5])
	assert.Equal(t, []int{1}, tr.messages(msgs))
	status(running, end)
	assert.Equal(t, []int{1}, tr.agentError(ErrorBody{Message: "boom", Level: conv.ErrorLevelError}))
	assert.Equal(t, 5, *tr.taskAt(start), "a rich message goes to the running task")
	status(stable, end)
	assert.Equal(t, TaskStatusFailed, tr.tasks[1].Status)
	assert.Equal(t, []string{"boom"}, tr.tasks[1].Errors)

	msgs = append(msgs, conv.Message{Id: 6, Role: conv.RoleUser, Message: "Go on", Time: end})
	tr.messages(msgs)
	status(running, end)
	limit := &native.LimitStop{Message: "limit", StoppedAt: end.Add(time.Second)}
	status(StatusChangeBody{Status: AgentStatusStable, LimitStop: limit}, end.Add(time.Second))
	assert.Equal(t, TaskStatusLimited, tr.tasks[2].Status)
	assert.Equal(t, limit, tr.tasks[2].Limit)

	// The agent exiting fails the running task.
	msgs = append(msgs, conv.Message{Id: 7, Role: conv.RoleUser, Message: "Again", Time: end.Add(time.Minute)})
	tr.messages(msgs)
	status(running, end)
	status(StatusChangeBody{Status: AgentStatusStable, Lifecycle: LifecycleExited}, end)
	assert.Equal(t, TaskStatusFailed, tr.tasks[3].Status)
	assert.Equal(t, []string{"the agent exited"}, tr.tasks[3].Errors)

	// A message taken back takes its task along.
	msgs = msgs[:7]
	tr.messages(msgs)
	assert.Len(t, tr.tasks, 3)
}

// A conversation from before (a resumed session) has its tasks done.
func TestTaskTrackerHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	tr := newTaskTracker(now.Add(-time.Hour))
	tr.reset(now)
	history := taskMessages(now.Add(-time.Hour), "Hi", ">One", "Done one", ">Two", "Done two")
	assert.Equal(t, []int{0, 1}, tr.messages(history))
	require.Len(t, tr.tasks, 2)
	for i, want := range []struct{ id, reply int }{{1, 2}, {3, 4}} {
		assert.Equal(t, TaskStatusCompleted, tr.tasks[i].Status)
		assert.Equal(t, want.id, tr.tasks[i].ID)
		assert.Equal(t, want.reply, *tr.tasks[i].ReplyID)
		assert.Equal(t, history[want.reply].Time, *tr.tasks[i].EndedAt, "ends with its reply")
	}
	assert.Equal(t, 3, *tr.taskAt(now.Add(-time.Hour + 3*time.Second + time.Millisecond)))
	assert.Nil(t, tr.taskAt(now.Add(-2*time.Hour)))

	// A task that was running when AgentAPI stopped is over after a restart.
	restored := []Task{{ID: 1, Status: TaskStatusRunning, StartedAt: now}}
	assert.Equal(t, []int{0}, tr.restore(restored, now.Add(time.Minute)))
	assert.Equal(t, TaskStatusCompleted, tr.tasks[0].Status)
}

// An answer to a request leaves the agent idle for a moment before it works
// again: the task ends only if it stays idle.
func TestTaskTrackerWaitsAfterAnAnswer(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	tr := newTaskTracker(start)
	tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start)
	tr.messages(taskMessages(start, ">Deploy"))
	tr.statusChanged(StatusChangeBody{Status: AgentStatusRunning}, start)
	// Waiting on an approval.
	changed, confirm := tr.statusChanged(StatusChangeBody{Status: AgentStatusStable, PendingRequests: 1}, start)
	assert.Empty(t, changed)
	assert.False(t, confirm)
	// Answered: idle, but likely to work again.
	changed, confirm = tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start)
	assert.Empty(t, changed)
	assert.True(t, confirm)
	// It works again: the grace period ends nothing.
	tr.statusChanged(StatusChangeBody{Status: AgentStatusRunning}, start)
	assert.Empty(t, tr.endIfIdle(start))
	changed, _ = tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start.Add(time.Minute))
	assert.Equal(t, []int{0}, changed)

	// An answer after which the turn was over: the task ends once the
	// grace period passed.
	tr.messages(taskMessages(start, ">Deploy", "Done", ">Again"))
	tr.statusChanged(StatusChangeBody{Status: AgentStatusRunning}, start)
	tr.statusChanged(StatusChangeBody{Status: AgentStatusStable, PendingRequests: 1}, start)
	_, confirm = tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start)
	require.True(t, confirm)
	assert.Equal(t, []int{1}, tr.endIfIdle(start.Add(taskEndGrace)))
	assert.Equal(t, TaskStatusCompleted, tr.tasks[1].Status)
}
