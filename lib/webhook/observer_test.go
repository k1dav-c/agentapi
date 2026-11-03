package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

func testDispatcher(t *testing.T, config Config, clock quartz.Clock) *Dispatcher {
	t.Helper()
	if config.URL == "" {
		config.URL = "https://example.com/hook"
	}
	d, err := New(config, slog.New(slog.NewTextHandler(io.Discard, nil)), instanceapi.AgentTypeClaude, clock)
	require.NoError(t, err)
	return d
}

// queued takes the events queued so far.
func queued(d *Dispatcher) []WebhookEvent {
	var events []WebhookEvent
	for {
		select {
		case event := <-d.queue:
			events = append(events, event.WebhookEvent)
		default:
			return events
		}
	}
}

func eventTypes(events []WebhookEvent) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}

func TestWebhookEventPatterns(t *testing.T) {
	t.Parallel()
	assert.True(t, webhookEventMatches("*", "task.completed"))
	assert.True(t, webhookEventMatches("task.*", "task.completed"))
	assert.True(t, webhookEventMatches("task.completed", "task.completed"))
	assert.False(t, webhookEventMatches("task.*", "tool.completed"))
	assert.False(t, webhookEventMatches("task", "task.completed"))

	require.NoError(t, Config{Events: []string{"task.*", "request.opened", "*"}}.validate())
	require.ErrorContains(t, Config{Events: []string{"task.done"}}.validate(), `unknown webhook event "task.done"`)
	require.ErrorContains(t, Config{PublicURL: "ftp://x"}.validate(), "public URL must use http or https")

	// Without events chosen only run.status_changed is sent, as before.
	d := testDispatcher(t, Config{}, quartz.NewReal())
	assert.True(t, d.wants(webhookEventRunStatusChanged))
	assert.False(t, d.wants(webhookEventTaskCompleted))
	d = testDispatcher(t, Config{Events: []string{"task.*"}}, quartz.NewReal())
	assert.False(t, d.wants(webhookEventRunStatusChanged))
	assert.True(t, d.wants(webhookEventTaskFailed))
}

// A task: the user's message, the agent's work and its reply, until the
// agent is stable again.
// observed is an emitter, and an observer that takes in what it sends.
type observed struct {
	emitter *convstate.EventEmitter
	o       *webhookObserver
	events  <-chan convstate.Event
}

func newObserved(t *testing.T, d *Dispatcher, clock quartz.Clock) *observed {
	t.Helper()
	emitter := convstate.NewEventEmitter(convstate.WithClock(clock), convstate.WithSubscriptionBufSize(256))
	o := newWebhookObserver(d, emitter, clock.Now())
	_, events, replay := o.subscribe()
	o.replay(replay, true)
	return &observed{emitter: emitter, o: o, events: events}
}

// countingSource counts the subscriptions an observer took, once each is
// open.
type countingSource struct {
	*convstate.EventEmitter
	count atomic.Int32
}

func (s *countingSource) SubscribeTo(wants func(convstate.EventType) bool, since uint64, epoch string) (int, <-chan convstate.Event, []convstate.Event) {
	id, events, replay := s.EventEmitter.SubscribeTo(wants, since, epoch)
	s.count.Add(1)
	return id, events, replay
}

func (s *countingSource) subscriptions() int { return int(s.count.Load()) }

// pump hands the observer what the emitter sent since the last time.
func (x *observed) pump() {
	for {
		select {
		case event, ok := <-x.events:
			if !ok {
				return
			}
			x.o.take(event)
		default:
			return
		}
	}
}

