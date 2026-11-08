package convstate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/transcript"
)

func TestEventEmitterSessionEvents(t *testing.T) {
	emitter := NewEventEmitter()
	emitter.EmitSessionEvents([]transcript.SessionEvent{
		{Kind: "text"},
		{Kind: "tool_call", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
	})

	events := emitter.SessionEvents()
	require.Len(t, events, 2)
	require.Equal(t, 1, events[0].EventID)
	require.Equal(t, 2, events[1].EventID)

	events[0].Kind = "changed"
	require.Equal(t, "text", emitter.SessionEvents()[0].Kind)
}

// The timeline's content shares memory with the rich messages' equal texts,
// which an agent reports apart, without changing its value.
func TestEventEmitterSessionEventsShareRichContent(t *testing.T) {
	output := strings.Repeat("a tool's output ", 10)
	thought := strings.Repeat("a thought ", 10)
	decodedApart := func(s string) *string {
		copied := strings.Clone(s)
		return &copied
	}
	emitter := NewEventEmitter()
	emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{
		{Type: "thinking", Thinking: thought},
	}})
	emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m2", Role: "user", Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Text: output},
	}})
	short := "ok"
	emitter.EmitSessionEvents([]transcript.SessionEvent{
		{Kind: "thinking", Content: decodedApart(thought)},
		{Kind: "tool_result", Content: decodedApart(output)},
		{Kind: "text", Content: decodedApart(strings.Repeat("said once ", 10))},
		{Kind: "text", Content: &short},
	})
	events := emitter.SessionEvents()
	require.Len(t, events, 4)
	assert.Equal(t, thought, *events[0].Content)
	assert.Same(t, unsafe.StringData(thought), unsafe.StringData(*events[0].Content))
	assert.Same(t, unsafe.StringData(output), unsafe.StringData(*events[1].Content))
	assert.Equal(t, strings.Repeat("said once ", 10), *events[2].Content)

	// The state database stores them apart too.
	restored := NewEventEmitter()
	restored.Restore(EmitterState{
		RichMessages: emitter.RichMessages(),
		RichSeqs:     []uint64{1, 2},
		SessionEvents: []transcript.SessionEvent{
			{EventID: 1, Kind: "tool_result", Content: decodedApart(output)},
		},
	})
	events = restored.SessionEvents()
	require.Len(t, events, 1)
	assert.Equal(t, output, *events[0].Content)
	assert.Same(t, unsafe.StringData(restored.RichMessages()[1].Content[0].Text), unsafe.StringData(*events[0].Content))
}

func TestEventEmitterMergesClaudeRichMessageDeltas(t *testing.T) {
	emitter := NewEventEmitter(WithSubscriptionBufSize(10))
	_, ch, _ := emitter.Subscribe()

	emitter.EmitRichMessage(transcript.RichMessage{
		MessageID: "msg_1",
		Role:      "assistant",
		Content: []transcript.RichContentBlock{
			{Type: "thinking", Thinking: "checking files"},
			{Type: "text", Text: "1. Read files"},
			{Type: "tool_use", ToolUseID: "tool_1", ToolName: "Read"},
		},
	})
	<-ch

	emitter.EmitRichMessage(transcript.RichMessage{
		MessageID: "msg_1",
		Role:      "assistant",
		Content: []transcript.RichContentBlock{
			{Type: "tool_use", ToolUseID: "tool_2", ToolName: "Read"},
		},
	})

	event := <-ch
	update := event.Payload.(RichMessageUpdateBody).RichMessage
	require.Len(t, update.Content, 4)
	require.Equal(t, "tool_2", update.Content[3].ToolUseID)
	require.Equal(t, update.Content, emitter.RichMessages()[0].Content)
}

