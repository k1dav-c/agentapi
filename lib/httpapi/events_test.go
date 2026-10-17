package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventEmitterSessionEvents(t *testing.T) {
	emitter := NewEventEmitter()
	emitter.EmitSessionEvents([]jsonlwatcher.SessionEvent{
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

func TestEventEmitterMergesClaudeRichMessageDeltas(t *testing.T) {
	emitter := NewEventEmitter(WithSubscriptionBufSize(10))
	_, ch, _ := emitter.Subscribe()

	emitter.EmitRichMessage(jsonlwatcher.RichMessage{
		MessageID: "msg_1",
		Role:      "assistant",
		Content: []jsonlwatcher.RichContentBlock{
			{Type: "thinking", Thinking: "checking files"},
			{Type: "text", Text: "1. Read files"},
			{Type: "tool_use", ToolUseID: "tool_1", ToolName: "Read"},
		},
	})
	<-ch

	emitter.EmitRichMessage(jsonlwatcher.RichMessage{
		MessageID: "msg_1",
		Role:      "assistant",
		Content: []jsonlwatcher.RichContentBlock{
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
				Payload: StatusChangeBody{Status: AgentStatusRunning, Lifecycle: LifecycleStarting, Version: version.Version},
			},
			{
				Type:    EventTypeScreenUpdate,
				Payload: ScreenUpdateBody{Screen: ""},
			},
		}, stateEvents)

		now := time.Now()
		emitter.EmitMessages([]st.ConversationMessage{
			{Id: 1, Message: "Hello, world!", Role: st.ConversationRoleUser, Time: now},
		})
		newEvent := <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 1, Message: "Hello, world!", Role: st.ConversationRoleUser, Time: now, Seq: 1},
		}, newEvent)

		emitter.EmitMessages([]st.ConversationMessage{
			{Id: 1, Message: "Hello, world! (updated)", Role: st.ConversationRoleUser, Time: now},
			{Id: 2, Message: "What's up?", Role: st.ConversationRoleAgent, Time: now},
		})
		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 1, Message: "Hello, world! (updated)", Role: st.ConversationRoleUser, Time: now, Seq: 2},
		}, newEvent)

		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: 2, Message: "What's up?", Role: st.ConversationRoleAgent, Time: now, Seq: 3},
		}, newEvent)

		emitter.EmitStatus(st.ConversationStatusStable)
		newEvent = <-ch
		assert.Equal(t, Event{
			Type:    EventTypeStatusChange,
			Payload: StatusChangeBody{Status: AgentStatusStable, Lifecycle: LifecycleReady, AgentType: "", Version: version.Version},
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

		emitter.EmitMessages([]st.ConversationMessage{
			{Id: 1, Message: "Hello, world!", Role: st.ConversationRoleUser, Time: now},
		})
		for _, ch := range channels {
			newEvent := <-ch
			assert.Equal(t, Event{
				Type:    EventTypeMessageUpdate,
				Payload: MessageUpdateBody{Id: 1, Message: "Hello, world!", Role: st.ConversationRoleUser, Time: now, Seq: 1},
			}, newEvent)
		}
	})

	t.Run("close-channel", func(t *testing.T) {
		emitter := NewEventEmitter(WithSubscriptionBufSize(1))
		_, ch, _ := emitter.Subscribe()
		for i := range 5 {
			emitter.EmitMessages([]st.ConversationMessage{
				{Id: i, Message: fmt.Sprintf("Hello, world! %d", i), Role: st.ConversationRoleUser, Time: time.Now()},
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
			emitter.EmitError(fmt.Sprintf("error %d", i), st.ErrorLevelError)
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

		emitter.EmitError("err1", st.ErrorLevelError)
		mockClock.Set(fixedTime.Add(1 * time.Second))
		emitter.EmitError("err2", st.ErrorLevelWarning)
		mockClock.Set(fixedTime.Add(2 * time.Second))
		emitter.EmitError("err3", st.ErrorLevelError)

		_, _, stateEvents := emitter.Subscribe()

		var errorEvents []Event
		for _, ev := range stateEvents {
			if ev.Type == EventTypeError {
				errorEvents = append(errorEvents, ev)
			}
		}

		assert.Len(t, errorEvents, 3)

		expected := []ErrorBody{
			{Message: "err1", Level: st.ErrorLevelError, Time: fixedTime},
			{Message: "err2", Level: st.ErrorLevelWarning, Time: fixedTime.Add(1 * time.Second)},
			{Message: "err3", Level: st.ErrorLevelError, Time: fixedTime.Add(2 * time.Second)},
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

		// Verify initial state events: session sync, status, screen
		assert.Len(t, stateEvents, 3)

		// Emit an error and verify it uses the mock clock time
		emitter.EmitError("test error", st.ErrorLevelError)

		event := <-ch
		assert.Equal(t, EventTypeError, event.Type)
		errorBody, ok := event.Payload.(ErrorBody)
		assert.True(t, ok)
		assert.Equal(t, "test error", errorBody.Message)
		assert.Equal(t, st.ErrorLevelError, errorBody.Level)
		assert.Equal(t, fixedTime, errorBody.Time)

		// Advance the clock and emit another error
		newTime := fixedTime.Add(1 * time.Hour)
		mockClock.Set(newTime)
		emitter.EmitError("another error", st.ErrorLevelWarning)

		event = <-ch
		assert.Equal(t, EventTypeError, event.Type)
		errorBody, ok = event.Payload.(ErrorBody)
		assert.True(t, ok)
		assert.Equal(t, "another error", errorBody.Message)
		assert.Equal(t, st.ErrorLevelWarning, errorBody.Level)
		assert.Equal(t, newTime, errorBody.Time)
	})
}

func TestEventEmitterTerminalPrompt(t *testing.T) {
	clock := quartz.NewMock(t)
	emitter := NewEventEmitter(WithAgentType(mf.AgentTypeClaude), WithClock(clock))
	_, ch, _ := emitter.Subscribe()
	statusEvents := func() []StatusChangeBody {
		var bodies []StatusChangeBody
		for {
			select {
			case event := <-ch:
				if event.Type == EventTypeStatusChange {
					bodies = append(bodies, event.Payload.(StatusChangeBody))
				}
			default:
				return bodies
			}
		}
	}

	// A numbered list in an answer, with the input box below it, is not a
	// prompt, even when it mentions words like "select", "allow" or "approve".
	emitter.EmitScreen(readScreenFixture(t, "claude_numbered_answer.txt"))
	require.Empty(t, emitter.StatusSnapshot().TerminalPrompt)
	require.Empty(t, statusEvents())

	for _, fixture := range []string{"claude_permission_dialog.txt", "claude_trust_dialog.txt"} {
		hadPrompt := emitter.StatusSnapshot().TerminalPrompt != ""
		emitter.EmitScreen(readScreenFixture(t, fixture))
		// A previous prompt is cleared at once; the new one is published
		// only once it has stayed on screen for a while.
		require.Empty(t, emitter.StatusSnapshot().TerminalPrompt, fixture)
		require.Len(t, statusEvents(), map[bool]int{true: 1, false: 0}[hadPrompt], fixture)
		clock.Advance(terminalPromptSettle).MustWait(context.Background())
		prompt := emitter.StatusSnapshot().TerminalPrompt
		require.Contains(t, prompt, "❯", fixture)
		events := statusEvents()
		require.Len(t, events, 1, fixture)
		require.Equal(t, prompt, events[0].TerminalPrompt)
	}

	// A prompt that disappears before it settles is never published.
	emitter.EmitScreen(readScreenFixture(t, "claude_numbered_answer.txt"))
	statusEvents()
	emitter.EmitScreen(readScreenFixture(t, "claude_trust_dialog.txt"))
	emitter.EmitScreen(readScreenFixture(t, "claude_numbered_answer.txt"))
	clock.Advance(terminalPromptSettle).MustWait(context.Background())
	require.Empty(t, emitter.StatusSnapshot().TerminalPrompt)
	require.Empty(t, statusEvents())

	emitter.EmitScreen(readScreenFixture(t, "claude_trust_dialog.txt"))
	clock.Advance(terminalPromptSettle).MustWait(context.Background())
	require.NotEmpty(t, statusEvents())

	// Back at the input box: the prompt is cleared at once and clients are told.
	emitter.EmitScreen(readScreenFixture(t, "claude_numbered_answer.txt"))
	require.Empty(t, emitter.StatusSnapshot().TerminalPrompt)
	events := statusEvents()
	require.Len(t, events, 1)
	require.Empty(t, events[0].TerminalPrompt)
}

func TestEventEmitterIncrementalReplay(t *testing.T) {
	emitter := NewEventEmitter(WithSubscriptionBufSize(10))
	now := time.Now()
	rich := func(id, text string) jsonlwatcher.RichMessage {
		return jsonlwatcher.RichMessage{MessageID: id, Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: text}}}
	}
	emitter.EmitMessages([]st.ConversationMessage{{Id: 0, Role: st.ConversationRoleUser, Message: "one", Time: now}})
	emitter.EmitRichMessage(rich("m1", "first"))
	emitter.EmitRichMessage(rich("m2", "second"))

	_, _, state := emitter.Subscribe()
	sync := state[0].Payload.(SessionSyncBody)
	require.True(t, sync.Full)
	require.Equal(t, uint64(3), sync.Seq)

	// Later changes: a new message, and an update to an existing rich message.
	emitter.EmitMessages([]st.ConversationMessage{
		{Id: 0, Role: st.ConversationRoleUser, Message: "one", Time: now},
		{Id: 1, Role: st.ConversationRoleAgent, Message: "two", Time: now},
	})
	emitter.EmitRichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "tool_use", ToolUseID: "t1"}}})

	kinds := func(events []Event) []string {
		var out []string
		for _, ev := range events {
			switch p := ev.Payload.(type) {
			case MessageUpdateBody:
				out = append(out, fmt.Sprintf("message %d", p.Id))
			case RichMessageUpdateBody:
				out = append(out, "rich "+p.MessageID)
			}
		}
		return out
	}

	// Resuming from the first replay gets only what changed since.
	_, _, state = emitter.SubscribeSince(sync.Seq, sync.Epoch)
	require.Equal(t, SessionSyncBody{Epoch: sync.Epoch, Full: false, Seq: 5}, state[0].Payload)
	require.Equal(t, []string{"message 1", "rich m1"}, kinds(state))
	updated := state[len(state)-1].Payload.(RichMessageUpdateBody)
	require.Len(t, updated.Content, 2, "the replayed rich message is the merged snapshot")

	// Up to date: nothing to replay.
	_, _, state = emitter.SubscribeSince(5, sync.Epoch)
	require.Equal(t, SessionSyncBody{Epoch: sync.Epoch, Full: false, Seq: 5}, state[0].Payload)
	require.Empty(t, kinds(state))

	// An unknown epoch, or a seq from the future, gets the full state.
	for _, tc := range []struct {
		since uint64
		epoch string
	}{{3, "other"}, {99, sync.Epoch}, {0, sync.Epoch}} {
		_, _, state = emitter.SubscribeSince(tc.since, tc.epoch)
		require.True(t, state[0].Payload.(SessionSyncBody).Full)
		require.Equal(t, []string{"message 0", "message 1", "rich m1", "rich m2"}, kinds(state))
	}

	// A reset starts a new epoch and tells connected clients right away.
	_, ch, _ := emitter.Subscribe()
	emitter.Reset()
	reset := (<-ch).Payload.(SessionSyncBody)
	require.True(t, reset.Full)
	require.Zero(t, reset.Seq, "nothing to wait for after a reset")
	require.NotEqual(t, sync.Epoch, reset.Epoch)
	_, _, state = emitter.SubscribeSince(5, sync.Epoch)
	require.True(t, state[0].Payload.(SessionSyncBody).Full)
	require.Empty(t, kinds(state))
}

