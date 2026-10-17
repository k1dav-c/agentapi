package pi

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

func TestOptionKind(t *testing.T) {
	t.Parallel()
	for label, want := range map[string]native.OptionKind{
		"Allow":                   native.OptionKindAllow,
		"Yes, proceed anyway":     native.OptionKindAllow,
		"OK":                      native.OptionKindAllow,
		"Always allow":            native.OptionKindAllowAlways,
		"Allow always":            native.OptionKindAllowAlways,
		"Yes, always":             native.OptionKindAllowAlways,
		"Always":                  native.OptionKindAllowAlways,
		"Block":                   native.OptionKindDeny,
		"No":                      native.OptionKindDeny,
		"No, let me commit first": native.OptionKindDeny,
		"Always deny":             native.OptionKindDeny,
		"Deny always":             native.OptionKindDeny,
		"Block always":            native.OptionKindDeny,
		"Disallow":                native.OptionKindDeny,
		"Cancel":                  native.OptionKindCancel,
		"Stop the run":            native.OptionKindCancel,
		"Normal mode":             native.OptionKindOther,
		"Notify me":               native.OptionKindOther,
		"Option A":                native.OptionKindOther,
	} {
		assert.Equal(t, want, optionKind(label), label)
	}
}

// failingStdin is a stdin whose writes fail.
type failingStdin struct{}

func (failingStdin) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (failingStdin) Close() error              { return nil }

func TestRespondAfterFailedWrite(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	h.write(`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Clear session?","message":"All messages will be lost."}`)
	h.settle()

	h.a.writeMu.Lock()
	stdin := h.a.stdin
	h.a.stdin = failingStdin{}
	h.a.writeMu.Unlock()
	err := h.a.Respond(h.ctx, "d1", native.Response{OptionID: optionNo})
	require.Error(t, err)
	assert.NotErrorIs(t, err, native.ErrRequestNotFound)

	h.a.writeMu.Lock()
	h.a.stdin = stdin
	h.a.writeMu.Unlock()
	require.NoError(t, h.a.Respond(h.ctx, "d1", native.Response{OptionID: optionNo}), "the dialog is still open")
	assert.Equal(t, false, h.pi.nextCmd()["confirmed"])
	assert.ErrorIs(t, h.a.Respond(h.ctx, "d1", native.Response{OptionID: optionNo}), native.ErrRequestNotFound)
}

// heldStdin holds writes until release is closed.
type heldStdin struct {
	io.WriteCloser
	writing chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *heldStdin) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.writing) })
	<-s.release
	return s.WriteCloser.Write(p)
}

// TestTurnEndWhileAnswering: a dialog whose answer is on its way when the
// turn ends gets just that answer, not a cancel as well.
func TestTurnEndWhileAnswering(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p", "ask me something")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	h.write(`{"type":"extension_ui_request","id":"d1","method":"input","title":"Enter a value"}`)
	h.settle()

	h.a.writeMu.Lock()
	stdin := &heldStdin{WriteCloser: h.a.stdin, writing: make(chan struct{}), release: make(chan struct{})}
	h.a.stdin = stdin
	h.a.writeMu.Unlock()
	h.call("r", func() error {
		return h.a.Respond(h.ctx, "d1", native.Response{Answers: map[string][]string{valueQuestion: {"typed"}}})
	})
	<-stdin.writing
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	close(stdin.release)
	require.NoError(t, h.result("r"))

	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "value": "typed"}, h.pi.nextCmd())
	assert.Never(t, func() bool { return len(h.pi.cmds) > 0 }, 200*time.Millisecond, 10*time.Millisecond, "no cancel follows the answer")
}

func TestDialogTimersStop(t *testing.T) {
	t.Parallel()
	dialog := `{"type":"extension_ui_request","id":"d1","method":"select","title":"Pick","options":["a","b"],"timeout":30000}`

	t.Run("answered", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		h.write(dialog)
		h.settle()
		require.NoError(t, h.a.Respond(h.ctx, "d1", native.Response{Answers: map[string][]string{choiceQuestion: {"b"}}}))
		assert.Equal(t, "b", h.pi.nextCmd()["value"])
		_, timer := h.clock.Peek()
		assert.False(t, timer)
	})

	t.Run("Pi exited", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		h.write(dialog)
		h.settle()
		h.pi.exit(nil)
		<-h.a.loopDone
		_, timer := h.clock.Peek()
		assert.False(t, timer)
	})
}

// TestTurnEndLeavesDialogsToTheConversation: when a turn ends with a dialog
// open, Pi stopped waiting for it (an abort resolves it in Pi). The
// conversation expires it with the turn, as it does for the other agents;
// the extension still gets a cancel.
func TestTurnEndLeavesDialogsToTheConversation(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p", "remove junk")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	h.write(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"rm -rf junk"}}`)
	h.write(`{"type":"extension_ui_request","id":"d1","method":"select","title":"Dangerous command: rm -rf junk","options":["Allow","Block"]}`)
	h.settle()
	require.NoError(t, h.a.Interrupt(h.ctx))
	abort := h.pi.nextCmd()
	h.write(`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","result":{"content":[{"type":"text","text":"Command aborted"}]},"isError":true}`)
	h.write(`{"type":"agent_settled"}`)
	h.answer(abort, "")
	h.settle()

	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.turnResults())
	assert.Empty(t, h.sink.closed)
	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "cancelled": true}, h.pi.nextCmd())
	assert.ErrorIs(t, h.a.Respond(h.ctx, "d1", native.Response{OptionID: "Allow"}), native.ErrRequestNotFound)
}

// TestInterruptCancelsCommandDialogs: abort doesn't reach an extension
// command waiting on a dialog, so the adapter cancels the dialog, which
// ends the command.
func TestInterruptCancelsCommandDialogs(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p", "/rpc-input")
	h.write(`{"type":"extension_ui_request","id":"d1","method":"input","title":"Enter a value","placeholder":"type something..."}`)
	require.NoError(t, h.result("p"))
	h.settle()
	require.NoError(t, h.a.Interrupt(h.ctx))
	abort := h.pi.nextCmd()
	assert.Equal(t, "abort", abort["type"])
	h.answer(abort, "")
	h.settle()
	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "cancelled": true}, h.pi.nextCmd())
	assert.Equal(t, []string{"d1"}, h.sink.closed, "the turn goes on until the command returns")
	assert.Empty(t, h.sink.turnResults())

	h.answer(cmd, `{"disposition":"handled"}`)
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.turnResults())
}
