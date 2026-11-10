package convstate_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/transcript"
)

// conversation drives an emitter through tasks as the native conversation
// does: the user's message, the agent at work, a rich message and a
// timeline event of its reply, and the agent idle again.
type conversation struct {
	t     *testing.T
	e     *convstate.EventEmitter
	clock *quartz.Mock
	msgs  []conv.Message
	// taskIDs are the IDs of the tasks' messages, by task number - 1.
	taskIDs []int
}

func (c *conversation) task(n int) {
	c.clock.Advance(time.Second)
	id := len(c.msgs)
	c.taskIDs = append(c.taskIDs, id)
	c.msgs = append(c.msgs, conv.Message{Id: id, Role: conv.RoleUser, Message: fmt.Sprintf("task %d", n), Time: c.clock.Now()})
	c.e.EmitMessages(slices.Clone(c.msgs))
	c.e.EmitStatus(conv.StatusChanging)
	c.clock.Advance(time.Millisecond)
	reply := fmt.Sprintf("reply %d", n)
	c.e.EmitRichMessageSnapshot(transcript.RichMessage{
		MessageID: fmt.Sprintf("a%d", n), Role: "assistant", Timestamp: c.clock.Now(),
		Content: []transcript.RichContentBlock{{Type: "text", Text: reply}},
	})
	c.e.EmitSessionEvents([]transcript.SessionEvent{{Kind: "text", EventTime: c.clock.Now(), Content: &reply}})
	c.msgs = append(c.msgs, conv.Message{Id: id + 1, Role: conv.RoleAgent, Message: reply, Time: c.clock.Now()})
	c.e.EmitMessages(slices.Clone(c.msgs))
	c.e.EmitStatus(conv.StatusStable)
}

func newConversation(t *testing.T, db *statedb.DB, hot, recent int) *conversation {
	clock := quartz.NewMock(t)
	opts := []convstate.EventEmitterOption{convstate.WithClock(clock), convstate.WithRecentTasks(recent)}
	if db != nil {
		opts = append(opts, convstate.WithPersister(db), convstate.WithHotTasks(hot))
	}
	c := &conversation{t: t, e: convstate.NewEventEmitter(opts...), clock: clock}
	c.e.EmitStatus(conv.StatusStable)
	return c
}

// taskOf returns the number of the task a rich message belongs to.
func (c *conversation) taskOf(id *int) int {
	if id == nil {
		return 0
	}
	return slices.Index(c.taskIDs, *id) + 1
}

func richTasks(c *conversation, msgs []transcript.RichMessage) []int {
	var tasks []int
	for _, m := range msgs {
		tasks = append(tasks, c.taskOf(m.TaskID))
	}
	return tasks
}

func numbers(from, to int) []int {
	var n []int
	for i := from; i <= to; i++ {
		n = append(n, i)
	}
	return n
}

// With a state database the emitter holds the rich messages and timeline
// events of the latest tasks only, however long the conversation runs;
// the database has them all, a page at a time.
func TestHotWindow(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := statedb.Open(path, nil)
	require.NoError(t, err)
	c := newConversation(t, db, 10, 4)
	for n := 1; n <= 300; n++ {
		c.task(n)
	}

	// Memory: the last 10 tasks' rich messages and events.
	assert.Equal(t, numbers(291, 300), richTasks(c, c.e.RichMessages()))
	assert.Len(t, c.e.SessionEvents(), 10)
	tasks := c.e.Tasks()
	require.Len(t, tasks, 300)
	assert.Equal(t, 300, tasks[299].Number)

	// The latest page: 4 tasks, and more before.
	r := c.e.Page(0, 0)
	require.Len(t, r.Tasks, 4)
	assert.Equal(t, 297, r.Tasks[0].Number)
	assert.True(t, r.More)
	assert.False(t, r.Prelude)
	rich, ok := c.e.RichMessagesIn(r)
	require.True(t, ok)
	assert.Equal(t, numbers(297, 300), richTasks(c, rich))
	assert.Len(t, c.e.MessagesIn(r), 8)

	// A page of earlier tasks is read from the database.
	r = c.e.Page(r.From, 10)
	require.Len(t, r.Tasks, 10)
	assert.Equal(t, 287, r.Tasks[0].Number)
	_, ok = c.e.RichMessagesIn(r)
	assert.False(t, ok, "tasks 287 to 290 are cold")
	_, ok = c.e.SessionEventsIn(r)
	assert.False(t, ok)
	stored, err := db.RichMessagesOf(r.From, r.Before, r.Prelude)
	require.NoError(t, err)
	assert.Equal(t, numbers(287, 296), richTasks(c, stored))
	events, err := db.SessionEventsOf(r.FromTime, r.BeforeTime, r.Prelude)
	require.NoError(t, err)
	require.Len(t, events, 10)
	assert.Equal(t, "reply 287", *events[0].Content)

	// The first page reaches the start.
	r = c.e.Page(c.taskIDs[3], 10)
	require.Len(t, r.Tasks, 3)
	assert.True(t, r.Prelude)
	assert.False(t, r.More)
	stored, err = db.RichMessagesOf(r.From, r.Before, r.Prelude)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, richTasks(c, stored))

	// The database has every rich message once, in order.
	all, err := db.RichMessagesOf(0, 0, true)
	require.NoError(t, err)
	assert.Equal(t, numbers(1, 300), richTasks(c, all))
	var exported int
	require.NoError(t, db.EachSessionEvent(func([]byte) error { exported++; return nil }))
	assert.Equal(t, 300, exported)

	// After a restart the database gives back the latest tasks' rich
	// messages and events, and where they start.
	require.NoError(t, db.Close())
	db, err = statedb.Open(path, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	snap, err := db.LoadRecent(10)
	require.NoError(t, err)
	assert.Len(t, snap.Messages, 600)
	assert.Len(t, snap.Tasks, 300)
	require.Len(t, snap.RichMessages, 10)
	assert.Equal(t, 290, snap.RichBase)
	assert.Len(t, snap.Events, 10)
	assert.True(t, snap.Window.Cold)
	assert.Equal(t, c.taskIDs[290], snap.Window.Task)
	assert.Equal(t, 291, snap.Events[0].EventID)
	assert.NotZero(t, snap.Window.ColdSeq)
}