func TestWebhookObserverTask(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	start := clock.Now()
	d := testDispatcher(t, Config{Events: []string{"*"}}, clock)
	x := newObserved(t, d, clock)
	x.o.taskCost = func(since time.Time) *WebhookCost {
		assert.Equal(t, start, since)
		return &WebhookCost{USD: 0.25, Calls: 2}
	}
	e := x.emitter
	e.EmitStatus(conv.StatusStable)
	e.SetSessionName("Fix the bug")
	x.pump()
	queued(d)

	prompt := conv.Message{Id: 0, Role: conv.RoleUser, Message: "Fix the bug", Time: start}
	e.EmitMessages([]conv.Message{prompt})
	e.EmitStatus(conv.StatusChanging)
	failed := false
	e.EmitRichMessage(transcript.RichMessage{MessageID: "a1", Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test"}`)},
	}})
	// Still running: no result yet.
	e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "r1", Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Text: "ok", Status: "running"},
	}})
	e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "r1", Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Text: "ok", Status: "completed", IsError: &failed},
	}})
	// The reply streams in: sent once it is final.
	e.EmitMessages([]conv.Message{prompt, {Id: 1, Role: conv.RoleAgent, Message: "Fix", Time: start}})
	e.EmitMessages([]conv.Message{prompt, {Id: 1, Role: conv.RoleAgent, Message: "Fixed it.", Time: start}})
	clock.Advance(90 * time.Second)
	e.EmitStatus(conv.StatusStable)
	x.pump()

	events := queued(d)
	require.Equal(t, []string{
		webhookEventMessageCreated, webhookEventTaskStarted, webhookEventToolCompleted,
		webhookEventMessageCreated, webhookEventTaskCompleted,
	}, eventTypes(events))

	started := events[1].Data.(WebhookTask)
	assert.Equal(t, "Fix the bug", started.Prompt)
	assert.Equal(t, convstate.TaskStatusRunning, started.Status)

	tool := events[2].Data.(WebhookToolCall)
	assert.Equal(t, "Bash", tool.Name)
	assert.Equal(t, "ok", tool.Result)
	assert.JSONEq(t, `{"command":"go test"}`, string(tool.Input.(json.RawMessage)))

	reply := events[3].Data.(WebhookMessage)
	assert.Equal(t, "Fixed it.", reply.Content)

	done := events[4].Data.(WebhookTask)
	assert.Equal(t, convstate.TaskStatusCompleted, done.Status)
	assert.Equal(t, "Fixed it.", done.Reply)
	assert.Equal(t, 1, *done.ReplyID)
	assert.Equal(t, 90.0, done.DurationSeconds)
	assert.Equal(t, 0.25, done.Cost.USD)
	assert.Equal(t, "Fix the bug", done.SessionName)
	assert.NotNil(t, events[4].Instance)
}

// What needs a person: requests, a login, a usage limit, a terminal
// dialog; and a task a limit stopped fails.
func TestWebhookObserverNeedsYou(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	start := clock.Now()
	d := testDispatcher(t, Config{Events: []string{"request.*", "login.*", "limit.*", "task.completed", "task.failed", "agent.error"}}, clock)
	x := newObserved(t, d, clock)
	e := x.emitter
	e.EmitStatus(conv.StatusStable)

	request := native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusPending}
	e.EmitRequest(request)
	e.EmitRequest(request)
	request.Status = native.RequestStatusResolved
	e.EmitRequest(request)

	// A turn waiting on an approval goes on once it is answered: the
	// agent is idle for a moment in between.
	deploy := conv.Message{Id: 0, Role: conv.RoleUser, Message: "Deploy", Time: start}
	e.EmitMessages([]conv.Message{deploy})
	e.EmitStatus(conv.StatusChanging)
	approval := native.RequestState{Request: native.Request{ID: "r2"}, Status: native.RequestStatusPending}
	e.EmitRequest(approval)
	e.EmitStatus(conv.StatusStable)
	approval.Status = native.RequestStatusResolved
	e.EmitRequest(approval)
	e.EmitStatus(conv.StatusChanging)
	e.EmitStatus(conv.StatusStable)

	e.SetLoginRequired(&native.LoginRequired{Agent: "Claude Code", Command: "claude auth login"})
	e.SetLoginRequired(nil)

	goOn := conv.Message{Id: 1, Role: conv.RoleUser, Message: "Go on", Time: start}
	e.EmitMessages([]conv.Message{deploy, goOn})
	e.EmitStatus(conv.StatusChanging)
	e.EmitError("warned", conv.ErrorLevelWarning)
	e.SetLimitStop(&native.LimitStop{Message: "You've hit your limit", StoppedAt: start})
	e.EmitStatus(conv.StatusStable)
	x.pump()

	events := queued(d)
	require.Equal(t, []string{
		webhookEventRequestOpened, webhookEventRequestClosed,
		webhookEventRequestOpened, webhookEventRequestClosed, webhookEventTaskCompleted,
		webhookEventLoginRequired, webhookEventLoginCompleted,
		webhookEventAgentError,
		webhookEventLimitStopped, webhookEventTaskFailed,
	}, eventTypes(events))
	task := events[9].Data.(WebhookTask)
	assert.Equal(t, convstate.TaskStatusLimited, task.Status)
	assert.Empty(t, task.Errors, "a warning doesn't fail a task")
}

