package httpapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	st "github.com/coder/agentapi/lib/screentracker"
)

// messageQueue holds the messages waiting for the agent, in the order they
// go out. IDs count up for as long as the server runs, a restart that
// empties the queue included, so a client that still has the ID of a
// message that went out can't edit or delete a newer one.
//
// Its methods are safe for concurrent use, so GET /queue doesn't wait for
// a slow call into the agent. Whatever changes the queue holds s.mu too,
// so a check and the change it leads to are one step. Its lock is taken
// last, and nothing is called with it held.
type messageQueue struct {
	mu       sync.Mutex
	messages []QueuedMessage
	lastID   int
	// failures counts the failed sends in a row of the message failedID.
	failedID int
	failures int
}

// Len is how many messages wait.
func (q *messageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// Messages returns the queue, in order. It is never nil.
func (q *messageQueue) Messages() []QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]QueuedMessage{}, q.messages...)
}

// LastID is the ID of the last message queued.
func (q *messageQueue) LastID() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastID
}

// Snapshot returns the queue and the last ID as of one moment.
func (q *messageQueue) Snapshot() ([]QueuedMessage, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]QueuedMessage{}, q.messages...), q.lastID
}

// Push queues content, queued at at: last, or first with front.
func (q *messageQueue) Push(content string, at time.Time, front bool) QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
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
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return QueuedMessage{}, false
	}
	return q.messages[0], true
}

// Get returns the queued message id.
func (q *messageQueue) Get(id int) (QueuedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.index(id)
	if i < 0 {
		return QueuedMessage{}, false
	}
	return q.messages[i], true
}

// Update replaces the content of the queued message id. It reports whether
// the message is queued.
func (q *messageQueue) Update(id int, content string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
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
	q.mu.Lock()
	defer q.mu.Unlock()
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
	q.mu.Lock()
	defer q.mu.Unlock()
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
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.failedID != id {
		q.failedID = id
		q.failures = 0
	}
	q.failures++
	return q.failures
}

// Clear empties the queue. IDs go on counting.
func (q *messageQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = nil
	q.failedID = 0
	q.failures = 0
}

// Restore puts back a queue stored before, whose last ID was lastID.
func (q *messageQueue) Restore(messages []QueuedMessage, lastID int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = slices.Clone(messages)
	q.lastID = lastID
	for _, m := range messages {
		q.lastID = max(q.lastID, m.ID)
	}
}

// index returns the position of the message id, or -1. Caller holds q.mu.
func (q *messageQueue) index(id int) int {
	return slices.IndexFunc(q.messages, func(m QueuedMessage) bool { return m.ID == id })
}

const (
	// messageQueueDispatchInterval is how often the queue dispatch loop
	// checks whether the head of the queue can be sent to the agent.
	messageQueueDispatchInterval = 500 * time.Millisecond
	// maxQueueDispatchAttempts is how many consecutive non-transient send
	// failures are tolerated before a queued message is dropped.
	maxQueueDispatchAttempts = 5
)

// getQueue handles GET /queue, without s.mu (see getTitle): the queue has a
// lock of its own.
func (s *Server) getQueue(ctx context.Context, input *struct{}) (*QueueResponse, error) {
	resp := &QueueResponse{}
	resp.Body.Messages = s.queue.Messages()
	return resp, nil
}

func (s *Server) updateQueuedMessage(ctx context.Context, input *UpdateQueuedMessageRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content := strings.TrimSpace(input.Body.Content)
	if content == "" {
		return nil, huma.Error400BadRequest("message must not be empty")
	}
	if !s.queue.Update(input.ID, content) {
		return nil, huma.Error404NotFound("queued message not found")
	}
	return queueMutated(), nil
}

func (s *Server) deleteQueuedMessage(ctx context.Context, input *DeleteQueuedMessageRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.queue.Remove(input.ID) {
		return nil, huma.Error404NotFound("queued message not found")
	}
	return queueMutated(), nil
}

