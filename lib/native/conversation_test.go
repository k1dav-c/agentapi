package native_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/transcript"
)

// recorded is what a recorder received.
type recorded struct {
	messages     []conversation.Message
	messageEmits int
	statuses     []conversation.Status
	errors       []string
	rich         []transcript.RichMessage
	requests     []native.RequestState
	refs         []string
	names        []string
	logins       []*native.LoginRequired
	settings     []native.Settings
	chosen       []native.Settings
	usage        []native.AgentUsage
	limitStops   []*native.LimitStop
}

// recorder records what a conversation emits and reports.
type recorder struct {
	mu sync.Mutex
	recorded
}

// get returns what was recorded so far.
func (r *recorder) get() recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recorded
}

func (r *recorder) EmitMessages(m []conversation.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = m
	r.messageEmits++
}

func (r *recorder) EmitStatus(s conversation.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, s)
}

func (r *recorder) EmitScreen(string) {}

func (r *recorder) EmitError(m string, _ conversation.ErrorLevel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, m)
}

func (r *recorder) Started(native.Capabilities) {}

func (r *recorder) LoginRequired(l *native.LoginRequired) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logins = append(r.logins, l)
}

func (r *recorder) RichMessage(m transcript.RichMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rich = append(r.rich, m)
}

func (r *recorder) SessionEvents([]transcript.SessionEvent) {}

func (r *recorder) SessionName(n string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, n)
}

func (r *recorder) SessionRef(ref string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refs = append(r.refs, ref)
}

func (r *recorder) RequestUpdated(s native.RequestState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, s)
}

func (r *recorder) Settings(s native.Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = append(r.settings, s)
}

func (r *recorder) SettingsChosen(s native.Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chosen = append(r.chosen, s)
}

func (r *recorder) Usage(u native.AgentUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = append(r.usage, u)
}

func (r *recorder) LimitStop(ls *native.LimitStop) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limitStops = append(r.limitStops, ls)
}

// logRecorder is a slog.Handler that keeps the messages logged.
type logRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *logRecorder) WithGroup(string) slog.Handler            { return h }

func (h *logRecorder) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, r.Message)
	return nil
}

func (h *logRecorder) logged(message string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Contains(h.messages, message)
}

// run creates a conversation with factory that reports to a recorder, and
// starts it.
func run(t *testing.T, factory *nativetest.Factory, cfg native.Config) (*native.Conversation, *recorder) {
	t.Helper()
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
	return c, rec
}

// start runs a conversation and waits until its agent started.
func start(t *testing.T, configure func(*nativetest.Adapter), cfg native.Config) (*native.Conversation, *nativetest.Factory, *recorder) {
	t.Helper()
	factory := &nativetest.Factory{Configure: configure}
	c, rec := run(t, factory, cfg)
	require.Eventually(t, func() bool { return c.Status() != conversation.StatusInitializing }, 5*time.Second, time.Millisecond)
	return c, factory, rec
}

// inStart reports whether the newest adapter of factory with resumeRef is
// in Start.
func inStart(factory *nativetest.Factory, resumeRef string) func() bool {
	return func() bool {
		a := factory.Last()
		return a != nil && a.ResumeRef == resumeRef && a.Sink() != nil
	}
}

func text(id, s string) transcript.RichMessage {
	return transcript.RichMessage{MessageID: id, Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: s}}}
}

func TestTurnBuildsMessages(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	assert.Equal(t, conversation.StatusChanging, c.Status())
	assert.False(t, c.Idle())

	sink := a.Sink()
	// A message of only tool calls doesn't start an agent message.
	sink.RichMessage(transcript.RichMessage{MessageID: "m0", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "tool_use", ToolUseID: "t1"}}})
	require.Len(t, c.Messages(), 1)
	sink.RichMessage(text("m1", "Hel"))
	sink.RichMessage(text("m1", "Hello"))
	sink.RichMessage(text("m2", "World"))
	sink.TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})

	msgs := c.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, conversation.RoleUser, msgs[0].Role)
	assert.Equal(t, "hello", msgs[0].Message)
	assert.Equal(t, conversation.RoleAgent, msgs[1].Role)
	assert.Equal(t, "Hello\n\nWorld", msgs[1].Message)
	assert.Equal(t, msgs, rec.get().messages)
	assert.Equal(t, conversation.StatusStable, c.Status())
	assert.Len(t, rec.get().rich, 4)
	assert.Equal(t, []native.Input{{Text: "hello"}}, a.Turns())

	// The next turn starts a new agent message.
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "again"}))
	sink.RichMessage(text("m3", "Second"))
	msgs = c.Messages()
	require.Len(t, msgs, 4)
	assert.Equal(t, "Second", msgs[3].Message)
	assert.Equal(t, 3, msgs[3].Id)
}

