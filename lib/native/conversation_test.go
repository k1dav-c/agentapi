package native_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	st "github.com/coder/agentapi/lib/screentracker"
)

type recorder struct {
	mu       sync.Mutex
	messages []st.ConversationMessage
	statuses []st.ConversationStatus
	errors   []string
	rich     []jsonlwatcher.RichMessage
	requests []native.RequestState
	refs     []string
	names    []string
}

func (r *recorder) EmitMessages(m []st.ConversationMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = m
}

func (r *recorder) EmitStatus(s st.ConversationStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, s)
}
func (r *recorder) EmitScreen(string) {}
func (r *recorder) EmitError(m string, _ st.ErrorLevel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, m)
}

func (r *recorder) Started(native.Capabilities)               {}
func (r *recorder) RichMessage(m jsonlwatcher.RichMessage)    { r.rich = append(r.rich, m) }
func (r *recorder) SessionEvents([]jsonlwatcher.SessionEvent) {}
func (r *recorder) SessionName(n string)                      { r.names = append(r.names, n) }
func (r *recorder) SessionRef(ref string)                     { r.refs = append(r.refs, ref) }
func (r *recorder) RequestUpdated(s native.RequestState)      { r.requests = append(r.requests, s) }

func (r *recorder) emittedMessages() []st.ConversationMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]st.ConversationMessage(nil), r.messages...)
}

func start(t *testing.T, configure func(*nativetest.Adapter), cfg native.Config) (*native.Conversation, *nativetest.Factory, *recorder) {
	t.Helper()
	factory := &nativetest.Factory{Configure: configure}
	rec := &recorder{}
	cfg.Factory = factory.New
	cfg.Emitter = rec
	cfg.Observer = rec
	c := native.NewConversation(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
	})
	c.Start(ctx)
	require.Eventually(t, func() bool { return c.Status() != st.ConversationStatusInitializing }, 5*time.Second, time.Millisecond)
	return c, factory, rec
}

func text(id, s string) jsonlwatcher.RichMessage {
	return jsonlwatcher.RichMessage{MessageID: id, Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: s}}}
}

func TestTurnBuildsMessages(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	assert.Equal(t, st.ConversationStatusChanging, c.Status())
	assert.False(t, c.Idle())

	sink := a.Sink()
	// A message of only tool calls doesn't start an agent message.
	sink.RichMessage(jsonlwatcher.RichMessage{MessageID: "m0", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "tool_use", ToolUseID: "t1"}}})
	require.Len(t, c.Messages(), 1)
	sink.RichMessage(text("m1", "Hel"))
	sink.RichMessage(text("m1", "Hello"))
	sink.RichMessage(text("m2", "World"))
	sink.TurnCompleted(native.TurnResult{StopReason: "end_turn"})

	msgs := c.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, st.ConversationRoleUser, msgs[0].Role)
	assert.Equal(t, "hello", msgs[0].Message)
	assert.Equal(t, st.ConversationRoleAgent, msgs[1].Role)
	assert.Equal(t, "Hello\n\nWorld", msgs[1].Message)
	assert.Equal(t, msgs, rec.emittedMessages())
	assert.Equal(t, st.ConversationStatusStable, c.Status())
	assert.Len(t, rec.rich, 4)
	assert.Equal(t, []native.Input{{Text: "hello"}}, a.Turns())

	// The next turn starts a new agent message.
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "again"}))
	sink.RichMessage(text("m3", "Second"))
	msgs = c.Messages()
	require.Len(t, msgs, 4)
	assert.Equal(t, "Second", msgs[3].Message)
	assert.Equal(t, 3, msgs[3].Id)
}

func TestStartTurnWhileBusy(t *testing.T) {
	c, _, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	err := c.StartTurn(context.Background(), native.Input{Text: "two"})
	assert.ErrorIs(t, err, st.ErrMessageValidationChanging)
	assert.Len(t, c.Messages(), 1)
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "  "}), st.ErrMessageValidationEmpty)
}

func TestStartTurnFailureRemovesMessage(t *testing.T) {
	boom := errors.New("boom")
	c, _, _ := start(t, func(a *nativetest.Adapter) { a.TurnErr = boom }, native.Config{})
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "one"}), boom)
	assert.Empty(t, c.Messages())
	assert.True(t, c.Idle())
}