// A full replay holds the recent tasks; a client that resumes from before
// what the emitter let go of gets one too, and one that resumes after only
// what changed.
func TestHotWindowReplay(t *testing.T) {
	t.Parallel()
	db, err := statedb.Open(filepath.Join(t.TempDir(), "state.db"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	c := newConversation(t, db, 10, 4)
	for n := 1; n <= 5; n++ {
		c.task(n)
	}
	_, _, replay := c.e.SubscribeSince(0, "")
	early := replay[0].Payload.(convstate.SessionSyncBody)
	for n := 6; n <= 30; n++ {
		c.task(n)
	}
	recent := c.e.Page(0, 0)
	count := func(events []convstate.Event, kind convstate.EventType) int {
		n := 0
		for _, event := range events {
			if event.Type == kind {
				n++
			}
		}
		return n
	}

	_, _, replay = c.e.SubscribeSince(0, "")
	sync := replay[0].Payload.(convstate.SessionSyncBody)
	assert.True(t, sync.Full)
	assert.Equal(t, 8, count(replay, convstate.EventTypeMessageUpdate))
	assert.Equal(t, 4, count(replay, convstate.EventTypeRichMessageUpdate))
	assert.Equal(t, 4, count(replay, convstate.EventTypeTaskUpdate))
	last := replay[len(replay)-1].Payload.(convstate.TaskUpdateBody)
	assert.Equal(t, 30, last.Number)
	assert.Equal(t, recent.Tasks[3].ID, last.ID)

	// From before the hot window: the full replay again.
	_, _, replay = c.e.SubscribeSince(early.Seq, early.Epoch)
	assert.True(t, replay[0].Payload.(convstate.SessionSyncBody).Full)
	assert.Equal(t, 4, count(replay, convstate.EventTypeTaskUpdate))

	// From within it: what changed since, also of tasks before the recent
	// ones.
	c.task(31)
	c.task(32)
	_, _, replay = c.e.SubscribeSince(sync.Seq, sync.Epoch)
	assert.False(t, replay[0].Payload.(convstate.SessionSyncBody).Full)
	assert.Equal(t, 2, count(replay, convstate.EventTypeRichMessageUpdate))
	assert.Equal(t, 2, count(replay, convstate.EventTypeTaskUpdate))
}

// Without a state database the emitter holds everything, and pages from
// memory.
func TestHotWindowMemoryOnly(t *testing.T) {
	t.Parallel()
	c := newConversation(t, nil, 10, 4)
	for n := 1; n <= 30; n++ {
		c.task(n)
	}
	assert.Len(t, c.e.RichMessages(), 30)
	r := c.e.Page(c.taskIDs[20], 10)
	rich, ok := c.e.RichMessagesIn(r)
	require.True(t, ok)
	assert.Equal(t, numbers(11, 20), richTasks(c, rich))
	events, ok := c.e.SessionEventsIn(r)
	require.True(t, ok)
	assert.Len(t, events, 10)
}

// A conversation without tasks is all prelude.
func TestPageWithoutTasks(t *testing.T) {
	t.Parallel()
	c := newConversation(t, nil, 0, 4)
	c.e.EmitMessages([]conv.Message{{Id: 0, Role: conv.RoleAgent, Message: "Welcome"}})
	r := c.e.Page(0, 0)
	assert.True(t, r.Prelude)
	assert.False(t, r.More)
	assert.Len(t, c.e.MessagesIn(r), 1)
}
