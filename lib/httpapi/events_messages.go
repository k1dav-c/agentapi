package httpapi

import (
	"encoding/json"
	"slices"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

// EmitMessages publishes the conversation's messages, which are ordered by
// increasing ID. Changed and new messages are sent as updates. Messages can
// also be removed (the native transport takes back a message the agent
// refused), which updates can't express: the conversation gets a new epoch
// and the subscriptions end, so that clients load the full state again
// (see resyncLocked). A message inserted between existing ones is not
// supported.
func (e *EventEmitter) EmitMessages(newMessages []conv.Message) {
	e.mu.Lock()
	defer e.unlock()

	// cut is where the IDs stop matching: the messages from there on were
	// removed or replaced by others.
	cut := min(len(e.messages), len(newMessages))
	for i := range cut {
		if e.messages[i].Id != newMessages[i].Id {
			cut = i
			break
		}
	}
	removed := cut < len(e.messages)
	if removed && e.persister != nil {
		e.persister.TruncateMessages(e.messages[cut].Id)
	}
	// from is the first message that changed: the tasks are derived again
	// from there on.
	from := len(newMessages)
	seqs := e.messageSeq[:min(len(e.messageSeq), len(newMessages))]
	for len(seqs) < len(newMessages) {
		seqs = append(seqs, 0)
	}
	for i, msg := range newMessages {
		if i < len(e.messages) && e.messages[i] == msg {
			continue
		}
		from = min(from, i)
		e.seq++
		seqs[i] = e.seq
		if e.persister != nil {
			e.persister.PutMessage(msg, e.seq)
		}
		if !removed {
			e.notifyChannels(EventTypeMessageUpdate, MessageUpdateBody{
				Id: msg.Id, Role: msg.Role, Message: msg.Message, Time: msg.Time, Seq: e.seq,
			})
		}
	}
	e.messages = newMessages
	e.messageSeq = seqs
	changed := e.tasks.messages(e.messages, from)
	if removed {
		// A task whose message went was deleted with it (TruncateMessages);
		// the clients get the tasks with the full state.
		for id := range e.taskSeq {
			if e.tasks.owner(id) < 0 {
				delete(e.taskSeq, id)
			}
		}
		e.tasksStoredLocked(changed)
		e.resyncLocked()
		return
	}
	e.tasksChangedLocked(changed)
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
}

// resyncLocked starts a new epoch for a change clients can't apply as an
// update, and ends the subscriptions that get the conversation's changes.
// Their clients reconnect with the old epoch and get the full state in the
// replay of a new subscription, whose SSE ids are right. Streaming the
// state to them instead gave every event its own id: a client takes the
// first one as the end of the replay, and a subscription whose buffer
// filled halfway through was closed with the rest of the state unsent,
// which the client then never asked for again. Caller holds e.mu.
func (e *EventEmitter) resyncLocked() {
	e.epoch = newEpoch()
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	e.endConversationSubscriptionsLocked()
}

// endConversationSubscriptionsLocked ends the subscriptions that get the
// conversation's changes; screen streams go on. Caller holds e.mu.
func (e *EventEmitter) endConversationSubscriptionsLocked() {
	for id, sub := range e.chans {
		if wantsConversation(sub.wants) {
			e.unsubscribeInner(id)
		}
	}
}

// wantsConversation reports whether a subscription that gets the events
// wants accepts gets changes of the conversation; a screen stream doesn't.
func wantsConversation(wants func(EventType) bool) bool {
	return wants(EventTypeMessageUpdate) || wants(EventTypeRichMessageUpdate) || wants(EventTypeRequestUpdate) ||
		wants(EventTypeTaskUpdate) || wants(EventTypeQueueUpdate)
}

// EmitRichMessage emits a rich structured message from the JSONL watcher.
// It stores the message for late subscriber replay. Messages are upserted
// by (MessageID, Role) because parsers re-emit a message as its content
// accumulates (e.g. Codex turn updates).
func (e *EventEmitter) EmitRichMessage(msg transcript.RichMessage) {
	e.emitRichMessage(msg, false)
}

// EmitRichMessageSnapshot stores msg as the full current state of the
// message, replacing an earlier state with the same (MessageID, Role)
// instead of merging blocks into it. Native adapters send snapshots: their
// text blocks grow, and merging would keep every intermediate version.
func (e *EventEmitter) EmitRichMessageSnapshot(msg transcript.RichMessage) {
	e.emitRichMessage(msg, true)
}

func (e *EventEmitter) emitRichMessage(msg transcript.RichMessage, replace bool) {
	e.mu.Lock()
	defer e.unlock()
	msg = e.upsertRichLocked(msg, replace)
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	// Always broadcast the merged snapshot. Broadcasting the incoming Claude
	// delta would make clients replace a complete message with its last block.
	e.notifyChannels(EventTypeRichMessageUpdate, RichMessageUpdateBody{RichMessage: msg, Seq: e.seq})
}

// LoadRichMessages adds a resumed session's rich messages at once, each
// stored as EmitRichMessageSnapshot stores it. The subscriptions to the
// conversation get them one by one when they fit their buffers easily;
// for a longer session, which would overflow them, the subscriptions end
// instead, and their clients load the state again.
func (e *EventEmitter) LoadRichMessages(msgs []transcript.RichMessage) {
	if len(msgs) == 0 {
		return
	}
	e.mu.Lock()
	defer e.unlock()
	stream := len(msgs) <= int(e.subscriptionBufSize)/4
	for _, msg := range msgs {
		msg = e.upsertRichLocked(msg, true)
		if stream {
			e.notifyChannels(EventTypeRichMessageUpdate, RichMessageUpdateBody{RichMessage: msg, Seq: e.seq})
		}
	}
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	if !stream {
		e.endConversationSubscriptionsLocked()
	}
}

// nonNilContent is content, or an empty list for none: clients rely on the
// content being a list, and Go would send a nil one as null.
func nonNilContent(content []transcript.RichContentBlock) []transcript.RichContentBlock {
	if content == nil {
		return []transcript.RichContentBlock{}
	}
	return content
}

// richKey identifies a rich message, which parsers and adapters send again
// as it grows.
type richKey struct{ id, role string }

// upsertRichLocked stores msg under the next seq, as a new rich message or
// in place of the one with its key (with replace) or merged into it, and
// returns what is stored. Caller holds e.mu.
func (e *EventEmitter) upsertRichLocked(msg transcript.RichMessage, replace bool) transcript.RichMessage {
	msg.Content = nonNilContent(msg.Content)
	key := richKey{msg.MessageID, msg.Role}
	idx, ok := e.richIndex[key]
	// A message belongs to the task it began in.
	switch {
	case ok && e.richMessages[idx].TaskID != nil:
		msg.TaskID = e.richMessages[idx].TaskID
	case msg.TaskID == nil:
		msg.TaskID = e.tasks.taskAt(msg.Timestamp)
	}
	switch {
	case ok && replace:
		e.richMessages[idx] = msg
	case ok:
		existing := e.richMessages[idx]
		existing.Content = nonNilContent(mergeRichContent(existing.Content, msg.Content))
		existing.StopReason = msg.StopReason
		existing.Model = msg.Model
		existing.Usage = msg.Usage
		existing.TaskID = msg.TaskID
		e.richMessages[idx] = existing
		msg = existing
	default:
		if e.richIndex == nil {
			e.richIndex = make(map[richKey]int)
		}
		e.richMessages = append(e.richMessages, msg)
		e.richSeq = append(e.richSeq, 0)
		idx = len(e.richMessages) - 1
		e.richIndex[key] = idx
	}
	e.seq++
	e.richSeq[idx] = e.seq
	if e.persister != nil {
		e.persister.PutRichMessage(idx, msg, e.seq)
	}
	return msg
}

func mergeRichContent(existing, incoming []transcript.RichContentBlock) []transcript.RichContentBlock {
	merged := slices.Clone(existing)
	seen := make(map[string]struct{}, len(existing))
	for _, block := range existing {
		seen[richContentBlockKey(block)] = struct{}{}
	}
	for _, block := range incoming {
		key := richContentBlockKey(block)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, block)
	}
	return merged
}

