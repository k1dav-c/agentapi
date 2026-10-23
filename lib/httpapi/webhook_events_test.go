package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

func testDispatcher(t *testing.T, config WebhookConfig, clock quartz.Clock) *webhookDispatcher {
	t.Helper()
	if config.URL == "" {
		config.URL = "https://example.com/hook"
	}
	d, err := newWebhookDispatcher(config, slog.New(slog.NewTextHandler(io.Discard, nil)), mf.AgentTypeClaude, TransportNative, clock)
	require.NoError(t, err)
	return d
}

// queued takes the events queued so far.
func queued(d *webhookDispatcher) []WebhookEvent {
	var events []WebhookEvent
	for {
		select {
		case event := <-d.queue:
			events = append(events, event)
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

	require.NoError(t, WebhookConfig{Events: []string{"task.*", "request.opened", "*"}}.validate())
	require.ErrorContains(t, WebhookConfig{Events: []string{"task.done"}}.validate(), `unknown webhook event "task.done"`)
	require.ErrorContains(t, WebhookConfig{PublicURL: "ftp://x"}.validate(), "public URL must use http or https")

	// Without events chosen only run.status_changed is sent, as before.
	d := testDispatcher(t, WebhookConfig{}, quartz.NewReal())
	assert.True(t, d.wants(webhookEventRunStatusChanged))
	assert.False(t, d.wants(webhookEventTaskCompleted))
	d = testDispatcher(t, WebhookConfig{Events: []string{"task.*"}}, quartz.NewReal())
	assert.False(t, d.wants(webhookEventRunStatusChanged))
	assert.True(t, d.wants(webhookEventTaskFailed))
}

// A task: the user's message, the agent's work and its reply, until the
// agent is stable again.
// observed is an emitter, and an observer that takes in what it sends.
type observed struct {
	emitter *EventEmitter
	o       *webhookObserver
	events  <-chan Event
}

func newObserved(t *testing.T, d *webhookDispatcher, clock quartz.Clock) *observed {
	t.Helper()
	emitter := NewEventEmitter(WithClock(clock), WithSubscriptionBufSize(256))
	o := newWebhookObserver(d, emitter, clock.Now())
	o.clock = clock
	_, events, replay := emitter.SubscribeTo(webhookObservedEvents, 0, "")
	for _, event := range replay {
		o.handle(event, true)
	}
	return &observed{emitter: emitter, o: o, events: events}
}

// pump hands the observer what the emitter sent since the last time.
func (x *observed) pump() {
	for {
		select {
		case event := <-x.events:
			x.o.handle(event, false)
		default:
			return
		}
	}
}

func TestWebhookObserverTask(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	start := clock.Now()
	d := testDispatcher(t, WebhookConfig{Events: []string{"*"}}, clock)
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
	assert.Equal(t, TaskStatusRunning, started.Status)

	tool := events[2].Data.(WebhookToolCall)
	assert.Equal(t, "Bash", tool.Name)
	assert.Equal(t, "ok", tool.Result)
	assert.JSONEq(t, `{"command":"go test"}`, string(tool.Input.(json.RawMessage)))

	reply := events[3].Data.(WebhookMessage)
	assert.Equal(t, "Fixed it.", reply.Content)

	done := events[4].Data.(WebhookTask)
	assert.Equal(t, TaskStatusCompleted, done.Status)
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
	d := testDispatcher(t, WebhookConfig{Events: []string{"request.*", "login.*", "limit.*", "task.completed", "task.failed", "agent.error"}}, clock)
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

	// A terminal dialog is published once it settled on the screen.
	e.mu.Lock()
	e.updateTerminalPromptLocked("1. Yes\n2. No")
	e.mu.Unlock()
	clock.Advance(terminalPromptSettle).MustWait(t.Context())
	e.mu.Lock()
	e.updateTerminalPromptLocked("")
	e.mu.Unlock()

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
		webhookEventRequestOpened, webhookEventRequestClosed,
		webhookEventAgentError,
		webhookEventLimitStopped, webhookEventTaskFailed,
	}, eventTypes(events))
	prompt := events[7].Data.(WebhookTerminalPrompt)
	assert.Equal(t, "terminal_prompt", prompt.Kind)
	assert.Equal(t, "1. Yes\n2. No", prompt.Prompt)
	assert.Equal(t, prompt.ID, events[8].Data.(WebhookTerminalPrompt).ID)
	task := events[11].Data.(WebhookTask)
	assert.Equal(t, TaskStatusLimited, task.Status)
	assert.Empty(t, task.Errors, "a warning doesn't fail a task")
}

