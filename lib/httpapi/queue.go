package httpapi

import (
	"slices"
	"time"
)

// messageQueue holds the messages waiting for the agent, in the order they
// go out. IDs count up for as long as the server runs, a restart that
// empties the queue included, so a client that still has the ID of a
// message that went out can't edit or delete a newer one. It isn't safe
// for concurrent use: the server guards it with s.mu.
type messageQueue struct {
	messages []QueuedMessage
	lastID   int
	// failures counts the failed sends in a row of the message failedID.
	failedID int
	failures int
}

// Len is how many messages wait.
func (q *messageQueue) Len() int {
	return len(q.messages)
}

// Messages returns the queue, in order. It is never nil.
func (q *messageQueue) Messages() []QueuedMessage {
	return append([]QueuedMessage{}, q.messages...)
}

// LastID is the ID of the last message queued.
func (q *messageQueue) LastID() int {
	return q.lastID
}

// Push queues content, queued at at: last, or first with front.
func (q *messageQueue) Push(content string, at time.Time, front bool) QueuedMessage {
	q.lastID++
	m := QueuedMessage{ID: q.lastID, Content: content, Time: at}
	if front {
		q.messages = slices.Insert(q.messages, 0, m)
	} else {
		q.messages = append(q.messages, m)
	}
	return m
}

// Head returns the message that goes out next.
func (q *messageQueue) Head() (QueuedMessage, bool) {
	if len(q.messages) == 0 {
		return QueuedMessage{}, false
	}
	return q.messages[0], true
}

// Get returns the queued message id.
func (q *messageQueue) Get(id int) (QueuedMessage, bool) {
	i := q.index(id)
	if i < 0 {
		return QueuedMessage{}, false
	}
	return q.messages[i], true
}

// Update replaces the content of the queued message id. It reports whether
// the message is queued.
func (q *messageQueue) Update(id int, content string) bool {
	i := q.index(id)
	if i < 0 {
		return false
	}
	q.messages[i].Content = content
	return true
}

// Remove takes the message id out of the queue. It reports whether it was
// queued.
func (q *messageQueue) Remove(id int) bool {
	i := q.index(id)
	if i < 0 {
		return false
	}
	q.messages = slices.Delete(q.messages, i, i+1)
	return true
}

// Reorder puts the queue in the order of ids. It reports false, and leaves
// the queue as it is, unless ids lists every queued message once.
func (q *messageQueue) Reorder(ids []int) bool {
	if len(ids) != len(q.messages) {
		return false
	}
	byID := make(map[int]QueuedMessage, len(q.messages))
	for _, m := range q.messages {
		byID[m.ID] = m
	}
	reordered := make([]QueuedMessage, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			return false
		}
		delete(byID, id)
		reordered = append(reordered, m)
	}
	q.messages = reordered
	return true
}

// Failed counts a failed send of the message id, and returns how many
// sends of it failed in a row.
func (q *messageQueue) Failed(id int) int {
	if q.failedID != id {
		q.failedID = id
		q.failures = 0
	}
	q.failures++
	return q.failures
}

// Clear empties the queue. IDs go on counting.
func (q *messageQueue) Clear() {
	q.messages = nil
	q.failedID = 0
	q.failures = 0
}

// Restore puts back a queue stored before, whose last ID was lastID.
func (q *messageQueue) Restore(messages []QueuedMessage, lastID int) {
	q.messages = slices.Clone(messages)
	q.lastID = lastID
	for _, m := range messages {
		q.lastID = max(q.lastID, m.ID)
	}
}

func (q *messageQueue) index(id int) int {
	return slices.IndexFunc(q.messages, func(m QueuedMessage) bool { return m.ID == id })
}
