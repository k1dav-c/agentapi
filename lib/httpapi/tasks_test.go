package httpapi

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
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

// feed gives tr the messages it is called with as the emitter does: with
// the position of the first that changed since the last call.
func feed(tr *taskTracker) func([]conv.Message) []int {
	var last []conv.Message
	return func(msgs []conv.Message) []int {
		from := len(msgs)
		for i := range msgs {
			if i >= len(last) || last[i] != msgs[i] {
				from = i
				break
			}
		}
		last = msgs
		return tr.messages(msgs, from)
	}
}

func TestTaskTracker(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	tr := newTaskTracker(start)
	take := feed(tr)
	stable := StatusChangeBody{Status: AgentStatusStable, Lifecycle: LifecycleReady}
	running := StatusChangeBody{Status: AgentStatusRunning, Lifecycle: LifecycleRunning}
	status := func(next StatusChangeBody, at time.Time) []int {
		changed, _ := tr.statusChanged(next, at)
		return changed
	}
	status(stable, start)

	// The message arrives before the agent runs.
	msgs := taskMessages(start, "", ">Fix the bug")
	assert.Equal(t, []int{0}, take(msgs))
	require.Len(t, tr.tasks, 1)
	assert.Equal(t, Task{ID: 1, Status: TaskStatusRunning, Prompt: "Fix the bug", StartedAt: msgs[1].Time}, tr.tasks[0])
	assert.Empty(t, status(stable, start), "it hasn't run yet")
	assert.Empty(t, status(running, start))

	// Steering adds to it; the reply is the agent's last message.
	msgs = taskMessages(start, "", ">Fix the bug", "Looking", ">Also the test", "Fixed both")
	assert.Equal(t, []int{0}, take(msgs))
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
	assert.Equal(t, []int{1}, take(msgs))
	status(running, end)
	assert.Equal(t, []int{1}, tr.agentError(ErrorBody{Message: "boom", Level: conv.ErrorLevelError}))
	assert.Equal(t, 5, *tr.taskAt(start), "a rich message goes to the running task")
	status(stable, end)
	assert.Equal(t, TaskStatusFailed, tr.tasks[1].Status)
	assert.Equal(t, []string{"boom"}, tr.tasks[1].Errors)

	msgs = append(msgs, conv.Message{Id: 6, Role: conv.RoleUser, Message: "Go on", Time: end})
	take(msgs)
	status(running, end)
	limit := &native.LimitStop{Message: "limit", StoppedAt: end.Add(time.Second)}
	status(StatusChangeBody{Status: AgentStatusStable, LimitStop: limit}, end.Add(time.Second))
	assert.Equal(t, TaskStatusLimited, tr.tasks[2].Status)
	assert.Equal(t, limit, tr.tasks[2].Limit)

	// The agent exiting fails the running task.
	msgs = append(msgs, conv.Message{Id: 7, Role: conv.RoleUser, Message: "Again", Time: end.Add(time.Minute)})
	take(msgs)
	status(running, end)
	status(StatusChangeBody{Status: AgentStatusStable, Lifecycle: LifecycleExited}, end)
	assert.Equal(t, TaskStatusFailed, tr.tasks[3].Status)
	assert.Equal(t, []string{"the agent exited"}, tr.tasks[3].Errors)

	// A message taken back takes its task along.
	msgs = msgs[:7]
	take(msgs)
	assert.Len(t, tr.tasks, 3)
}

// A conversation from before (a resumed session) has its tasks done.
func TestTaskTrackerHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	tr := newTaskTracker(now.Add(-time.Hour))
	tr.reset(now)
	history := taskMessages(now.Add(-time.Hour), "Hi", ">One", "Done one", ">Two", "Done two")
	assert.Equal(t, []int{0, 1}, tr.messages(history, 0))
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
	take := feed(tr)
	tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start)
	take(taskMessages(start, ">Deploy"))
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
	take(taskMessages(start, ">Deploy", "Done", ">Again"))
	tr.statusChanged(StatusChangeBody{Status: AgentStatusRunning}, start)
	tr.statusChanged(StatusChangeBody{Status: AgentStatusStable, PendingRequests: 1}, start)
	_, confirm = tr.statusChanged(StatusChangeBody{Status: AgentStatusStable}, start)
	require.True(t, confirm)
	assert.Equal(t, []int{1}, tr.endIfIdle(start.Add(taskEndGrace)))
	assert.Equal(t, TaskStatusCompleted, tr.tasks[1].Status)
}

