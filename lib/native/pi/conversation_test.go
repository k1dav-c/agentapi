package pi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// TestConversation runs the adapter under native.Conversation: a turn with
// text and a tool approval, then Pi exits while the approval is pending.
func TestConversation(t *testing.T) {
	t.Parallel()
	pi := newFakePi(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conv := native.NewConversation(native.Config{
		Factory: func(ref string) (native.Adapter, error) {
			return newAdapter(Config{Logger: logger}, ref, pi.spawn)
		},
		Logger: logger,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = conv.Close()
		cancel()
	})
	conv.Start(ctx)

	write := func(s string) {
		_, err := pi.stdoutW.Write([]byte(s + "\n"))
		require.NoError(t, err)
	}
	reply := func(cmd map[string]any, data string) {
		b, _ := json.Marshal(map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": true, "data": json.RawMessage(data)})
		write(string(b))
	}
	reply(pi.nextCmd(), `{"sessionId":"agentapi-0123456789abcdef","model":{"input":["text"]}}`)
	require.Eventually(t, func() bool { return conv.Status() == st.ConversationStatusStable }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "agentapi-0123456789abcdef", conv.SessionRef())
	assert.False(t, conv.Capabilities().Images, "the model takes no images")

	turn := make(chan error, 1)
	go func() { turn <- conv.StartTurn(ctx, native.Input{Text: "hi"}) }()
	prompt := pi.nextCmd()
	assert.Equal(t, "hi", prompt["message"])
	reply(prompt, `{"disposition":"started"}`)
	require.NoError(t, <-turn)
	write(`{"type":"agent_start"}`)
	write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":10}}`)
	write(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello"},{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"rm -rf x"}}],"model":"m","stopReason":"toolUse","timestamp":10}}`)
	write(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"rm -rf x"}}`)
	write(`{"type":"extension_ui_request","id":"d1","method":"select","title":"Allow bash?","options":["Allow","Always allow","Deny"]}`)

	require.Eventually(t, func() bool { return len(conv.PendingRequests()) == 1 }, 5*time.Second, time.Millisecond)
	req := conv.PendingRequests()[0]
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, "c1", req.ToolUseID)
	assert.Equal(t, []native.OptionKind{native.OptionKindAllow, native.OptionKindAllowAlways, native.OptionKindDeny},
		[]native.OptionKind{req.Options[0].Kind, req.Options[1].Kind, req.Options[2].Kind})
	assert.Equal(t, st.ConversationStatusStable, conv.Status(), "waiting on the user")
	msgs := conv.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, "hello", msgs[1].Message)

	pi.exit(errors.New("exit status 1"))
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
	_, err := conv.Respond(ctx, "d1", native.Response{OptionID: "Allow"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)
}