// The conversation the observer starts with, and a resumed session's, is
// history: nothing is sent for it.
func TestWebhookObserverHistory(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"*"}}, clock)
	emitter := convstate.NewEventEmitter(convstate.WithClock(clock))
	old := clock.Now().Add(-time.Hour)
	emitter.EmitMessages([]conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: conv.RoleAgent, Message: "Done earlier", Time: old},
	})
	emitter.EmitStatus(conv.StatusStable)
	o := newWebhookObserver(d, emitter, clock.Now())

	ctx := t.Context()
	go o.run(ctx)
	require.Eventually(t, func() bool { return emitter.Subscribers() == 1 }, 5*time.Second, 10*time.Millisecond)
	// A resumed session's messages are old too.
	emitter.EmitMessages([]conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: conv.RoleAgent, Message: "Done earlier", Time: old},
		{Id: 2, Role: conv.RoleUser, Message: "Resumed", Time: old.Add(time.Minute)},
	})
	now := clock.Now()
	emitter.EmitMessages([]conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: conv.RoleAgent, Message: "Done earlier", Time: old},
		{Id: 2, Role: conv.RoleUser, Message: "Resumed", Time: old.Add(time.Minute)},
		{Id: 3, Role: conv.RoleUser, Message: "New", Time: now},
	})
	var events []WebhookEvent
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= 2
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{webhookEventMessageCreated, webhookEventTaskStarted}, eventTypes(events))
	assert.Equal(t, "New", events[0].Data.(WebhookMessage).Content)
}

// A subscription the emitter ends, here because a message was taken back,
// is taken up again. The new one replays what was sent already, which isn't
// sent again, and what changed meanwhile goes out.
func TestWebhookObserverResubscribes(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"message.created", "task.started", "agent.error"}}, clock)
	emitter := convstate.NewEventEmitter(convstate.WithClock(clock))
	emitter.EmitStatus(conv.StatusStable)
	source := &countingSource{EventEmitter: emitter}
	o := newWebhookObserver(d, source, clock.Now())
	subscriptions := source.subscriptions
	go o.run(t.Context())
	require.Eventually(t, func() bool { return subscriptions() == 1 }, 5*time.Second, 10*time.Millisecond)

	now := clock.Now()
	msgs := []conv.Message{{Id: 0, Role: conv.RoleUser, Message: "Fix the bug", Time: now}}
	emitter.EmitMessages(msgs)
	emitter.EmitError("the agent warned", conv.ErrorLevelWarning)
	emitter.EmitMessages(append(msgs, conv.Message{Id: 1, Role: conv.RoleUser, Message: "Refused", Time: now}))
	var events []WebhookEvent
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= 4
	}, 5*time.Second, 10*time.Millisecond)

	emitter.EmitMessages(msgs)
	require.Eventually(t, func() bool { return subscriptions() == 2 }, 5*time.Second, 10*time.Millisecond, "subscribed again")
	emitter.EmitError("the agent failed", conv.ErrorLevelError)
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= 5
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{
		webhookEventMessageCreated, webhookEventTaskStarted, webhookEventAgentError, webhookEventMessageCreated,
		webhookEventAgentError,
	}, eventTypes(events))
	assert.Equal(t, "the agent failed", events[4].Data.(convstate.ErrorBody).Message)
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, queued(d), "nothing was sent twice")
}

