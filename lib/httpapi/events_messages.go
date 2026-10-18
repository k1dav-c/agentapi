package httpapi

import (
	"encoding/json"
	"slices"

	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/transcript"
)

// EmitMessages publishes the conversation's messages, which are ordered by
// increasing ID. Changed and new messages are sent as updates. Messages can
// also be removed (the native transport takes back a message the agent
// refused), which updates can't express: clients then get the full state
// under a new epoch. A message inserted between existing ones is not
// supported.
func (e *EventEmitter) EmitMessages(newMessages []st.ConversationMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()

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
	seqs := make([]uint64, len(newMessages))
	copy(seqs, e.messageSeq)
	for i, msg := range newMessages {
		if i < len(e.messages) && e.messages[i] == msg {
			continue
		}
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
	if removed {
		e.resyncLocked()
		return
	}
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
}

// resyncLocked starts a new epoch and sends connected clients the full
// state, for a change they can't apply as an update. Clients that reconnect
// with the old epoch get the full state too. Caller holds e.mu.
func (e *EventEmitter) resyncLocked() {
	e.epoch = newEpoch()
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	for _, event := range e.currentStateAsEvents(0, "") {
		// Status and screen didn't change, and errors would be shown again.
		if event.Type == EventTypeStatusChange || event.Type == EventTypeScreenUpdate || event.Type == EventTypeError {
			continue
		}
		e.notifyChannels(event.Type, event.Payload)
	}
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
	defer e.mu.Unlock()

	idx := -1
	// Search from the end: updates target recent messages.
	for i := len(e.richMessages) - 1; i >= 0; i-- {
		if e.richMessages[i].MessageID == msg.MessageID && e.richMessages[i].Role == msg.Role {
			idx = i
			break
		}
	}
	if idx >= 0 && replace {
		e.richMessages[idx] = msg
	} else if idx >= 0 {
		existing := e.richMessages[idx]
		existing.Content = mergeRichContent(existing.Content, msg.Content)
		existing.StopReason = msg.StopReason
		existing.Model = msg.Model
		existing.Usage = msg.Usage
		e.richMessages[idx] = existing
		msg = existing
	} else {
		e.richMessages = append(e.richMessages, msg)
		e.richSeq = append(e.richSeq, 0)
		idx = len(e.richMessages) - 1
	}
	e.seq++
	e.richSeq[idx] = e.seq
	if e.persister != nil {
		e.persister.PutRichMessage(idx, msg, e.seq)
		e.persister.SetSeq(e.epoch, e.seq)
	}
	// Always broadcast the merged snapshot. Broadcasting the incoming Claude
	// delta would make clients replace a complete message with its last block.
	e.notifyChannels(EventTypeRichMessageUpdate, RichMessageUpdateBody{RichMessage: msg, Seq: e.seq})
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
	defer e.mu.Unlock()
	return slices.Clone(e.richMessages)
}

// EmitSessionEvents stores normalized events in the same order as the source
// JSONL records and assigns stable identifiers for the lifetime of this run.
func (e *EventEmitter) EmitSessionEvents(events []transcript.SessionEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, event := range events {
		event.EventID = e.nextSessionEventID
		e.nextSessionEventID++
		e.sessionEvents = append(e.sessionEvents, event)
	}
	if e.persister != nil && len(events) > 0 {
		e.persister.AppendSessionEvents(e.sessionEvents[len(e.sessionEvents)-len(events):])
	}
}

func (e *EventEmitter) SessionEvents() []transcript.SessionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sessionEvents)
}