func TestEventEmitter(t *testing.T) {
	t.Run("single-subscription", func(t *testing.T) {
		emitter := NewEventEmitter(WithSubscriptionBufSize(10))
		_, ch, stateEvents := emitter.Subscribe()
		assert.Empty(t, ch)
		assert.Equal(t, []Event{
			{
				Type:    EventTypeSessionSync,
				Payload: SessionSyncBody{Epoch: emitter.epoch, Full: true},
			},
			{
				Type:    EventTypeStatusChange,
				Payload: StatusChangeBody{Status: instanceapi.AgentStatusRunning, Lifecycle: instanceapi.LifecycleStarting, Transport: instanceapi.TransportNative, Version: version.Version},
			},
		}, stateEvents)

		now := time.Now()
		emitter.EmitMessages([]conv.Message{
			{Id: 1, Message: "Hello, world!", Role: conv.RoleUser, Time: now},
		})
		newEvent := <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 1, Message: "Hello, world!", Role: conv.RoleUser, Time: now, Seq: 1},
		}, newEvent)
		// The user's message starts a task.
		task := instanceapi.Task{ID: 1, Status: instanceapi.TaskStatusRunning, Prompt: "Hello, world!", StartedAt: now}
		assert.Equal(t, Event{Type: EventTypeTaskUpdate, Payload: TaskUpdateBody{Task: task, Seq: 2}}, <-ch)

		emitter.EmitMessages([]conv.Message{
			{Id: 1, Message: "Hello, world! (updated)", Role: conv.RoleUser, Time: now},
			{Id: 2, Message: "What's up?", Role: conv.RoleAgent, Time: now},
		})
		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 1, Message: "Hello, world! (updated)", Role: conv.RoleUser, Time: now, Seq: 3},
		}, newEvent)

		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 2, Message: "What's up?", Role: conv.RoleAgent, Time: now, Seq: 4},
		}, newEvent)
		// The agent's message is its reply.
		reply := 2
		task.ReplyID = &reply
		assert.Equal(t, Event{Type: EventTypeTaskUpdate, Payload: TaskUpdateBody{Task: task, Seq: 5}}, <-ch)

		emitter.EmitStatus(conv.StatusStable)
		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeStatusChange,
			Payload: StatusChangeBody{Status: instanceapi.AgentStatusStable, Lifecycle: instanceapi.LifecycleReady, AgentType: "", Transport: instanceapi.TransportNative, Version: version.Version},
		}, newEvent)
	})

	t.Run("multiple-subscriptions", func(t *testing.T) {
		emitter := NewEventEmitter(WithSubscriptionBufSize(10))
		channels := make([]<-chan Event, 0, 10)
		for i := 0; i < 10; i++ {
			_, ch, _ := emitter.Subscribe()
			channels = append(channels, ch)
		}
		now := time.Now()

		emitter.EmitMessages([]conv.Message{
			{Id: 1, Message: "Hello, world!", Role: conv.RoleUser, Time: now},
		})
		for _, ch := range channels {
			newEvent := <-ch
			assert.Equal(t, Event{
				Type:    EventTypeMessageUpdate,
				Payload: MessageUpdateBody{Id: 1, Message: "Hello, world!", Role: conv.RoleUser, Time: now, Seq: 1},
			}, newEvent)
		}
	})

	t.Run("close-channel", func(t *testing.T) {
		emitter := NewEventEmitter(WithSubscriptionBufSize(1))
		_, ch, _ := emitter.Subscribe()
		for i := range 5 {
			emitter.EmitMessages([]conv.Message{
				{Id: i, Message: fmt.Sprintf("Hello, world! %d", i), Role: conv.RoleUser, Time: time.Now()},
			})
		}
		_, ok := <-ch
		assert.True(t, ok)
		select {
		case _, ok := <-ch:
			assert.False(t, ok)
		default:
			t.Fatalf("read should not block")
		}
	})

	t.Run("error-cap", func(t *testing.T) {
		emitter := NewEventEmitter(WithSubscriptionBufSize(10))

		for i := range 150 {
			emitter.EmitError(fmt.Sprintf("error %d", i), conv.ErrorLevelError)
		}

		_, _, stateEvents := emitter.Subscribe()

		var errorEvents []Event
		for _, ev := range stateEvents {
			if ev.Type == EventTypeError {
				errorEvents = append(errorEvents, ev)
			}
		}

		assert.Len(t, errorEvents, maxStoredErrors)

		// Errors should be the last 100: "error 50" through "error 149".
		for i, ev := range errorEvents {
			body, ok := ev.Payload.(ErrorBody)
			assert.True(t, ok)
			assert.Equal(t, fmt.Sprintf("error %d", i+50), body.Message)
		}
	})

	t.Run("error-events-in-initial-state", func(t *testing.T) {
		mockClock := quartz.NewMock(t)
		fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		mockClock.Set(fixedTime)

		emitter := NewEventEmitter(WithClock(mockClock), WithSubscriptionBufSize(10))

		emitter.EmitError("err1", conv.ErrorLevelError)
		mockClock.Set(fixedTime.Add(1 * time.Second))
		emitter.EmitError("err2", conv.ErrorLevelWarning)
		mockClock.Set(fixedTime.Add(2 * time.Second))
		emitter.EmitError("err3", conv.ErrorLevelError)

		_, _, stateEvents := emitter.Subscribe()

		var errorEvents []Event
		for _, ev := range stateEvents {
			if ev.Type == EventTypeError {
				errorEvents = append(errorEvents, ev)
			}
		}

		assert.Len(t, errorEvents, 3)

		expected := []ErrorBody{
			{Message: "err1", Level: conv.ErrorLevelError, Time: fixedTime},
			{Message: "err2", Level: conv.ErrorLevelWarning, Time: fixedTime.Add(1 * time.Second)},
			{Message: "err3", Level: conv.ErrorLevelError, Time: fixedTime.Add(2 * time.Second)},
		}
		for i, ev := range errorEvents {
			body, ok := ev.Payload.(ErrorBody)
			assert.True(t, ok)
			assert.Equal(t, expected[i].Message, body.Message)
			assert.Equal(t, expected[i].Level, body.Level)
			assert.Equal(t, expected[i].Time, body.Time)
		}
	})

	t.Run("clock-injection", func(t *testing.T) {
		mockClock := quartz.NewMock(t)
		fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		mockClock.Set(fixedTime)

		emitter := NewEventEmitter(WithClock(mockClock), WithSubscriptionBufSize(10))
		_, ch, stateEvents := emitter.Subscribe()

		// Verify initial state events: session sync, status
		assert.Len(t, stateEvents, 2)

		// Emit an error and verify it uses the mock clock time
		emitter.EmitError("test error", conv.ErrorLevelError)

		event := <-ch
		assert.Equal(t, EventTypeError, event.Type)
		errorBody, ok := event.Payload.(ErrorBody)
		assert.True(t, ok)
		assert.Equal(t, "test error", errorBody.Message)
		assert.Equal(t, conv.ErrorLevelError, errorBody.Level)
		assert.Equal(t, fixedTime, errorBody.Time)

		// Advance the clock and emit another error
		newTime := fixedTime.Add(1 * time.Hour)
		mockClock.Set(newTime)
		emitter.EmitError("another error", conv.ErrorLevelWarning)

		event = <-ch
		assert.Equal(t, EventTypeError, event.Type)
		errorBody, ok = event.Payload.(ErrorBody)
		assert.True(t, ok)
		assert.Equal(t, "another error", errorBody.Message)
		assert.Equal(t, conv.ErrorLevelWarning, errorBody.Level)
		assert.Equal(t, newTime, errorBody.Time)
	})
}