// recordingPersister records what an emitter stores.
type recordingPersister struct {
	messages  map[int]st.ConversationMessage
	requests  map[string]native.RequestState
	truncated []int
	deleted   []string
	epoch     string
	seq       uint64
}

func newRecordingPersister() *recordingPersister {
	return &recordingPersister{messages: map[int]st.ConversationMessage{}, requests: map[string]native.RequestState{}}
}

func (p *recordingPersister) PutMessage(msg st.ConversationMessage, _ uint64) {
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

func (p *recordingPersister) PutRichMessage(int, jsonlwatcher.RichMessage, uint64) {}
func (p *recordingPersister) AppendSessionEvents([]jsonlwatcher.SessionEvent)      {}
func (p *recordingPersister) PutRequest(state native.RequestState, _ uint64) {
	p.requests[state.ID] = state
}

func (p *recordingPersister) DeleteRequest(id string) {
	p.deleted = append(p.deleted, id)
	delete(p.requests, id)
}

func (p *recordingPersister) SetSeq(epoch string, seq uint64) { p.epoch, p.seq = epoch, seq }
func (p *recordingPersister) SetSessionName(string)           {}
func (p *recordingPersister) SetSessionRef(string)            {}

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

func TestEventEmitterRemovedMessageResyncs(t *testing.T) {
	persister := newRecordingPersister()
	emitter := NewEventEmitter(WithSubscriptionBufSize(20), WithPersister(persister))
	now := time.Now()
	msgs := []st.ConversationMessage{
		{Id: 0, Role: st.ConversationRoleUser, Message: "one", Time: now},
		{Id: 1, Role: st.ConversationRoleAgent, Message: "two", Time: now},
		{Id: 2, Role: st.ConversationRoleUser, Message: "refused", Time: now},
	}
	emitter.EmitMessages(msgs)
	emitter.EmitRichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant"})
	emitter.EmitRequest(native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusPending})
	_, _, state := emitter.Subscribe()
	cached := state[0].Payload.(SessionSyncBody)
	_, ch, _ := emitter.Subscribe()

	// The agent refused the last message, and it was taken back. Updates
	// can't say so: connected clients get the full state under a new epoch.
	emitter.EmitMessages(msgs[:2])
	events := drain(ch)
	require.NotEmpty(t, events)
	sync, ok := events[0].Payload.(SessionSyncBody)
	require.True(t, ok, "the full state starts with a session_sync")
	assert.True(t, sync.Full)
	assert.NotEqual(t, cached.Epoch, sync.Epoch)
	assert.Equal(t, uint64(5), sync.Seq, "the replay ends with the newest change")
	var replayed []string
	for _, event := range events[1:] {
		switch p := event.Payload.(type) {
		case MessageUpdateBody:
			replayed = append(replayed, fmt.Sprintf("message %d", p.Id))
		case RichMessageUpdateBody:
			replayed = append(replayed, "rich "+p.MessageID)
		case RequestUpdateBody:
			replayed = append(replayed, "request "+p.ID)
		default:
			t.Errorf("unexpected %s event in the replay", event.Type)
		}
	}
	assert.Equal(t, []string{"message 0", "message 1", "rich m1", "request r1"}, replayed)
	assert.Equal(t, []int{2}, persister.truncated)
	assert.Equal(t, sync.Epoch, persister.epoch, "the new epoch is stored")

	// A client that cached the message and reconnects gets the full state.
	_, _, state = emitter.SubscribeSince(cached.Seq, cached.Epoch)
	assert.True(t, state[0].Payload.(SessionSyncBody).Full)

	// A message removed from the middle is gone from the stored rows too,
	// and the ones after it are stored again.
	msgs = append(msgs[:2:2], st.ConversationMessage{Id: 3, Role: st.ConversationRoleAgent, Message: "three", Time: now})
	emitter.EmitMessages(msgs)
	require.Len(t, drain(ch), 1, "an added message is an update")
	emitter.EmitMessages([]st.ConversationMessage{msgs[0], msgs[2]})
	assert.Equal(t, []int{2, 1}, persister.truncated)
	assert.Equal(t, map[int]st.ConversationMessage{0: msgs[0], 3: msgs[2]}, persister.messages)
	events = drain(ch)
	require.NotEmpty(t, events)
	assert.True(t, events[0].Payload.(SessionSyncBody).Full)
}