// The conversation the observer starts with, and a resumed session's, is
// history: nothing is sent for it.
func TestWebhookObserverHistory(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, WebhookConfig{Events: []string{"*"}}, clock)
	emitter := NewEventEmitter(WithClock(clock))
	old := clock.Now().Add(-time.Hour)
	emitter.EmitMessages([]conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: conv.RoleAgent, Message: "Done earlier", Time: old},
	})
	emitter.EmitStatus(conv.StatusStable)
	o := newWebhookObserver(d, emitter, clock.Now())
	o.clock = clock

	ctx := t.Context()
	go o.run(ctx)
	require.Eventually(t, func() bool {
		emitter.mu.Lock()
		defer emitter.mu.Unlock()
		return len(emitter.chans) == 1
	}, 5*time.Second, 10*time.Millisecond)
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
	d := testDispatcher(t, WebhookConfig{Events: []string{"message.created", "task.started", "agent.error"}}, clock)
	emitter := NewEventEmitter(WithClock(clock))
	emitter.EmitStatus(conv.StatusStable)
	o := newWebhookObserver(d, emitter, clock.Now())
	o.clock = clock
	subscriptions := func() int {
		emitter.mu.Lock()
		defer emitter.mu.Unlock()
		return emitter.chanIdx
	}
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
	assert.Equal(t, "the agent failed", events[4].Data.(ErrorBody).Message)
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, queued(d), "nothing was sent twice")
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
	d := testDispatcher(t, WebhookConfig{URL: server.URL, Secret: "s3cret", Events: []string{"*"}}, quartz.NewReal())
	d.setInstance(WebhookInstance{ID: "host:3284"})
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{Task: Task{ID: 1, Prompt: "Hi"}}))
	d.deliver(t.Context(), <-d.queue)

	timestamp := header.Get("X-AgentAPI-Timestamp")
	assert.Equal(t, webhookSignature("s3cret", timestamp, body), header.Get("X-AgentAPI-Signature-256"))
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
	assert.Equal(t, mf.AgentTypeClaude, event.Instance.AgentType)
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
	d := testDispatcher(t, WebhookConfig{URL: server.URL, MaxAttempts: 5}, quartz.NewReal())
	d.deliver(t.Context(), WebhookEvent{ID: "e1", Type: webhookEventRunStatusChanged, CreatedAt: time.Now()})
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, 30*time.Second, webhookRetryDelay(10))
	assert.Equal(t, 4*time.Second, webhookRetryDelay(3))
}

func TestWebhookTemplateData(t *testing.T) {
	t.Parallel()
	d := testDispatcher(t, WebhookConfig{Events: []string{"*"}, PayloadTemplate: `{"text":"{{.Instance.ID}}: {{.Data.prompt}}"}`}, quartz.NewReal())
	d.setInstance(WebhookInstance{ID: "box:1"})
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{Task: Task{Prompt: "Hi"}}))
	body, err := d.renderBody(<-d.queue)
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
	d := testDispatcher(t, WebhookConfig{URL: server.URL, Events: []string{"instance.*"}, Heartbeat: -1}, quartz.NewReal())
	d.snapshot = func() any { return WebhookInstanceState{Status: AgentStatusStable} }
	ctx := t.Context()
	d.start(ctx)
	d.stop(ctx)
	assert.Equal(t, webhookEventInstanceStarted, <-got)
	select {
	case event := <-got:
		assert.Equal(t, webhookEventInstanceStopped, event)
	default:
		t.Fatal("instance.stopped wasn't delivered before stop returned")
	}
}

func TestWebhookHeartbeat(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, WebhookConfig{Events: []string{"instance.heartbeat"}, Heartbeat: time.Minute}, clock)
	d.snapshot = func() any { return WebhookInstanceState{Status: AgentStatusRunning} }
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
	assert.Equal(t, AgentStatusRunning, events[0].Data.(WebhookInstanceState).Status)
}

// A test event goes out once, whatever the events chosen, and says how the
// receiver answered.
func TestWebhookTestEvent(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var event string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		event = r.Header.Get("X-AgentAPI-Event")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	d := testDispatcher(t, WebhookConfig{URL: server.URL, Events: []string{"task.*"}}, quartz.NewReal())
	s := &webhookAPI{webhook: d}
	response, err := s.testWebhook(t.Context(), nil)
	require.NoError(t, err)
	assert.False(t, response.Body.OK)
	assert.Equal(t, "503 Service Unavailable", response.Body.Status)
	assert.Contains(t, response.Body.Error, "503")
	assert.EqualValues(t, 1, calls.Load(), "not retried")
	assert.Equal(t, webhookEventTest, event)

	require.NoError(t, d.updateConfig(WebhookConfig{}))
	response, err = s.testWebhook(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "no webhook URL is set", response.Body.Error)
}