// What the observer keeps doesn't grow with the conversation: calls that
// returned (but those of the latest messages, which may still grow),
// requests that closed, and a conversation a reset replaced are
// forgotten, and the calls waiting for a result are capped.
func TestWebhookObserverForgets(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"*"}}, clock)
	x := newObserved(t, d, clock)
	e := x.emitter
	e.EmitStatus(conv.StatusStable)
	start := clock.Now()
	e.EmitMessages([]conv.Message{{Id: 0, Role: conv.RoleUser, Message: "Go", Time: start}})
	e.EmitStatus(conv.StatusChanging)
	x.pump()
	for i := range 100 {
		id := fmt.Sprintf("t%d", i)
		e.EmitRichMessage(transcript.RichMessage{MessageID: "a" + id, Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_use", ToolUseID: id, ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"` + strings.Repeat("x", 1000) + `"}`)},
		}})
		e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "r" + id, Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_result", ToolUseID: id, Text: "ok", Status: "completed"},
		}})
		request := native.RequestState{Request: native.Request{ID: "q" + id}, Status: native.RequestStatusPending}
		e.EmitRequest(request)
		request.Status = native.RequestStatusResolved
		e.EmitRequest(request)
		x.pump()
	}
	assert.Empty(t, x.o.tools, "every call returned")
	assert.LessOrEqual(t, len(x.o.toolDone), maxToolMessages, "those of the latest messages")
	assert.LessOrEqual(t, len(x.o.toolMessages), maxToolMessages)
	assert.Empty(t, x.o.openRequests, "every request closed")
	// Calls whose result never comes.
	for i := range 2 * maxOpenTools {
		id := fmt.Sprintf("lost%d", i)
		e.EmitRichMessage(transcript.RichMessage{MessageID: "a" + id, Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_use", ToolUseID: id, ToolName: "Bash"},
		}})
		x.pump()
	}
	assert.Len(t, x.o.tools, maxOpenTools)
	assert.LessOrEqual(t, len(x.o.toolOrder), 2*maxOpenTools)
	completed := 0
	for _, event := range queued(d) {
		if event.Type == webhookEventToolCompleted {
			completed++
		}
	}
	assert.Equal(t, 100, completed, "each call once")

	// A reset (a restart, a session switch) forgets the conversation, and
	// the new one's IDs start over.
	clock.Advance(time.Minute)
	e.Reset()
	x.pump()
	assert.Empty(t, x.o.tools)
	assert.Empty(t, x.o.toolDone)
	assert.Empty(t, x.o.toolMessages)
	assert.Empty(t, x.o.replies)
	e.EmitMessages([]conv.Message{{Id: 0, Role: conv.RoleUser, Message: "Again", Time: clock.Now()}})
	x.pump()
	events := queued(d)
	require.Equal(t, []string{webhookEventMessageCreated, webhookEventTaskStarted}, eventTypes(events))
	assert.Equal(t, "Again", events[0].Data.(WebhookMessage).Content)
}