func TestEventEmitterIncrementalReplay(t *testing.T) {
	emitter := NewEventEmitter(WithSubscriptionBufSize(10))
	now := time.Now()
	rich := func(id, text string) transcript.RichMessage {
		return transcript.RichMessage{MessageID: id, Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: text}}}
	}
	emitter.EmitMessages([]conv.Message{{Id: 0, Role: conv.RoleUser, Message: "one", Time: now}})
	emitter.EmitRichMessage(rich("m1", "first"))
	emitter.EmitRichMessage(rich("m2", "second"))

	_, _, state := emitter.Subscribe()
	sync := state[0].Payload.(SessionSyncBody)
	require.True(t, sync.Full)
	// The message, its task and the two rich messages.
	require.Equal(t, uint64(4), sync.Seq)

	// Later changes: a new message, and an update to an existing rich message.
	emitter.EmitMessages([]conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "one", Time: now},
		{Id: 1, Role: conv.RoleAgent, Message: "two", Time: now},
	})
	emitter.EmitRichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "tool_use", ToolUseID: "t1"}}})

	kinds := func(events []Event) []string {
		var out []string
		for _, ev := range events {
			switch p := ev.Payload.(type) {
			case MessageUpdateBody:
				out = append(out, fmt.Sprintf("message %d", p.Id))
			case RichMessageUpdateBody:
				out = append(out, "rich "+p.MessageID)
			case TaskUpdateBody:
				out = append(out, fmt.Sprintf("task %d", p.ID))
			}
		}
		return out
	}

	// Resuming from the first replay gets only what changed since: the
	// task changed too, as the agent's message became its reply.
	_, _, state = emitter.SubscribeSince(sync.Seq, sync.Epoch)
	require.Equal(t, SessionSyncBody{Epoch: sync.Epoch, Full: false, Seq: 7}, state[0].Payload)
	require.Equal(t, []string{"message 1", "rich m1", "task 0"}, kinds(state))
	updated := state[len(state)-2].Payload.(RichMessageUpdateBody)
	require.Len(t, updated.Content, 2, "the replayed rich message is the merged snapshot")
	require.Equal(t, 0, *updated.TaskID, "it belongs to the task")

	// Up to date: nothing to replay.
	_, _, state = emitter.SubscribeSince(7, sync.Epoch)
	require.Equal(t, SessionSyncBody{Epoch: sync.Epoch, Full: false, Seq: 7}, state[0].Payload)
	require.Empty(t, kinds(state))

	// An unknown epoch, or a seq from the future, gets the full state.
	for _, tc := range []struct {
		since uint64
		epoch string
	}{{3, "other"}, {99, sync.Epoch}, {0, sync.Epoch}} {
		_, _, state = emitter.SubscribeSince(tc.since, tc.epoch)
		require.True(t, state[0].Payload.(SessionSyncBody).Full)
		require.Equal(t, []string{"message 0", "message 1", "rich m1", "rich m2", "task 0"}, kinds(state))
	}

	// A reset starts a new epoch and tells connected clients right away.
	_, ch, _ := emitter.Subscribe()
	emitter.Reset()
	reset := (<-ch).Payload.(SessionSyncBody)
	require.True(t, reset.Full)
	require.Zero(t, reset.Seq, "nothing to wait for after a reset")
	require.NotEqual(t, sync.Epoch, reset.Epoch)
	_, _, state = emitter.SubscribeSince(7, sync.Epoch)
	require.True(t, state[0].Payload.(SessionSyncBody).Full)
	require.Empty(t, kinds(state))
}