func richContentBlockKey(block transcript.RichContentBlock) string {
	if block.ToolUseID != "" {
		return block.Type + ":" + block.ToolUseID
	}
	encoded, _ := json.Marshal(block)
	return string(encoded)
}

// RichMessages returns a snapshot of all rich messages received so far.
// The emitter is the single store for rich messages: the JSONL watcher
// parses and emits, the emitter deduplicates, replays, and serves reads.
func (e *EventEmitter) RichMessages() []transcript.RichMessage {
	e.mu.Lock()
	defer e.unlock()
	return slices.Clone(e.richMessages)
}

// EmitSessionEvents stores normalized events in the same order as the source
// JSONL records and assigns stable identifiers for the lifetime of this run.
func (e *EventEmitter) EmitSessionEvents(events []transcript.SessionEvent) {
	e.mu.Lock()
	defer e.unlock()
	for _, event := range events {
		event.EventID = e.nextSessionEventID
		e.nextSessionEventID++
		e.shareContentLocked(&event)
		e.sessionEvents = append(e.sessionEvents, event)
	}
	if e.persister != nil && len(events) > 0 {
		e.persister.AppendSessionEvents(e.sessionEvents[len(e.sessionEvents)-len(events):])
	}
}

func (e *EventEmitter) SessionEvents() []transcript.SessionEvent {
	e.mu.Lock()
	defer e.unlock()
	return slices.Clone(e.sessionEvents)
}

// The timeline holds what the rich messages hold, a second time: the agents
// report what they said and did both ways, and the two are decoded apart.
// Its texts, thoughts and tool results are made to share memory with the
// rich messages' equal ones; the timeline's values don't change.
const (
	// minSharedContent is the shortest content worth sharing.
	minSharedContent = 64
	// sharedContentLookback is how many of the latest rich messages a new
	// event's content is looked for in: an adapter reports a message's
	// timeline events right after the message.
	sharedContentLookback = 4
)

// shareContentLocked makes the content of a new timeline event share memory
// with the equal text of a recent rich message. Caller holds e.mu.
func (e *EventEmitter) shareContentLocked(event *transcript.SessionEvent) {
	if event.Content == nil || len(*event.Content) < minSharedContent {
		return
	}
	content := *event.Content
	for i := len(e.richMessages) - 1; i >= 0 && i >= len(e.richMessages)-sharedContentLookback; i-- {
		for _, block := range e.richMessages[i].Content {
			for _, text := range [...]string{block.Text, block.Thinking} {
				if text == content {
					event.Content = &text
					return
				}
			}
		}
	}
}

// shareRestoredContent makes the content of restored timeline events share
// memory with the equal texts of the restored rich messages, which the
// state database stores apart.
func shareRestoredContent(rich []transcript.RichMessage, events []transcript.SessionEvent) {
	texts := make(map[string]string)
	for _, msg := range rich {
		for _, block := range msg.Content {
			for _, text := range [...]string{block.Text, block.Thinking} {
				if len(text) >= minSharedContent {
					texts[text] = text
				}
			}
		}
	}
	for i := range events {
		if content := events[i].Content; content != nil {
			if text, ok := texts[*content]; ok {
				events[i].Content = &text
			}
		}
	}
}
