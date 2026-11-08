package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
)

func TestMessageQueue(t *testing.T) {
	t.Parallel()
	at := time.Unix(1_700_000_000, 0).UTC()
	var q messageQueue
	assert.NotNil(t, q.Messages(), "an empty queue is listed as []")
	a := q.Push("a", at, false)
	b := q.Push("b", at, false)
	first := q.Push("first", at, true)
	assert.Equal(t, []int{1, 2, 3}, []int{a.ID, b.ID, first.ID})
	assert.Equal(t, []convstate.QueuedMessage{first, a, b}, q.Messages())
	head, ok := q.Head()
	require.True(t, ok)
	assert.Equal(t, first, head)

	assert.True(t, q.Update(a.ID, "a2"))
	assert.False(t, q.Update(99, "x"))
	got, ok := q.Get(a.ID)
	require.True(t, ok)
	assert.Equal(t, "a2", got.Content)

	assert.False(t, q.Reorder([]int{b.ID, a.ID}), "every message must be listed")
	assert.False(t, q.Reorder([]int{b.ID, a.ID, a.ID}), "each once")
	assert.True(t, q.Reorder([]int{b.ID, a.ID, first.ID}))
	assert.Equal(t, []int{b.ID, a.ID, first.ID}, ids(q.Messages()))

	assert.True(t, q.Remove(a.ID))
	assert.False(t, q.Remove(a.ID))
	assert.Equal(t, 2, q.Len())

	assert.Equal(t, 1, q.Failed(b.ID))
	assert.Equal(t, 2, q.Failed(b.ID))
	assert.Equal(t, 1, q.Failed(first.ID), "another message starts over")
}

// After a restart empties the queue, new messages don't get the IDs of
// messages a client may still show: editing one of those must not change a
// new message.
func TestQueueIDsGoOnAfterARestart(t *testing.T) {
	t.Parallel()
	s, _ := newQuietNativeServer(t, nil)
	old := s.queue.Push("old", s.clock.Now(), false)
	s.mu.Lock()
	require.NoError(t, s.restartLocked())
	s.mu.Unlock()
	assert.Zero(t, s.queue.Len())
	fresh := s.queue.Push("new", s.clock.Now(), false)
	assert.Greater(t, fresh.ID, old.ID)
	edit := &UpdateQueuedMessageRequest{ID: old.ID}
	edit.Body.Content = "changed"
	_, err := s.updateQueuedMessage(t.Context(), edit)
	assert.Equal(t, 404, statusOf(t, err))
	got, _ := s.queue.Get(fresh.ID)
	assert.Equal(t, "new", got.Content)
}

func TestQueueRestoreKeepsCounting(t *testing.T) {
	t.Parallel()
	var q messageQueue
	q.Restore([]convstate.QueuedMessage{{ID: 7, Content: "x"}}, 5)
	assert.Equal(t, 7, q.lastID, "an ID stored with a message counts too")
	assert.Equal(t, 8, q.Push("y", time.Time{}, false).ID)
}

func ids(messages []convstate.QueuedMessage) []int {
	out := make([]int, len(messages))
	for i, m := range messages {
		out[i] = m.ID
	}
	return out
}

// Reads answer while s.mu is held for a slow call into the agent, such as
// a restart that updates the agent CLI.
func TestReadsDontWaitForTheServerLock(t *testing.T) {
	t.Parallel()
	s, _ := newQuietNativeServer(t, nil)
	s.queue.Push("queued", s.clock.Now(), false)
	s.mu.Lock()
	defer s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		queue, err := s.getQueue(t.Context(), nil)
		assert.NoError(t, err)
		assert.Len(t, queue.Body.Messages, 1)
		_, err = s.getMessages(t.Context(), nil)
		assert.NoError(t, err)
		_, err = s.getTitle(t.Context(), nil)
		assert.NoError(t, err)
		_, err = s.getStatus(t.Context(), nil)
		assert.NoError(t, err)
		// The human reply protocol's offer: nothing while a message waits.
		pending, err := s.getPendingHandoff(t.Context(), nil)
		assert.NoError(t, err)
		assert.Nil(t, pending.Body.Request)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a read waited for s.mu")
	}
}

// Clients follow the queue through queue_update events.
func TestEmitterQueueUpdates(t *testing.T) {
	t.Parallel()
	e := convstate.NewEventEmitter()
	var q messageQueue
	q.onChange = func(queue []convstate.QueuedMessage, _ int, version uint64) { e.SetQueue(queue, version) }
	_, ch, _ := e.SubscribeTo(convstate.AllEvents, 0, "")
	at := time.Now()
	first := q.Push("one", at, false)
	q.Push("two", at, false)
	q.Update(first.ID, "uno")
	q.Remove(first.ID)
	var queues [][]string
	for _, event := range drain(ch) {
		update, ok := event.Payload.(convstate.QueueUpdateBody)
		require.True(t, ok)
		var contents []string
		for _, m := range update.Queue {
			contents = append(contents, m.Content)
		}
		queues = append(queues, contents)
	}
	assert.Equal(t, [][]string{{"one"}, {"one", "two"}, {"uno", "two"}, {"two"}}, queues)

	// A change reported late, after a newer one, is dropped.
	e.SetQueue(nil, 1)
	assert.Empty(t, drain(ch))

	// The replay holds the queue.
	_, _, replay := e.SubscribeSince(0, "")
	var replayed *convstate.QueueUpdateBody
	for _, event := range replay {
		if update, ok := event.Payload.(convstate.QueueUpdateBody); ok {
			replayed = &update
		}
	}
	require.NotNil(t, replayed)
	require.Len(t, replayed.Queue, 1)
	assert.Equal(t, "two", replayed.Queue[0].Content)
}

// drain returns the events waiting in ch.
func drain(ch <-chan convstate.Event) []convstate.Event {
	var events []convstate.Event
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		default:
			return events
		}
	}
}
