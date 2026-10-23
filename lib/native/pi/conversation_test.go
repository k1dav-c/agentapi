package pi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

// convFixture runs adapters under native.Conversation against a fake Pi.
type convFixture struct {
	t    *testing.T
	ctx  context.Context
	pi   *fakePi
	conv *native.Conversation
}

func newConvFixture(t *testing.T) *convFixture {
	t.Helper()
	pi := newFakePi(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conv := native.NewConversation(native.Config{
		Factory: func(ref native.Resume) (native.Adapter, error) {
			return newAdapter(Config{Logger: logger, StartProcess: pi.start}, nil, ref.Ref), nil
		},
		Logger: logger,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = conv.Close()
		cancel()
	})
	conv.Start(ctx)
	return &convFixture{t: t, ctx: ctx, pi: pi, conv: conv}
}

func (f *convFixture) write(s string) {
	f.t.Helper()
	_, err := f.pi.stdoutW.Write([]byte(s + "\n"))
	require.NoError(f.t, err)
}

func (f *convFixture) reply(cmd map[string]any, data string) {
	f.t.Helper()
	b, err := json.Marshal(map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": true, "data": json.RawMessage(data)})
	require.NoError(f.t, err)
	f.write(string(b))
}

// startTurn sends a message and starts Pi's run for it.
func (f *convFixture) startTurn(text string) {
	f.t.Helper()
	turn := make(chan error, 1)
	go func() { turn <- f.conv.StartTurn(f.ctx, native.Input{Text: text}) }()
	prompt := f.pi.nextCmd()
	assert.Equal(f.t, text, prompt["message"])
	f.reply(prompt, `{"disposition":"started"}`)
	require.NoError(f.t, <-turn)
	f.write(`{"type":"agent_start"}`)
}

// TestConversation runs the adapter under native.Conversation: a turn with
// text and a tool approval, then Pi exits while the approval is pending.
func TestConversation(t *testing.T) {
	t.Parallel()
	f := newConvFixture(t)
	conv := f.conv
	f.reply(f.pi.nextCmd(), `{"sessionId":"agentapi-0123456789abcdef","model":{"input":["text"]}}`)
	require.Eventually(t, func() bool { return conv.Status() == conversation.StatusStable }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "agentapi-0123456789abcdef", conv.SessionRef())
	assert.False(t, conv.Capabilities().Images, "the model takes no images")

	f.startTurn("hi")
	f.write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":10}}`)
	f.write(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"rm -rf x"}}],"model":"m","stopReason":"toolUse","timestamp":10}}`)
	f.write(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"rm -rf x"}}`)
	f.write(`{"type":"extension_ui_request","id":"d1","method":"select","title":"Allow bash?","options":["Allow","Always allow","Deny"]}`)

	require.Eventually(t, func() bool { return len(conv.PendingRequests()) == 1 }, 5*time.Second, time.Millisecond)
	req := conv.PendingRequests()[0]
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, "c1", req.ToolUseID)
	assert.Equal(t, []native.OptionKind{native.OptionKindAllow, native.OptionKindAllowAlways, native.OptionKindDeny},
		[]native.OptionKind{req.Options[0].Kind, req.Options[1].Kind, req.Options[2].Kind})
	assert.Equal(t, conversation.StatusStable, conv.Status(), "waiting on the user")
	msgs := conv.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "hello", msgs[1].Message)

	f.pi.exit(errors.New("exit status 1"))
	select {
	case err := <-conv.Exited():
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the conversation didn't report the exit")
	}
	state, ok := conv.Request("d1")
	require.True(t, ok)
	assert.Equal(t, native.RequestStatusExpired, state.Status)
	assert.Equal(t, native.ResponseNotResumable, state.Capability)
	_, err := conv.Respond(f.ctx, "d1", native.Response{OptionID: "Allow"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)
}