// recordingPersister records what an emitter stores.
type recordingPersister struct {
	tasks     map[int]string
	messages  map[int]conv.Message
	requests  map[string]agentstate.RequestState
	truncated []int
	deleted   []string
	epoch     string
	seq       uint64
}

func newRecordingPersister() *recordingPersister {
	return &recordingPersister{messages: map[int]conv.Message{}, requests: map[string]agentstate.RequestState{}}
}

func (p *recordingPersister) PutMessage(msg conv.Message, _ uint64) {
	p.messages[msg.Id] = msg
}

func (p *recordingPersister) TruncateMessages(fromID int) {
	p.truncated = append(p.truncated, fromID)
	for id := range p.messages {
		if id >= fromID {
			delete(p.messages, id)
		}
	}
}

func (p *recordingPersister) PutRichMessage(int, transcript.RichMessage, uint64) {}
func (p *recordingPersister) AppendSessionEvents([]transcript.SessionEvent)      {}
func (p *recordingPersister) PutRequest(state agentstate.RequestState, _ uint64) {
	p.requests[state.ID] = state
}

func (p *recordingPersister) DeleteRequest(id string) {
	p.deleted = append(p.deleted, id)
	delete(p.requests, id)
}

func (p *recordingPersister) SetSeq(epoch string, seq uint64)    { p.epoch, p.seq = epoch, seq }
func (p *recordingPersister) SetSessionName(string)              {}
func (p *recordingPersister) SetSessionRef(string)               {}
func (p *recordingPersister) SetSettings(agentstate.Settings)    {}
func (p *recordingPersister) SetLimitStop(*agentstate.LimitStop) {}
func (p *recordingPersister) SetRetryWait(*agentstate.RetryWait) {}
func (p *recordingPersister) SetSeenAt(time.Time)                {}
func (p *recordingPersister) PutTask(id int, data []byte, _ uint64) {
	if p.tasks == nil {
		p.tasks = map[int]string{}
	}
	p.tasks[id] = string(data)
}

func (p *recordingPersister) DeleteTask(id int) { delete(p.tasks, id) }

func (p *recordingPersister) Reset(epoch string) {
	p.epoch = epoch
	clear(p.messages)
	clear(p.requests)
}

// drain returns the events waiting in ch.
func drain(ch <-chan Event) []Event {
	var events []Event
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		default:
			return events
		}
	}
}

// drainClosed returns the events waiting in ch, and whether it was closed.
func drainClosed(ch <-chan Event) ([]Event, bool) {
	var events []Event
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return events, true
			}
			events = append(events, event)
		default:
			return events, false
		}
	}
}