// A message taken back gives its ID to the next one, which is news; the
// messages before it, replayed when the observer subscribes again, aren't.
func TestWebhookObserverReusedID(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"message.created"}}, clock)
	x := newObserved(t, d, clock)
	e := x.emitter
	first := conv.Message{Id: 0, Role: conv.RoleUser, Message: "Fix the bug", Time: clock.Now()}
	e.EmitMessages([]conv.Message{first})
	clock.Advance(time.Second)
	e.EmitMessages([]conv.Message{first, {Id: 1, Role: conv.RoleUser, Message: "Refused", Time: clock.Now()}})
	x.pump()
	require.Len(t, queued(d), 2)

	e.EmitMessages([]conv.Message{first})
	_, events, replay := x.o.subscribe()
	x.events = events
	x.o.replay(replay, false)
	clock.Advance(time.Second)
	e.EmitMessages([]conv.Message{first, {Id: 1, Role: conv.RoleUser, Message: "Taken", Time: clock.Now()}})
	x.pump()
	sent := queued(d)
	require.Len(t, sent, 1)
	assert.Equal(t, "Taken", sent[0].Data.(WebhookMessage).Content)
}

// toolsCompleted counts the tool.completed events by call.
func toolsCompleted(events []WebhookEvent) map[string]int {
	completed := map[string]int{}
	for _, event := range events {
		if event.Type == webhookEventToolCompleted {
			completed[event.Data.(WebhookToolCall).ToolUseID]++
		}
	}
	return completed
}

// A rich message comes again, whole, as it grows: a Codex turn holds every
// call it made. Each result is sent once, also in a turn of more calls
// than the observer keeps messages, while other messages come and go.
func TestWebhookObserverGrowingMessage(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"tool.completed"}}, clock)
	x := newObserved(t, d, clock)
	e := x.emitter
	start := clock.Now()
	var turn []transcript.RichContentBlock
	calls := 3 * maxToolMessages
	for i := range calls {
		id := fmt.Sprintf("turn%d", i)
		turn = append(turn,
			transcript.RichContentBlock{Type: "tool_use", ToolUseID: id, ToolName: "Bash"},
			transcript.RichContentBlock{Type: "tool_result", ToolUseID: id, Text: "ok", Status: "completed"})
		e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "turn", Role: "assistant", Timestamp: start, Content: slices.Clone(turn)})
		other := fmt.Sprintf("other%d", i)
		e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: other, Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_result", ToolUseID: other, Text: "ok", Status: "completed"},
		}})
		x.pump()
	}
	completed := toolsCompleted(queued(d))
	assert.Len(t, completed, 2*calls)
	for id, n := range completed {
		assert.Equal(t, 1, n, id)
	}
	assert.LessOrEqual(t, len(x.o.toolMessages), maxToolMessages)
	assert.LessOrEqual(t, len(x.o.toolDone), calls+maxToolMessages, "the turn's and the latest others'")
}

// After the conversation was rebuilt (a message taken back), a new
// subscription replays all of it: calls that returned long ago aren't sent
// again, and one still running keeps its name and input.
func TestWebhookObserverResubscribesWithoutRepeats(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"tool.completed"}}, clock)
	emitter := convstate.NewEventEmitter(convstate.WithClock(clock))
	emitter.EmitStatus(conv.StatusStable)
	source := &countingSource{EventEmitter: emitter}
	o := newWebhookObserver(d, source, clock.Now())
	subscriptions := source.subscriptions
	go o.run(t.Context())
	require.Eventually(t, func() bool { return subscriptions() == 1 }, 5*time.Second, 10*time.Millisecond)

	start := clock.Now()
	msgs := []conv.Message{{Id: 0, Role: conv.RoleUser, Message: "Go", Time: start}}
	emitter.EmitMessages(msgs)
	calls := 2 * maxToolMessages
	for i := range calls {
		id := fmt.Sprintf("t%d", i)
		emitter.EmitRichMessage(transcript.RichMessage{MessageID: "a" + id, Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_use", ToolUseID: id, ToolName: "Bash"},
		}})
		emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "r" + id, Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "tool_result", ToolUseID: id, Text: "ok", Status: "completed"},
		}})
	}
	emitter.EmitRichMessage(transcript.RichMessage{MessageID: "a-open", Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_use", ToolUseID: "open", ToolName: "Edit", ToolInput: json.RawMessage(`{"file":"main.go"}`)},
	}})
	var events []WebhookEvent
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= calls
	}, 5*time.Second, 10*time.Millisecond)

	emitter.EmitMessages(append(msgs, conv.Message{Id: 1, Role: conv.RoleUser, Message: "Refused", Time: start}))
	emitter.EmitMessages(msgs)
	require.Eventually(t, func() bool { return subscriptions() == 2 }, 5*time.Second, 10*time.Millisecond, "subscribed again")
	emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "r-open", Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "open", Text: "edited", Status: "completed"},
	}})
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= calls+1
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	events = append(events, queued(d)...)

	completed := toolsCompleted(events)
	assert.Len(t, completed, calls+1)
	for id, n := range completed {
		assert.Equal(t, 1, n, id)
	}
	open := events[len(events)-1].Data.(WebhookToolCall)
	assert.Equal(t, "open", open.ToolUseID)
	assert.Equal(t, "Edit", open.Name)
	assert.JSONEq(t, `{"file":"main.go"}`, string(open.Input.(json.RawMessage)))
}

