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

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
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
func TestWebhookObserverTask(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	start := clock.Now()
	d := testDispatcher(t, WebhookConfig{Events: []string{"*"}}, clock)
	emitter := NewEventEmitter(WithClock(clock))
	o := newWebhookObserver(d, emitter, start)
	o.clock = clock
	o.taskCost = func(since time.Time) *WebhookCost {
		assert.Equal(t, start, since)
		return &WebhookCost{USD: 0.25, Calls: 2}
	}
	status := func(s AgentStatus) Event {
		return Event{Type: EventTypeStatusChange, Payload: StatusChangeBody{Status: s, Lifecycle: LifecycleRunning, SessionName: "Fix the bug"}}
	}
	o.handle(status(AgentStatusStable), true)

	o.handle(Event{Payload: MessageUpdateBody{Id: 0, Role: st.ConversationRoleUser, Message: "Fix the bug", Time: start}}, false)
	o.handle(status(AgentStatusRunning), false)
	failed := false
	o.handle(Event{Payload: RichMessageUpdateBody{RichMessage: transcript.RichMessage{Role: "assistant", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test"}`)},
	}}}}, false)
	// Still running: no result yet.
	o.handle(Event{Payload: RichMessageUpdateBody{RichMessage: transcript.RichMessage{Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Text: "ok", Status: "running"},
	}}}}, false)
	o.handle(Event{Payload: RichMessageUpdateBody{RichMessage: transcript.RichMessage{Role: "user", Timestamp: start, Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Text: "ok", Status: "completed", IsError: &failed},
	}}}}, false)
	// The reply streams in: sent once it is final.
	o.handle(Event{Payload: MessageUpdateBody{Id: 1, Role: st.ConversationRoleAgent, Message: "Fix", Time: start}}, false)
	o.handle(Event{Payload: MessageUpdateBody{Id: 1, Role: st.ConversationRoleAgent, Message: "Fixed it.", Time: start}}, false)
	clock.Advance(90 * time.Second)
	o.handle(status(AgentStatusStable), false)

	events := queued(d)
	require.Equal(t, []string{
		webhookEventMessageCreated, webhookEventTaskStarted, webhookEventToolCompleted,
		webhookEventMessageCreated, webhookEventTaskCompleted,
	}, eventTypes(events))

	started := events[1].Data.(WebhookTask)
	assert.Equal(t, "Fix the bug", started.Prompt)
	assert.Empty(t, started.Outcome)

	tool := events[2].Data.(WebhookToolCall)
	assert.Equal(t, "Bash", tool.Name)
	assert.Equal(t, "ok", tool.Result)
	assert.JSONEq(t, `{"command":"go test"}`, string(tool.Input.(json.RawMessage)))

	reply := events[3].Data.(WebhookMessage)
	assert.Equal(t, "Fixed it.", reply.Content)

	done := events[4].Data.(WebhookTask)
	assert.Equal(t, "completed", done.Outcome)
	assert.Equal(t, "Fixed it.", done.Reply)
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
	o := newWebhookObserver(d, NewEventEmitter(WithClock(clock)), start)
	o.clock = clock
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable}}, true)

	request := native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusPending}
	o.handle(Event{Payload: RequestUpdateBody{RequestState: request}}, false)
	o.handle(Event{Payload: RequestUpdateBody{RequestState: request}}, false)
	request.Status = native.RequestStatusResolved
	o.handle(Event{Payload: RequestUpdateBody{RequestState: request}}, false)

	// A turn waiting on an approval goes on once it is answered.
	o.handle(Event{Payload: MessageUpdateBody{Id: 5, Role: st.ConversationRoleUser, Message: "Deploy", Time: start}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusRunning}}, false)
	approval := native.RequestState{Request: native.Request{ID: "r2"}, Status: native.RequestStatusPending}
	o.handle(Event{Payload: RequestUpdateBody{RequestState: approval}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable, PendingRequests: 1}}, false)
	approval.Status = native.RequestStatusResolved
	o.handle(Event{Payload: RequestUpdateBody{RequestState: approval}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusRunning}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable}}, false)

	login := &native.LoginRequired{Agent: "Claude Code", Command: "claude auth login"}
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable, LoginRequired: login}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable}}, false)

	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable, TerminalPrompt: "1. Yes\n2. No"}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable}}, false)

	o.handle(Event{Payload: MessageUpdateBody{Id: 0, Role: st.ConversationRoleUser, Message: "Go on", Time: start}}, false)
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusRunning}}, false)
	o.handle(Event{Payload: ErrorBody{Message: "warned", Level: st.ErrorLevelWarning, Time: start}}, false)
	limit := &native.LimitStop{Message: "You've hit your limit", StoppedAt: start.Add(time.Minute)}
	o.handle(Event{Payload: StatusChangeBody{Status: AgentStatusStable, LimitStop: limit}}, false)

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
	assert.Equal(t, "limited", task.Outcome)
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
	emitter.EmitMessages([]st.ConversationMessage{
		{Id: 0, Role: st.ConversationRoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: st.ConversationRoleAgent, Message: "Done earlier", Time: old},
	})
	emitter.EmitStatus(st.ConversationStatusStable)
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
	emitter.EmitMessages([]st.ConversationMessage{
		{Id: 0, Role: st.ConversationRoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: st.ConversationRoleAgent, Message: "Done earlier", Time: old},
		{Id: 2, Role: st.ConversationRoleUser, Message: "Resumed", Time: old.Add(time.Minute)},
	})
	now := clock.Now()
	emitter.EmitMessages([]st.ConversationMessage{
		{Id: 0, Role: st.ConversationRoleUser, Message: "Earlier", Time: old},
		{Id: 1, Role: st.ConversationRoleAgent, Message: "Done earlier", Time: old},
		{Id: 2, Role: st.ConversationRoleUser, Message: "Resumed", Time: old.Add(time.Minute)},
		{Id: 3, Role: st.ConversationRoleUser, Message: "New", Time: now},
	})
	var events []WebhookEvent
	require.Eventually(t, func() bool {
		events = append(events, queued(d)...)
		return len(events) >= 2
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{webhookEventMessageCreated, webhookEventTaskStarted}, eventTypes(events))
	assert.Equal(t, "New", events[0].Data.(WebhookMessage).Content)
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
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{ID: 1, Prompt: "Hi"}))
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
	require.True(t, d.emit(webhookEventTaskStarted, WebhookTask{Prompt: "Hi"}))
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
	s := &Server{webhook: d}
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