// replayed names the changes in a replay.
func replayed(events []Event) []string {
	var names []string
	for _, event := range events {
		switch p := event.Payload.(type) {
		case MessageUpdateBody:
			names = append(names, fmt.Sprintf("message %d", p.Id))
		case RichMessageUpdateBody:
			names = append(names, "rich "+p.MessageID)
		case RequestUpdateBody:
			names = append(names, "request "+p.ID)
		case TaskUpdateBody:
			names = append(names, fmt.Sprintf("task %d", p.ID))
		}
	}
	return names
}

func TestEventEmitterRemovedMessageResyncs(t *testing.T) {
	persister := newRecordingPersister()
	emitter := NewEventEmitter(WithSubscriptionBufSize(20), WithPersister(persister))
	now := time.Now()
	msgs := []conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "one", Time: now},
		{Id: 1, Role: conv.RoleAgent, Message: "two", Time: now},
		{Id: 2, Role: conv.RoleUser, Message: "refused", Time: now},
	}
	emitter.EmitMessages(msgs)
	emitter.EmitRichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant"})
	emitter.EmitRequest(agentstate.RequestState{Request: agentstate.Request{ID: "r1"}, Status: agentstate.RequestStatusPending})
	_, _, state := emitter.Subscribe()
	cached := state[0].Payload.(SessionSyncBody)
	_, ch, _ := emitter.Subscribe()
	_, summary, _ := emitter.SubscribeTo(SummaryEvents, 0, "")

	// The agent refused the last message, and it was taken back. Updates
	// can't say so: the conversation gets a new epoch, and the
	// subscriptions to it end, so that their clients load it again.
	emitter.EmitMessages(msgs[:2])
	events, closed := drainClosed(ch)
	assert.True(t, closed, "the subscription ended")
	assert.Empty(t, events, "nothing is streamed")
	_, closed = drainClosed(summary)
	assert.False(t, closed, "the summary stream goes on")
	assert.Equal(t, []int{2}, persister.truncated)
	assert.NotEqual(t, cached.Epoch, emitter.Epoch())
	assert.Equal(t, emitter.Epoch(), persister.epoch, "the new epoch is stored")

	// A client that cached the message reconnects with its epoch and gets
	// the full state.
	_, ch, state = emitter.SubscribeSince(cached.Seq, cached.Epoch)
	sync := state[0].Payload.(SessionSyncBody)
	assert.True(t, sync.Full)
	assert.Equal(t, emitter.Epoch(), sync.Epoch)
	assert.Equal(t, uint64(7), sync.Seq, "the replay ends with the newest change: the task the taken-back message steered")
	assert.Equal(t, []string{"message 0", "message 1", "rich m1", "request r1", "task 0"}, replayed(state))
	assert.Empty(t, emitter.Tasks()[0].Steered)

	// A message removed from the middle is gone from the stored rows too,
	// and the ones after it are stored again.
	msgs = append(msgs[:2:2], conv.Message{Id: 3, Role: conv.RoleAgent, Message: "three", Time: now})
	emitter.EmitMessages(msgs)
	events, closed = drainClosed(ch)
	assert.Equal(t, []string{"message 3", "task 0"}, replayed(events), "an added message is an update, and the task's reply")
	assert.False(t, closed)
	emitter.EmitMessages([]conv.Message{msgs[0], msgs[2]})
	assert.Equal(t, []int{2, 1}, persister.truncated)
	assert.Equal(t, map[int]conv.Message{0: msgs[0], 3: msgs[2]}, persister.messages)
	_, closed = drainClosed(ch)
	assert.True(t, closed)
}

// A resync used to stream the full state to the connected clients, each
// event with its SSE id. The chat takes the first id as the end of the
// replay, and a subscription whose buffer filled halfway was closed with
// the rest unsent, which the client, resuming after that id, never got.
// The client that reconnects now gets all of it.
func TestEventEmitterResyncLosesNothing(t *testing.T) {
	emitter := NewEventEmitter(WithSubscriptionBufSize(8))
	var want []string
	var msgs []conv.Message
	for i := range 6 {
		msgs = append(msgs, conv.Message{Id: i, Role: conv.RoleUser, Message: fmt.Sprint("m", i), Time: time.Unix(0, 0)})
		if i < 5 {
			want = append(want, fmt.Sprintf("message %d", i))
		}
	}
	for i := range 5 {
		emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: fmt.Sprint("r", i), Role: "assistant"})
		want = append(want, fmt.Sprintf("rich r%d", i))
	}
	for i := range 5 {
		// Each old message is a task that is done.
		want = append(want, fmt.Sprintf("task %d", i))
	}
	emitter.EmitMessages(msgs)
	_, ch, state := emitter.SubscribeSince(0, "")
	synced := state[0].Payload.(SessionSyncBody)

	emitter.EmitMessages(msgs[:5])
	_, closed := drainClosed(ch)
	require.True(t, closed)
	_, _, state = emitter.SubscribeSince(synced.Seq, synced.Epoch)
	assert.True(t, state[0].Payload.(SessionSyncBody).Full)
	assert.Equal(t, want, replayed(state))
}

