package convstate

import (
	"slices"
	"time"
)

type QueuedMessage struct {
	ID      int       `json:"id" doc:"Unique identifier for the queued message."`
	Content string    `json:"content" doc:"Message content."`
	Time    time.Time `json:"time" doc:"Timestamp when the message was queued."`
}

// QueueUpdateBody is the SSE payload of a change of the message queue.
type QueueUpdateBody struct {
	Queue []QueuedMessage `json:"queue" nullable:"false" doc:"The messages waiting for the agent, in the order they go out."`
	Seq   uint64          `json:"seq" doc:"Sequence number of this change; see message_update."`
}

// SetQueue sends the message queue after a change, the version-th; a
// change reported late, after a newer one, is dropped.
func (e *EventEmitter) SetQueue(queue []QueuedMessage, version uint64) {
	e.mu.Lock()
	defer e.unlock()
	if version <= e.queueVersion {
		return
	}
	e.queue, e.queueVersion = queue, version
	e.seq++
	e.queueSeq = e.seq
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	e.notifyChannels(EventTypeQueueUpdate, QueueUpdateBody{Queue: slices.Clone(queue), Seq: e.seq})
}