func TestSteerAndInterrupt(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	a := factory.Last()
	assert.ErrorIs(t, c.Steer(context.Background(), native.Input{Text: "x"}), native.ErrNoActiveTurn)
	assert.ErrorIs(t, c.Interrupt(context.Background()), native.ErrNoActiveTurn)

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RichMessage(text("m1", "first part"))
	require.NoError(t, c.Steer(context.Background(), native.Input{Text: "also this"}))
	// The first message is still streaming after the steer; it stays in
	// the first agent message.
	a.Sink().RichMessage(text("m1", "first part, finished"))
	a.Sink().RichMessage(text("m2", "second part"))
	require.NoError(t, c.Interrupt(context.Background()))

	msgs := c.Messages()
	require.Len(t, msgs, 4)
	assert.Equal(t, "first part, finished", msgs[1].Message)
	assert.Equal(t, "also this", msgs[2].Message)
	assert.Equal(t, "second part", msgs[3].Message)
	assert.Equal(t, []native.Input{{Text: "also this"}}, a.Steers())
	assert.Equal(t, 1, a.Interrupts())
}

func TestSteerUnsupported(t *testing.T) {
	c, _, _ := start(t, func(a *nativetest.Adapter) { a.Caps = native.Capabilities{} }, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	assert.ErrorIs(t, c.Steer(context.Background(), native.Input{Text: "x"}), native.ErrUnsupported)
	assert.ErrorIs(t, c.Interrupt(context.Background()), native.ErrUnsupported)
}

func approval(id string) native.Request {
	return native.Request{
		ID: id, Kind: native.RequestKindApproval, Title: "Run ls",
		Options: []native.RequestOption{
			{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow},
			{ID: "deny", Label: "Deny", Kind: native.OptionKindDeny},
		},
	}
}

func TestRequestLifecycle(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))

	// Waiting on the user counts as stable, but no new turn can start.
	assert.Equal(t, st.ConversationStatusStable, c.Status())
	assert.False(t, c.Idle())
	pending := c.PendingRequests()
	require.Len(t, pending, 1)
	assert.Equal(t, native.ResponseLive, pending[0].Capability)

	_, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "nope"})
	assert.ErrorIs(t, err, native.ErrInvalidResponse)
	_, err = c.Respond(context.Background(), "missing", native.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	state, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	require.NoError(t, err)
	assert.Equal(t, native.RequestStatusResolved, state.Status)
	assert.Equal(t, st.ConversationStatusChanging, c.Status())
	assert.Equal(t, []nativetest.Response{{RequestID: "r1", Response: native.Response{OptionID: "allow"}}}, a.Responses())

	// A second answer is refused.
	_, err = c.Respond(context.Background(), "r1", native.Response{OptionID: "deny"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	// The agent withdraws a request; then a turn end expires the rest.
	a.Sink().RequestOpened(approval("r2"))
	a.Sink().RequestClosed("r2")
	r2, ok := c.Request("r2")
	require.True(t, ok)
	assert.Equal(t, native.RequestStatusWithdrawn, r2.Status)

	a.Sink().RequestOpened(approval("r3"))
	a.Sink().RequestOpened(native.Request{ID: "q1", Kind: native.RequestKindQuestion, Questions: []native.Question{{ID: "a", Question: "?"}}})
	a.Sink().TurnCompleted(native.TurnResult{})
	r3, _ := c.Request("r3")
	q1, _ := c.Request("q1")
	assert.Equal(t, native.RequestStatusExpired, r3.Status)
	assert.Equal(t, native.ResponseNotResumable, r3.Capability)
	assert.Equal(t, native.RequestStatusExpired, q1.Status)
	assert.Equal(t, native.ResponseMessage, q1.Capability)
	assert.Empty(t, c.PendingRequests())
	assert.NotEmpty(t, rec.requests)
}

func TestRespondFailureKeepsRequestPending(t *testing.T) {
	boom := errors.New("boom")
	c, factory, _ := start(t, func(a *nativetest.Adapter) { a.RespondErr = boom }, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(approval("r1"))
	_, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, boom)
	assert.Len(t, c.PendingRequests(), 1)
}

func TestQuestionValidation(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(native.Request{
		ID: "q", Kind: native.RequestKindQuestion,
		Questions: []native.Question{
			{ID: "color", Question: "Color?", Options: []native.QuestionOption{{Label: "Red"}, {Label: "Blue"}}},
			{ID: "tags", Question: "Tags?", MultiSelect: true, Options: []native.QuestionOption{{Label: "a"}, {Label: "b"}}},
			{ID: "name", Question: "Name?", FreeText: true},
		},
	})
	cases := []map[string][]string{
		{"color": {"Red"}, "tags": {"a"}},
		{"color": {"Green"}, "tags": {"a"}, "name": {"x"}},
		{"color": {"Red", "Blue"}, "tags": {"a"}, "name": {"x"}},
	}
	for _, answers := range cases {
		_, err := c.Respond(context.Background(), "q", native.Response{Answers: answers})
		assert.ErrorIs(t, err, native.ErrInvalidResponse, answers)
	}
	_, err := c.Respond(context.Background(), "q", native.Response{Answers: map[string][]string{
		"color": {"Red"}, "tags": {"a", "b"}, "name": {"anything"},
	}})
	assert.NoError(t, err)
}

func TestRestartReplacesAdapter(t *testing.T) {
	c, factory, rec := start(t, func(a *nativetest.Adapter) { a.Ref = "session-1" }, native.Config{ResumeRef: "old"})
	first := factory.Last()
	assert.Equal(t, "old", first.ResumeRef)
	assert.Equal(t, "session-1", c.SessionRef())
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().RequestOpened(approval("r1"))

	require.NoError(t, c.Restart(context.Background()))
	second := factory.Last()
	assert.NotSame(t, first, second)
	assert.True(t, first.Closed())
	assert.Empty(t, second.ResumeRef, "a restart starts a new session")
	assert.Empty(t, c.Messages())
	r1, _ := c.Request("r1")
	assert.Equal(t, native.ResponseNotResumable, r1.Capability)
	assert.True(t, c.Idle())

	// Events from the replaced adapter are ignored.
	first.Sink().RichMessage(text("old", "stale"))
	assert.Empty(t, c.Messages())
	for _, m := range rec.rich {
		assert.NotEqual(t, "old", m.MessageID)
	}

	// The replaced adapter's exit is not reported.
	select {
	case err := <-c.Exited():
		t.Fatalf("unexpected exit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestExitIsReported(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	boom := errors.New("crashed")
	factory.Last().Exit(boom)
	select {
	case err := <-c.Exited():
		assert.ErrorIs(t, err, boom)
	case <-time.After(5 * time.Second):
		t.Fatal("exit not reported")
	}
	assert.Equal(t, st.ConversationStatusInitializing, c.Status())
}

func TestInitialPrompt(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{InitialPrompt: "do it"})
	require.Eventually(t, func() bool { return len(factory.Last().Turns()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "do it", factory.Last().Turns()[0].Text)
	assert.Len(t, c.Messages(), 1)
}

func TestStartFailure(t *testing.T) {
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) { a.StartErr = errors.New("no login") }}
	rec := &recorder{}
	c := native.NewConversation(native.Config{Factory: factory.New, Emitter: rec, Observer: rec})
	c.Start(context.Background())
	select {
	case err := <-c.Exited():
		assert.ErrorContains(t, err, "no login")
	case <-time.After(5 * time.Second):
		t.Fatal("start failure not reported")
	}
	assert.NotEmpty(t, rec.errors)
}

func TestInputFromText(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	// The 8-byte PNG signature is enough for content sniffing.
	require.NoError(t, os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n0000"), 0o600))
	txt := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(txt, []byte("hello"), 0o600))

	in := native.InputFromText(`look @"` + png + `" and @"` + txt + `" and @"` + png + `" @"/does/not/exist.png"`)
	assert.Equal(t, []native.Image{{Path: png, MediaType: "image/png"}}, in.Images)
	assert.Contains(t, in.Text, png)
}