// Rich messages are found by their key, also after a restore, and the
// index goes with a reset.
func TestEventEmitterFindsRichMessagesByKey(t *testing.T) {
	persister := newRecordingPersister()
	emitter := NewEventEmitter(WithPersister(persister))
	text := func(id, role, s string) transcript.RichMessage {
		return transcript.RichMessage{MessageID: id, Role: role, Content: []transcript.RichContentBlock{{Type: "text", Text: s}}}
	}
	emitter.Restore(EmitterState{
		Seq:          2,
		RichMessages: []transcript.RichMessage{text("m1", "assistant", "one"), text("m1", "user", "result")},
		RichSeqs:     []uint64{1, 2},
	})
	emitter.EmitRichMessageSnapshot(text("m1", "assistant", "one, grown"))
	emitter.EmitRichMessageSnapshot(text("m2", "assistant", "two"))
	emitter.EmitRichMessage(transcript.RichMessage{MessageID: "m1", Role: "user", Content: []transcript.RichContentBlock{{Type: "text", Text: "more"}}})
	rich := emitter.RichMessages()
	require.Len(t, rich, 3)
	assert.Equal(t, "one, grown", rich[0].Content[0].Text)
	require.Len(t, rich[1].Content, 2, "merged")
	assert.Equal(t, "two", rich[2].Content[0].Text)

	emitter.Reset()
	emitter.EmitRichMessageSnapshot(text("m2", "assistant", "new conversation"))
	rich = emitter.RichMessages()
	require.Len(t, rich, 1)
	assert.Equal(t, "new conversation", rich[0].Content[0].Text)
}

// A resumed session's rich messages are stored at once. A short session's
// go to the subscriptions one by one; for a long one, which would overflow
// their buffers, the subscriptions to the conversation end instead, and
// the clients' replay holds them all.
func TestEventEmitterLoadRichMessages(t *testing.T) {
	history := func(n int) ([]transcript.RichMessage, []string) {
		var msgs []transcript.RichMessage
		var names []string
		for i := range n {
			msgs = append(msgs, transcript.RichMessage{MessageID: fmt.Sprint("h", i), Role: "assistant"})
			names = append(names, fmt.Sprintf("rich h%d", i))
		}
		return msgs, names
	}

	emitter := NewEventEmitter(WithSubscriptionBufSize(40))
	_, ch, _ := emitter.Subscribe()
	short, want := history(10)
	emitter.LoadRichMessages(short)
	events, closed := drainClosed(ch)
	assert.False(t, closed)
	assert.Equal(t, want, replayed(events))

	emitter = NewEventEmitter(WithSubscriptionBufSize(40))
	_, ch, _ = emitter.Subscribe()
	_, summary, _ := emitter.SubscribeTo(SummaryEvents, 0, "")
	long, want := history(11)
	emitter.LoadRichMessages(long)
	events, closed = drainClosed(ch)
	assert.True(t, closed)
	assert.Empty(t, events)
	_, closed = drainClosed(summary)
	assert.False(t, closed)
	_, _, state := emitter.SubscribeSince(0, "")
	assert.Equal(t, want, replayed(state))
	assert.Equal(t, uint64(11), state[0].Payload.(SessionSyncBody).Seq)
}

// Emitting rich messages one by one takes time in proportion to their
// number: a resumed session's used to take quadratic time, under s.mu.
func BenchmarkEmitRichMessageSnapshot(b *testing.B) {
	emitter := NewEventEmitter()
	b.ResetTimer()
	for i := range b.N {
		emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: strconv.Itoa(i), Role: "assistant"})
	}
}