func TestEventEmitterRequests(t *testing.T) {
	persister := newRecordingPersister()
	emitter := NewEventEmitter(WithPersister(persister))
	request := func(id string, status native.RequestStatus) native.RequestState {
		return native.RequestState{Request: native.Request{ID: id, Kind: native.RequestKindApproval}, Status: status}
	}
	emitter.EmitRequest(request("waiting", native.RequestStatusPending))
	for i := range native.MaxClosedRequests + 2 {
		id := fmt.Sprintf("r%d", i)
		emitter.EmitRequest(request(id, native.RequestStatusPending))
		emitter.EmitRequest(request(id, native.RequestStatusResolved))
	}
	assert.Equal(t, 1, emitter.StatusSnapshot().PendingRequests)

	// The oldest closed requests are dropped; pending ones are kept.
	requests := emitter.Requests()
	require.Len(t, requests, native.MaxClosedRequests+1)
	assert.Equal(t, "waiting", requests[0].ID)
	assert.Equal(t, "r2", requests[1].ID)
	assert.Equal(t, []string{"r0", "r1"}, persister.deleted)
	assert.Len(t, persister.requests, native.MaxClosedRequests+1)
	_, ok := emitter.Request("r1")
	assert.False(t, ok)
	got, ok := emitter.Request("r2")
	require.True(t, ok)
	assert.Equal(t, native.RequestStatusResolved, got.Status)

	// A request closed after a reset belongs to the conversation that was
	// cleared; it doesn't come back.
	emitter.Reset()
	seq := emitter.seq
	emitter.EmitRequest(request("waiting", native.RequestStatusExpired))
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
		Messages:      []st.ConversationMessage{{Id: 0, Role: st.ConversationRoleUser, Message: "one", Time: now}, {Id: 1, Role: st.ConversationRoleAgent, Message: "two", Time: now}},
		MessageSeqs:   []uint64{1, 4},
		RichMessages:  []jsonlwatcher.RichMessage{{MessageID: "m1", Role: "assistant"}},
		RichSeqs:      []uint64{5},
		SessionEvents: []jsonlwatcher.SessionEvent{{EventID: 1}, {EventID: 7}},
		Requests:      []native.RequestState{{Request: native.Request{ID: "r1"}, Status: native.RequestStatusPending}},
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
	var replayed []uint64
	for _, event := range state[1:] {
		if seq := eventSeq(event); seq != 0 {
			replayed = append(replayed, seq)
		}
	}
	assert.Equal(t, []uint64{4, 5, 6}, replayed)

	// New changes continue the restored numbering.
	emitter.EmitSessionEvents([]jsonlwatcher.SessionEvent{{Kind: "text"}})
	assert.Equal(t, 8, emitter.SessionEvents()[2].EventID)
	_, ch, _ := emitter.Subscribe()
	emitter.EmitMessages(append(emitter.messages, st.ConversationMessage{Id: 2, Role: st.ConversationRoleUser, Message: "three", Time: now}))
	events := drain(ch)
	require.Len(t, events, 1, "the restored messages are not sent again")
	assert.Equal(t, MessageUpdateBody{Id: 2, Role: st.ConversationRoleUser, Message: "three", Time: now, Seq: 7}, events[0].Payload)
}