// reorderQueue puts the queued messages in the given order.
func (s *Server) reorderQueue(_ context.Context, input *ReorderQueueRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.queue.Reorder(input.Body.IDs) {
		return nil, huma.Error409Conflict("the queue changed; list every queued message once")
	}
	return queueMutated(), nil
}

func queueMutated() *QueueMutationResponse {
	resp := &QueueMutationResponse{}
	resp.Body.Ok = true
	return resp
}

// queueMessageLocked queues content, first with front, and says so in resp.
// Caller holds s.mu.
func (s *Server) queueMessageLocked(resp *MessageResponse, content string, front bool) {
	s.queue.Push(content, s.clock.Now(), front)
	resp.Body.Ok = true
	resp.Body.Queued = true
}

// startMessageQueue starts the queue dispatch loop, which runs until Stop.
// Dispatch is driven by polling rather than by subscribing to status
// events: an event subscriber channel is closed by the emitter when it
// fills up (which a slow conversation.Send call could cause), and status
// events don't fire again when a message fails transient validation while
// the agent stays stable. Polling is immune to both.
func (s *Server) startMessageQueue() {
	s.queueLoop = s.clock.TickerFunc(s.shutdownCtx, messageQueueDispatchInterval, func() error {
		s.beforeQueue()
		s.dispatchNextQueuedMessage()
		s.persistQueue()
		return nil
	}, "messageQueueDispatch")
}

// beforeQueue gives the transport's backend its turn on the queue's tick,
// before the queue (see agentBackend.beforeQueue).
func (s *Server) beforeQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restarting {
		return
	}
	s.backend().beforeQueue()
}

// dispatchNextQueuedMessage sends the head of the queue once the agent can
// take it. A message the agent refuses is tried again on later ticks and
// dropped after maxQueueDispatchAttempts, or at once when sending it again
// could deliver it twice.
func (s *Server) dispatchNextQueuedMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restarting {
		// The messages wait for the new agent.
		return
	}
	next, ok := s.queue.Head()
	backend := s.backend()
	if !ok || !backend.readyForQueued() {
		return
	}
	err := backend.sendQueued(next.Content)
	switch {
	case err == nil:
		s.queue.Remove(next.ID)
		return
	case errors.Is(err, st.ErrMessageValidationChanging):
		// The agent got busy again: on a later tick.
		return
	}
	attempts := s.queue.Failed(next.ID)
	s.logger.Error("Failed to send queued message", "queueId", next.ID, "attempt", attempts, "error", err)
	if reason := backend.notResent(err); reason != "" {
		s.queue.Remove(next.ID)
		s.emitter.EmitError(fmt.Sprintf("Queued message %q %s: %v", shortText(next.Content, 40), reason, err), st.ErrorLevelError)
		return
	}
	if attempts >= maxQueueDispatchAttempts {
		// It would block the queue forever.
		s.queue.Remove(next.ID)
		s.emitter.EmitError(fmt.Sprintf("Dropped queued message %q after %d failed attempts: %v", shortText(next.Content, 40), attempts, err), st.ErrorLevelError)
	}
}

func (s *Server) registerQueueRoutes() {
	huma.Get(s.api, "/queue", s.getQueue, func(o *huma.Operation) {
		o.Description = "Returns user messages waiting to be sent to the agent."
	})
	huma.Put(s.api, "/queue/{id}", s.updateQueuedMessage, func(o *huma.Operation) {
		o.Description = "Updates a queued user message."
	})
	huma.Delete(s.api, "/queue/{id}", s.deleteQueuedMessage, func(o *huma.Operation) {
		o.Description = "Deletes a queued user message."
	})
	huma.Put(s.api, "/queue", s.reorderQueue, func(o *huma.Operation) {
		o.Description = "Reorders the queued messages. The body must list every queued message id exactly once; otherwise 409 is returned."
		o.Errors = []int{409}
	})
}
