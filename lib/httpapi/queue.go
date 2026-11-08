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

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/native"
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
	messages []convstate.QueuedMessage
	lastID   int
	// failures counts the failed sends in a row of the message failedID.
	failedID int
	failures int
	// version counts the changes; onChange, if set, gets the queue and
	// the last ID after each, without q.mu held.
	version  uint64
	onChange func(queue []convstate.QueuedMessage, lastID int, version uint64)
}

// changed tells onChange about a change made with q.mu held, which it
// releases.
func (q *messageQueue) changed() {
	q.version++
	queue, lastID, version, onChange := append([]convstate.QueuedMessage{}, q.messages...), q.lastID, q.version, q.onChange
	q.mu.Unlock()
	if onChange != nil {
		onChange(queue, lastID, version)
	}
}

// state returns the queue, the last ID and the version as of one moment.
func (q *messageQueue) state() ([]convstate.QueuedMessage, int, uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]convstate.QueuedMessage{}, q.messages...), q.lastID, q.version
}

// Len is how many messages wait.
func (q *messageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// Messages returns the queue, in order. It is never nil.
func (q *messageQueue) Messages() []convstate.QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]convstate.QueuedMessage{}, q.messages...)
}

// Push queues content, queued at at: last, or first with front.
func (q *messageQueue) Push(content string, at time.Time, front bool) convstate.QueuedMessage {
	q.mu.Lock()
	q.lastID++
	m := convstate.QueuedMessage{ID: q.lastID, Content: content, Time: at}
	if front {
		q.messages = slices.Insert(q.messages, 0, m)
	} else {
		q.messages = append(q.messages, m)
	}
	q.changed()
	return m
}

// Head returns the message that goes out next.
func (q *messageQueue) Head() (convstate.QueuedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return convstate.QueuedMessage{}, false
	}
	return q.messages[0], true
}

// Get returns the queued message id.
func (q *messageQueue) Get(id int) (convstate.QueuedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.index(id)
	if i < 0 {
		return convstate.QueuedMessage{}, false
	}
	return q.messages[i], true
}

// Update replaces the content of the queued message id. It reports whether
// the message is queued.
func (q *messageQueue) Update(id int, content string) bool {
	q.mu.Lock()
	i := q.index(id)
	if i < 0 {
		q.mu.Unlock()
		return false
	}
	q.messages[i].Content = content
	q.changed()
	return true
}

// Remove takes the message id out of the queue. It reports whether it was
// queued.
func (q *messageQueue) Remove(id int) bool {
	q.mu.Lock()
	i := q.index(id)
	if i < 0 {
		q.mu.Unlock()
		return false
	}
	q.messages = slices.Delete(q.messages, i, i+1)
	q.changed()
	return true
}

// Reorder puts the queue in the order of ids. It reports false, and leaves
// the queue as it is, unless ids lists every queued message once.
func (q *messageQueue) Reorder(ids []int) bool {
	q.mu.Lock()
	if len(ids) != len(q.messages) {
		q.mu.Unlock()
		return false
	}
	byID := make(map[int]convstate.QueuedMessage, len(q.messages))
	for _, m := range q.messages {
		byID[m.ID] = m
	}
	reordered := make([]convstate.QueuedMessage, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			q.mu.Unlock()
			return false
		}
		delete(byID, id)
		reordered = append(reordered, m)
	}
	q.messages = reordered
	q.changed()
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
	q.messages = nil
	q.failedID = 0
	q.failures = 0
	q.changed()
}

// Restore puts back a queue stored before, whose last ID was lastID.
func (q *messageQueue) Restore(messages []convstate.QueuedMessage, lastID int) {
	q.mu.Lock()
	q.messages = slices.Clone(messages)
	q.lastID = lastID
	for _, m := range messages {
		q.lastID = max(q.lastID, m.ID)
	}
	q.changed()
}

// index returns the position of the message id, or -1. Caller holds q.mu.
func (q *messageQueue) index(id int) int {
	return slices.IndexFunc(q.messages, func(m convstate.QueuedMessage) bool { return m.ID == id })
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
		return nil
	}, "messageQueueDispatch")
}

// beforeQueue sends the continuation of a turn a usage limit stopped, or
// the retry of a failed turn, once it is due, on the queue's tick: the
// queued messages go after it.
func (s *Server) beforeQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnGateLocked(turnContinuation, s.clock.Now()) != nil {
		return
	}
	s.continueIfDueLocked()
}

// dispatchNextQueuedMessage sends the head of the queue once the agent can
// take it. A message the agent refuses is tried again on later ticks and
// dropped after maxQueueDispatchAttempts, or at once when sending it again
// could deliver it twice.
func (s *Server) dispatchNextQueuedMessage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// turnGateLocked says why the head waits, a tick that waited for s.mu
	// while Stop began included: the queue is stored for after the restart.
	next, ok := s.queue.Head()
	if !ok || s.turnGateLocked(turnQueued, s.clock.Now()) != nil {
		return
	}
	err := s.sendQueued(next.Content)
	switch {
	case err == nil:
		s.queue.Remove(next.ID)
		return
	case errors.Is(err, conv.ErrMessageValidationChanging):
		// The agent got busy again: on a later tick.
		return
	}
	attempts := s.queue.Failed(next.ID)
	s.logger.Error("Failed to send queued message", "queueId", next.ID, "attempt", attempts, "error", err)
	if reason := notResent(err); reason != "" {
		s.queue.Remove(next.ID)
		s.emitter.EmitError(fmt.Sprintf("Queued message %q %s: %v", shortText(next.Content, 40), reason, err), conv.ErrorLevelError)
		return
	}
	if attempts >= maxQueueDispatchAttempts {
		// It would block the queue forever.
		s.queue.Remove(next.ID)
		s.emitter.EmitError(fmt.Sprintf("Dropped queued message %q after %d failed attempts: %v", shortText(next.Content, 40), attempts, err), conv.ErrorLevelError)
	}
}

// sendQueued sends a queued message to the agent as a new turn.
func (s *Server) sendQueued(content string) error {
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	s.emitter.SendingMessage()
	return s.native.StartTurn(ctx, native.InputFromText(content))
}

// notResent says why a queued message whose send failed with err must not
// be sent again, or "" when it can be. A message the agent may have taken
// could run its task twice if it were sent again. The adapters mark one
// that went out but wasn't confirmed with native.ErrUnconfirmed: the call
// timed out, AgentAPI stopped meanwhile (which would otherwise keep it
// queued and, with --state-db, send it again after the restart), or the
// agent exited. A message whose call ended before it went out stays
// queued.
func notResent(err error) string {
	if errors.Is(err, native.ErrUnconfirmed) {
		return "may have reached the agent, which didn't confirm it"
	}
	return ""
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