func TestEventEmitterRequests(t *testing.T) {
	persister := newRecordingPersister()
	emitter := NewEventEmitter(WithPersister(persister))
	request := func(id string, status agentstate.RequestStatus) agentstate.RequestState {
		return agentstate.RequestState{Request: agentstate.Request{ID: id, Kind: agentstate.RequestKindApproval}, Status: status}
	}
	emitter.EmitRequest(request("waiting", agentstate.RequestStatusPending))
	for i := range agentstate.MaxClosedRequests + 2 {
		id := fmt.Sprintf("r%d", i)
		emitter.EmitRequest(request(id, agentstate.RequestStatusPending))
		emitter.EmitRequest(request(id, agentstate.RequestStatusResolved))
	}
	assert.Equal(t, 1, emitter.StatusSnapshot().PendingRequests)

	// The oldest closed requests are dropped; pending ones are kept.
	requests := emitter.Requests()
	require.Len(t, requests, agentstate.MaxClosedRequests+1)
	assert.Equal(t, "waiting", requests[0].ID)
	assert.Equal(t, "r2", requests[1].ID)
	assert.Equal(t, []string{"r0", "r1"}, persister.deleted)
	assert.Len(t, persister.requests, agentstate.MaxClosedRequests+1)
	_, ok := emitter.Request("r1")
	assert.False(t, ok)
	got, ok := emitter.Request("r2")
	require.True(t, ok)
	assert.Equal(t, agentstate.RequestStatusResolved, got.Status)

	// A request closed after a reset belongs to the conversation that was
	// cleared; it doesn't come back.
	emitter.Reset()
	seq := emitter.seq
	emitter.EmitRequest(request("waiting", agentstate.RequestStatusExpired))
	assert.Empty(t, emitter.Requests())
	assert.Equal(t, seq, emitter.seq)
	assert.NotContains(t, persister.requests, "waiting")
}

func TestEventEmitterRestore(t *testing.T) {
	emitter := NewEventEmitter()
	now := time.Now()
	emitter.Restore(EmitterState{
		Epoch:         "restored",
		Seq:           6,
		Messages:      []conv.Message{{Id: 0, Role: conv.RoleUser, Message: "one", Time: now}, {Id: 1, Role: conv.RoleAgent, Message: "two", Time: now}},
		MessageSeqs:   []uint64{1, 4},
		RichMessages:  []transcript.RichMessage{{MessageID: "m1", Role: "assistant"}},
		RichSeqs:      []uint64{5},
		SessionEvents: []transcript.SessionEvent{{EventID: 1}, {EventID: 7}},
		Requests:      []agentstate.RequestState{{Request: agentstate.Request{ID: "r1"}, Status: agentstate.RequestStatusPending}},
		RequestSeqs:   []uint64{6},
		SessionName:   "Greeting",
	})
	assert.Equal(t, "restored", emitter.Epoch())
	status := emitter.StatusSnapshot()
	assert.Equal(t, "Greeting", status.SessionName)
	assert.Equal(t, 1, status.PendingRequests)

	// A client that has everything resumes without a replay; one that
	// missed changes gets only those.
	_, _, state := emitter.SubscribeSince(6, "restored")
	assert.Equal(t, SessionSyncBody{Epoch: "restored", Seq: 6}, state[0].Payload)
	_, _, state = emitter.SubscribeSince(3, "restored")
	var seqs []uint64
	for _, event := range state[1:] {
		if seq := event.Seq(); seq != 0 {
			seqs = append(seqs, seq)
		}
	}
	assert.Equal(t, []uint64{4, 5, 6}, seqs)

	// New changes continue the restored numbering.
	emitter.EmitSessionEvents([]transcript.SessionEvent{{Kind: "text"}})
	assert.Equal(t, 8, emitter.SessionEvents()[2].EventID)
	_, ch, _ := emitter.Subscribe()
	later := now.Add(time.Second)
	emitter.EmitMessages(append(emitter.messages, conv.Message{Id: 2, Role: conv.RoleUser, Message: "three", Time: later}))
	events := drain(ch)
	assert.Equal(t, []string{"message 2", "task 0", "task 2"}, replayed(events), "the restored messages are not sent again; the restored one's task is done")
	assert.Equal(t, MessageUpdateBody{Id: 2, Role: conv.RoleUser, Message: "three", Time: later, Seq: 7}, events[0].Payload)
	tasks := emitter.Tasks()
	assert.Equal(t, instanceapi.TaskStatusCompleted, tasks[0].Status)
	assert.Equal(t, instanceapi.TaskStatusRunning, tasks[1].Status)
}