func TestUnchangedTextEmitsNoMessages(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	sink := factory.Last().Sink()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	sink.RichMessage(text("m1", "Hello"))
	emits := rec.get().messageEmits

	// A snapshot that only adds a tool call leaves the agent message as it
	// was.
	sink.RichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{
		{Type: "text", Text: "Hello"}, {Type: "tool_use", ToolUseID: "t1"},
	}})
	assert.Equal(t, emits, rec.get().messageEmits)
	assert.Len(t, rec.get().rich, 2, "observers get every snapshot")
}

func TestStartTurnWhileBusy(t *testing.T) {
	c, _, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	err := c.StartTurn(context.Background(), native.Input{Text: "two"})
	assert.ErrorIs(t, err, conversation.ErrMessageValidationChanging)
	assert.Len(t, c.Messages(), 1)
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "  "}), conversation.ErrMessageValidationEmpty)
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

func TestFailedSteerLeavesTheReplyWhole(t *testing.T) {
	boom := errors.New("boom")
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.SteerErr = boom
		// The agent goes on talking while the steer is on its way.
		a.OnSteer = func(context.Context) { a.Sink().RichMessage(text("m2", "more")) }
	}, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RichMessage(text("m1", "first"))

	assert.ErrorIs(t, c.Steer(context.Background(), native.Input{Text: "also this"}), boom)
	a.Sink().RichMessage(text("m3", "last"))
	msgs := c.Messages()
	require.Len(t, msgs, 2, "the failed steer leaves no message: %+v", msgs)
	assert.Equal(t, "first\n\nmore\n\nlast", msgs[1].Message)
}

func TestSteerUnsupported(t *testing.T) {
	c, _, _ := start(t, func(a *nativetest.Adapter) { a.Caps = native.Capabilities{} }, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	assert.ErrorIs(t, c.Steer(context.Background(), native.Input{Text: "x"}), native.ErrUnsupported)
	assert.ErrorIs(t, c.Interrupt(context.Background()), native.ErrUnsupported)
}

func TestImagesNeedSupport(t *testing.T) {
	c, factory, _ := start(t, func(a *nativetest.Adapter) { a.Caps.Images = false }, native.Config{})
	a := factory.Last()
	in := native.Input{Text: `look @"/tmp/shot.png"`, Images: []native.Image{{Path: "/tmp/shot.png", MediaType: "image/png"}}}
	require.NoError(t, c.StartTurn(context.Background(), in))
	require.NoError(t, c.Steer(context.Background(), in))
	// The text still mentions the file.
	assert.Equal(t, []native.Input{{Text: in.Text}}, a.Turns())
	assert.Equal(t, []native.Input{{Text: in.Text}}, a.Steers())
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

func question(id string) native.Request {
	return native.Request{ID: id, Kind: native.RequestKindQuestion, Questions: []native.Question{{ID: "a", Question: "?"}}}
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

// The sink copies a rich message: the observer may change the copy it got
// (the server replaces image data with URLs), and the adapter its own.
func TestRichMessageIsCopied(t *testing.T) {
	_, factory, rec := start(t, nil, native.Config{})
	isError := false
	msg := transcript.RichMessage{MessageID: "u1", Role: "user", Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", IsError: &isError, Images: []transcript.RichImage{{MediaType: "image/png", Data: []byte("png")}}},
	}}
	factory.Last().Sink().RichMessage(msg)
	got := rec.get().rich[0]
	got.Content[0].Images[0] = transcript.RichImage{MediaType: "image/png", URL: "/uploads/x/image.png"}
	assert.Equal(t, []byte("png"), msg.Content[0].Images[0].Data, "the observer's change doesn't reach the adapter")

	msg.Content[0].Status = "completed"
	isError = true
	kept := rec.get().rich[0]
	assert.Empty(t, kept.Content[0].Status, "the adapter's change doesn't reach the observer")
	assert.False(t, *kept.Content[0].IsError)
}

// PrepareRichMessage changes a rich message before the conversation takes
// it, without the conversation's lock: storing images there holds up
// nothing else.
func TestPrepareRichMessageRunsUnlocked(t *testing.T) {
	var conv atomic.Pointer[native.Conversation]
	unlocked := make(chan bool, 1)
	c, factory, rec := start(t, nil, native.Config{PrepareRichMessage: func(msg *transcript.RichMessage) {
		unlocked <- nativetest.Returns(func() { conv.Load().Status() })
		msg.Content[0].Images = []transcript.RichImage{{MediaType: "image/png", URL: "/uploads/x/image.png"}}
	}})
	conv.Store(c)
	factory.Last().Sink().RichMessage(transcript.RichMessage{MessageID: "u1", Role: "user", Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "t1", Images: []transcript.RichImage{{MediaType: "image/png", Data: []byte("png")}}},
	}})
	assert.True(t, <-unlocked, "the conversation's lock is held")
	got := rec.get().rich
	require.Len(t, got, 1)
	assert.Equal(t, "/uploads/x/image.png", got[0].Content[0].Images[0].URL)
	assert.Nil(t, got[0].Content[0].Images[0].Data)
}