// A subscription taken up again replays what changed since the observer
// last read, not the whole conversation.
func TestWebhookObserverResumes(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"*"}}, clock)
	x := newObserved(t, d, clock)
	e := x.emitter
	start := clock.Now()
	e.EmitMessages([]conv.Message{{Id: 0, Role: conv.RoleUser, Message: "Go", Time: start}})
	for i := range 10 {
		e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: fmt.Sprintf("m%d", i), Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
			{Type: "text", Text: "working"},
		}})
	}
	x.pump()
	changes := func() []convstate.Event {
		id, _, replay := x.o.subscribe()
		e.Unsubscribe(id)
		var changed []convstate.Event
		for _, event := range replay {
			if event.Seq() != 0 {
				changed = append(changed, event)
			}
		}
		return changed
	}
	assert.Empty(t, changes(), "nothing changed since")

	e.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m3", Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "text", Text: "done"},
	}})
	changed := changes()
	require.Len(t, changed, 1)
	assert.Equal(t, "m3", changed[0].Payload.(convstate.RichMessageUpdateBody).MessageID)
}

// The observer runs while the webhook sends events it makes. A hub's
// webhook sends instance.* alone, which the dispatcher sends itself: the
// observer would follow the conversation for nothing.
func TestWebhookObserverRunsForItsEvents(t *testing.T) {
	t.Parallel()
	for _, event := range EventTypes {
		dispatcher := event == webhookEventRunStatusChanged || strings.HasPrefix(event, "instance.")
		assert.NotEqual(t, dispatcher, slices.Contains(webhookObserverEvents, event), "%s is the observer's or the dispatcher's", event)
	}
	assert.False(t, webhookObservedEvents(convstate.EventTypeSummaryUpdate), "the emitter needn't work out the summary for it")
}