// The emitter keeps the most recently closed requests, as the conversation
// does: a request that waited long isn't dropped as it closes.
func TestEventEmitterKeepsTheMostRecentlyClosedRequests(t *testing.T) {
	emitter := NewEventEmitter()
	at := func(second int) *time.Time {
		v := time.Date(2026, 10, 18, 9, 0, second, 0, time.UTC)
		return &v
	}
	state := func(id string, status agentstate.RequestStatus, closedAt *time.Time) agentstate.RequestState {
		return agentstate.RequestState{Request: agentstate.Request{ID: id, Kind: agentstate.RequestKindApproval}, Status: status, ClosedAt: closedAt}
	}
	emitter.EmitRequest(state("long", agentstate.RequestStatusPending, nil))
	for i := range agentstate.MaxClosedRequests {
		id := fmt.Sprintf("r%d", i)
		emitter.EmitRequest(state(id, agentstate.RequestStatusPending, nil))
		emitter.EmitRequest(state(id, agentstate.RequestStatusResolved, at(i)))
	}
	emitter.EmitRequest(state("long", agentstate.RequestStatusExpired, at(59)))

	requests := emitter.Requests()
	require.Len(t, requests, agentstate.MaxClosedRequests)
	assert.Equal(t, "long", requests[0].ID)
	_, ok := emitter.Request("r0")
	assert.False(t, ok, "the first to close is dropped")
}

// A rich message without content is sent with an empty list, never null:
// clients iterate it.
func TestEmitter_RichMessageContentIsNeverNull(t *testing.T) {
	t.Parallel()
	e := NewEventEmitter()
	e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m1", Role: "user"})
	e.EmitRichMessage(transcript.RichMessage{MessageID: "m2", Role: "assistant"})
	e.EmitRichMessage(transcript.RichMessage{MessageID: "m2", Role: "assistant"})
	for _, msg := range e.RichMessages() {
		data, err := json.Marshal(msg)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"content":[]`, msg.MessageID)
	}
	restored := NewEventEmitter()
	restored.Restore(EmitterState{RichMessages: []transcript.RichMessage{{MessageID: "m3", Role: "user"}}, RichSeqs: []uint64{1}})
	assert.NotNil(t, restored.RichMessages()[0].Content)
}

// Restarting, exited and failed stay while the conversation's status
// changes (the old agent may still report while it restarts), until the
// server releases them.
func TestEventEmitterHoldsServerLifecycle(t *testing.T) {
	emitter := NewEventEmitter()
	emitter.EmitStatus(conv.StatusStable)
	require.Equal(t, instanceapi.LifecycleReady, emitter.StatusSnapshot().Lifecycle)

	emitter.SetLifecycle(instanceapi.LifecycleRestarting)
	emitter.EmitStatus(conv.StatusChanging)
	status := emitter.StatusSnapshot()
	assert.Equal(t, instanceapi.LifecycleRestarting, status.Lifecycle, "held")
	assert.Equal(t, instanceapi.AgentStatusRunning, status.Status, "the agent status still follows")
	emitter.EmitStatus(conv.StatusStable)
	assert.Equal(t, instanceapi.LifecycleRestarting, emitter.StatusSnapshot().Lifecycle)

	emitter.ReleaseLifecycle()
	assert.Equal(t, instanceapi.LifecycleReady, emitter.StatusSnapshot().Lifecycle, "the conversation's again")
	emitter.EmitStatus(conv.StatusChanging)
	assert.Equal(t, instanceapi.LifecycleRunning, emitter.StatusSnapshot().Lifecycle)

	for _, lifecycle := range []instanceapi.LifecycleState{instanceapi.LifecycleExited, instanceapi.LifecycleFailed} {
		emitter.SetLifecycle(lifecycle)
		emitter.EmitStatus(conv.StatusStable)
		emitter.EmitStatus(conv.StatusChanging)
		assert.Equal(t, lifecycle, emitter.StatusSnapshot().Lifecycle)
	}

	// Another lifecycle the server sets ends the hold.
	emitter.SetLifecycle(instanceapi.LifecycleStarting)
	emitter.EmitStatus(conv.StatusStable)
	assert.Equal(t, instanceapi.LifecycleReady, emitter.StatusSnapshot().Lifecycle)
}