// TestConversationExpiresDialogsWithTheTurn: a dialog the turn leaves open
// expires with the turn, as the requests of the other agents do, so a
// question can still be answered with a message.
func TestConversationExpiresDialogsWithTheTurn(t *testing.T) {
	t.Parallel()
	f := newConvFixture(t)
	f.reply(f.pi.nextCmd(), noState)
	require.Eventually(t, func() bool { return f.conv.Status() == conversation.StatusStable }, 5*time.Second, time.Millisecond)

	f.startTurn("hi")
	f.write(`{"type":"extension_ui_request","id":"q1","method":"input","title":"Your name?"}`)
	require.Eventually(t, func() bool { return len(f.conv.PendingRequests()) == 1 }, 5*time.Second, time.Millisecond)
	f.write(`{"type":"agent_settled"}`)
	require.Eventually(t, func() bool { return !f.conv.TurnActive() }, 5*time.Second, time.Millisecond)

	state, ok := f.conv.Request("q1")
	require.True(t, ok)
	assert.Equal(t, native.RequestStatusExpired, state.Status)
	assert.Equal(t, native.ResponseMessage, state.Capability)
	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "q1", "cancelled": true}, f.pi.nextCmd(),
		"the extension stops waiting too")
}

// settingsObserver records what the conversation reports of the settings
// and capabilities.
type settingsObserver struct {
	mu       sync.Mutex
	settings []native.Settings
	caps     []native.Capabilities
}

func (o *settingsObserver) Started(caps native.Capabilities) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.caps = append(o.caps, caps)
}

func (o *settingsObserver) Settings(s native.Settings) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.settings = append(o.settings, s)
}
func (*settingsObserver) LoginRequired(*native.LoginRequired)     {}
func (*settingsObserver) RichMessage(transcript.RichMessage)      {}
func (*settingsObserver) SessionEvents([]transcript.SessionEvent) {}
func (*settingsObserver) SessionName(string)                      {}
func (*settingsObserver) SessionRef(string)                       {}
func (*settingsObserver) RequestUpdated(native.RequestState)      {}
func (*settingsObserver) SettingsChosen(native.Settings)          {}
func (*settingsObserver) Usage(native.AgentUsage)                 {}
func (*settingsObserver) LimitStop(*native.LimitStop)             {}

func TestModelSwitchReportsNoMixedSettings(t *testing.T) {
	t.Parallel()
	pi := newFakePi(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	obs := &settingsObserver{}
	conv := native.NewConversation(native.Config{
		Factory: func(ref native.Resume) (native.Adapter, error) {
			return newAdapter(Config{Logger: logger, StartProcess: pi.start}, nil, ref.Ref), nil
		},
		Logger:   logger,
		Observer: obs,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = conv.Close()
		cancel()
	})
	conv.Start(ctx)
	f := &convFixture{t: t, ctx: ctx, pi: pi, conv: conv}
	const modelA = `{"provider":"p","id":"a","reasoning":true,"input":["text"],"contextWindow":1000,"thinkingLevelMap":{}}`
	const modelB = `{"provider":"p","id":"b","reasoning":true,"input":["text","image"],"contextWindow":2000,"thinkingLevelMap":{}}`
	f.reply(pi.nextCmd(), `{"sessionId":"s1","thinkingLevel":"low","model":`+modelA+`}`)
	require.Eventually(t, func() bool { return conv.Status() == conversation.StatusStable }, 5*time.Second, time.Millisecond)
	assert.False(t, conv.Capabilities().Images)

	applied := make(chan error, 1)
	go func() { applied <- conv.ApplySettings(ctx, native.Settings{Model: "p/b", Effort: "high"}) }()
	f.reply(pi.nextCmd(), `{"models":[`+modelA+`,`+modelB+`]}`)
	// As Pi does: the level changes, then the command is answered.
	setModel := pi.nextCmd()
	require.Equal(t, "set_model", setModel["type"])
	f.write(`{"type":"thinking_level_changed","level":"medium"}`)
	f.reply(setModel, modelB)
	setLevel := pi.nextCmd()
	require.Equal(t, "set_thinking_level", setLevel["type"])
	f.write(`{"type":"thinking_level_changed","level":"high"}`)
	f.reply(setLevel, `null`)
	f.reply(pi.nextCmd(), `{"sessionId":"s1","thinkingLevel":"high","model":`+modelB+`}`)
	require.NoError(t, <-applied)

	assert.Equal(t, native.Settings{Model: "p/b", Effort: "high"}, conv.Settings())
	assert.True(t, conv.Capabilities().Images, "the new model takes images")
	obs.mu.Lock()
	defer obs.mu.Unlock()
	for _, s := range obs.settings {
		assert.False(t, s.Model == "p/a" && s.Effort != "low", "reported the old model with a new level: %+v", s)
	}
	require.NotEmpty(t, obs.caps)
	assert.True(t, obs.caps[len(obs.caps)-1].Images)
}