// Taking only the messages from the first that changed derives the same
// tasks as going through them all, whatever the changes.
func TestTaskTrackerIncremental(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	statuses := []StatusChangeBody{
		{Status: AgentStatusRunning, Lifecycle: LifecycleRunning},
		{Status: AgentStatusStable, Lifecycle: LifecycleReady},
		{Status: AgentStatusStable, Lifecycle: LifecycleReady, PendingRequests: 1},
		{Status: AgentStatusStable, Lifecycle: LifecycleReady, TerminalPrompt: "1. Yes"},
		{Status: AgentStatusStable, Lifecycle: LifecycleExited},
	}
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 0))
		incremental, all := newTaskTracker(start), newTaskTracker(start)
		take := feed(incremental)
		var msgs []conv.Message
		nextID := 0
		now := start.Add(-time.Minute)
		for step := range 300 {
			now = now.Add(time.Second)
			msgs = slices.Clone(msgs)
			switch op := rng.IntN(20); {
			case op < 4:
				msgs = append(msgs, conv.Message{Id: nextID, Role: conv.RoleUser, Message: fmt.Sprint("ask ", step), Time: now})
				nextID++
			case op < 8:
				text := ""
				if rng.IntN(4) > 0 {
					text = fmt.Sprint("reply ", step)
				}
				msgs = append(msgs, conv.Message{Id: nextID, Role: conv.RoleAgent, Message: text, Time: now})
				nextID++
			case op < 12 && len(msgs) > 0:
				// The latest message grows.
				msgs[len(msgs)-1].Message += " more"
			case op < 13 && len(msgs) > 0:
				// Messages taken back.
				msgs = msgs[:rng.IntN(len(msgs))]
			case op < 14 && len(msgs) > 0:
				// One taken out from between others, whose ID goes again
				// if it was the last.
				i := rng.IntN(len(msgs))
				if msgs[i].Id == nextID-1 {
					nextID--
				}
				msgs = slices.Delete(msgs, i, i+1)
			case op < 15 && len(msgs) > 0:
				// A message changes its time or role.
				i := rng.IntN(len(msgs))
				if rng.IntN(2) == 0 {
					msgs[i].Time = msgs[i].Time.Add(time.Millisecond)
				} else if msgs[i].Role == conv.RoleUser {
					msgs[i].Role = conv.RoleAgent
				} else {
					msgs[i].Role = conv.RoleUser
				}
			case op < 18:
				status := statuses[rng.IntN(len(statuses))]
				c1, ok1 := incremental.statusChanged(status, now)
				c2, ok2 := all.statusChanged(status, now)
				require.Equal(t, c2, c1, "seed %d step %d", seed, step)
				require.Equal(t, ok2, ok1)
				continue
			case op < 19:
				err := ErrorBody{Message: "boom", Level: conv.ErrorLevelError}
				require.Equal(t, all.agentError(err), incremental.agentError(err))
				continue
			default:
				require.Equal(t, all.endIfIdle(now), incremental.endIfIdle(now))
				continue
			}
			require.Equal(t, all.derive(msgs), take(msgs), "seed %d step %d", seed, step)
			require.Equal(t, all.tasks, incremental.tasks, "seed %d step %d", seed, step)
			require.Equal(t, all.open, incremental.open)
			require.Equal(t, all.ran, incremental.ran)
		}
	}
}

// A reply streams in at the end of a long session: n tasks before it, the
// last one running. An update costs the same however long the session is.
func BenchmarkEmitMessagesLongSession(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		for _, update := range []string{"reply", "unchanged"} {
			b.Run(fmt.Sprintf("tasks=%d/%s", n, update), func(b *testing.B) {
				clock := quartz.NewMock(b)
				e := NewEventEmitter(WithClock(clock))
				before := clock.Now().Add(-time.Hour)
				var msgs []conv.Message
				for i := range n {
					at := before.Add(time.Duration(i) * time.Second)
					msgs = append(msgs,
						conv.Message{Id: len(msgs), Role: conv.RoleUser, Message: "Fix the bug", Time: at},
						conv.Message{Id: len(msgs) + 1, Role: conv.RoleAgent, Message: "Looking", Time: at},
						conv.Message{Id: len(msgs) + 2, Role: conv.RoleAgent, Message: "Fixed", Time: at})
				}
				msgs = append(msgs,
					conv.Message{Id: len(msgs), Role: conv.RoleUser, Message: "Go on", Time: clock.Now()},
					conv.Message{Id: len(msgs) + 1, Role: conv.RoleAgent, Message: "Working", Time: clock.Now()})
				texts := [2]string{"Working", "Working on it"}
				if update == "unchanged" {
					texts[1] = texts[0]
				}
				// The emitter keeps the slice it gets: the next update goes
				// in the other one.
				held, next := msgs, slices.Clone(msgs)
				held[len(held)-1].Message = texts[1]
				e.EmitMessages(held)
				b.ReportAllocs()
				b.ResetTimer()
				for i := range b.N {
					next[len(next)-1].Message = texts[i%2]
					e.EmitMessages(next)
					held, next = next, held
				}
			})
		}
	}
}