func TestWebhookSignature(t *testing.T) {
	t.Parallel()
	var header http.Header
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d := testDispatcher(t, Config{URL: server.URL, Secret: "s3cret", Events: []string{"*"}}, quartz.NewReal())
	d.SetInstance(WebhookInstance{ID: "host:3284"})
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{Task: convstate.Task{ID: 1, Prompt: "Hi"}}))
	d.deliver(t.Context(), <-d.queue)

	timestamp := header.Get("X-AgentAPI-Timestamp")
	assert.Equal(t, instanceapi.Sign("s3cret", timestamp, body), header.Get("X-AgentAPI-Signature-256"))
	assert.True(t, strings.HasPrefix(header.Get("X-AgentAPI-Signature-256"), "sha256="))
	assert.Equal(t, "host:3284", header.Get("X-AgentAPI-Instance"))
	var event struct {
		Type     string          `json:"type"`
		Instance WebhookInstance `json:"instance"`
		Data     WebhookTask     `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &event))
	assert.Equal(t, webhookEventTaskStarted, event.Type)
	assert.Equal(t, "host:3284", event.Instance.ID)
	assert.Equal(t, instanceapi.AgentTypeClaude, event.Instance.AgentType)
	assert.Equal(t, "Hi", event.Data.Prompt)
}

// A receiver that refuses the event (a client error) isn't asked again.
func TestWebhookPermanentFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	d := testDispatcher(t, Config{URL: server.URL, MaxAttempts: 5}, quartz.NewReal())
	d.deliver(t.Context(), queuedEvent{WebhookEvent: WebhookEvent{ID: "e1", Type: webhookEventRunStatusChanged, CreatedAt: time.Now()}})
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, 30*time.Second, webhookRetryDelay(10))
	assert.Equal(t, 4*time.Second, webhookRetryDelay(3))
}

func TestWebhookTemplateData(t *testing.T) {
	t.Parallel()
	d := testDispatcher(t, Config{Events: []string{"*"}, PayloadTemplate: `{"text":"{{.Instance.ID}}: {{.Data.prompt}}"}`}, quartz.NewReal())
	d.SetInstance(WebhookInstance{ID: "box:1"})
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{Task: convstate.Task{Prompt: "Hi"}}))
	body, err := d.renderBody((<-d.queue).WebhookEvent)
	require.NoError(t, err)
	assert.JSONEq(t, `{"text":"box:1: Hi"}`, string(body))
}

// Shutting down sends instance.stopped and waits for it.
func TestWebhookStop(t *testing.T) {
	t.Parallel()
	got := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("X-AgentAPI-Event")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	d := testDispatcher(t, Config{URL: server.URL, Events: []string{"instance.*"}, Heartbeat: -1}, quartz.NewReal())
	d.snapshot = func() any { return WebhookInstanceState{Status: convstate.AgentStatusStable} }
	ctx := t.Context()
	d.Start(ctx)
	d.Stop(ctx)
	assert.Equal(t, webhookEventInstanceStarted, <-got)
	select {
	case event := <-got:
		assert.Equal(t, webhookEventInstanceStopped, event)
	default:
		t.Fatal("instance.stopped wasn't delivered before stop returned")
	}
}

// Waiting for instance.stopped takes at most half the time shutting down
// has: the HTTP server's drain and the state database get the rest.
func TestWebhookStopLeavesTimeForTheRest(t *testing.T) {
	t.Parallel()
	hang := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer server.Close()
	defer close(hang)
	// The mock clock's webhookStopTimeout never passes.
	d := testDispatcher(t, Config{URL: server.URL, Events: []string{"instance.stopped"}, Heartbeat: -1}, quartz.NewMock(t))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.Start(ctx)
	stopCtx, stopCancel := context.WithTimeout(t.Context(), time.Second)
	defer stopCancel()
	start := time.Now()
	d.Stop(stopCtx)
	assert.Less(t, time.Since(start), 900*time.Millisecond)
	assert.NoError(t, stopCtx.Err(), "time left for the rest")
}

func TestWebhookHeartbeat(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, Config{Events: []string{"instance.heartbeat"}, Heartbeat: time.Minute}, clock)
	d.snapshot = func() any { return WebhookInstanceState{Status: convstate.AgentStatusRunning} }
	d.SetInstance(WebhookInstance{ID: "host:3284"})
	trap := clock.Trap().NewTimer("webhook", "heartbeat")
	defer trap.Close()
	ctx := t.Context()
	go d.heartbeats(ctx)
	call := trap.MustWait(ctx)
	assert.Equal(t, time.Minute, call.Duration)
	call.Release()
	clock.Advance(time.Minute).MustWait(ctx)
	trap.MustWait(ctx).Release()
	events := queued(d)
	require.Len(t, events, 1)
	assert.Equal(t, webhookEventInstanceHeartbeat, events[0].Type)
	assert.Equal(t, convstate.AgentStatusRunning, events[0].Data.(WebhookInstanceState).Status)
	// A hub refuses an instance event that names no run.
	assert.Equal(t, d.serverID, events[0].Instance.ServerID)
	assert.NotEmpty(t, d.serverID)
}
