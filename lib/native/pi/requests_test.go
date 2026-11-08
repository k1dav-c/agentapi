package pi

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/native"
)

func TestOptionKind(t *testing.T) {
	t.Parallel()
	for label, want := range map[string]agentstate.OptionKind{
		"Allow":                   agentstate.OptionKindAllow,
		"Yes, proceed anyway":     agentstate.OptionKindAllow,
		"OK":                      agentstate.OptionKindAllow,
		"Always allow":            agentstate.OptionKindAllowAlways,
		"Allow always":            agentstate.OptionKindAllowAlways,
		"Yes, always":             agentstate.OptionKindAllowAlways,
		"Always":                  agentstate.OptionKindAllowAlways,
		"Block":                   agentstate.OptionKindDeny,
		"No":                      agentstate.OptionKindDeny,
		"No, let me commit first": agentstate.OptionKindDeny,
		"Always deny":             agentstate.OptionKindDeny,
		"Deny always":             agentstate.OptionKindDeny,
		"Block always":            agentstate.OptionKindDeny,
		"Disallow":                agentstate.OptionKindDeny,
		"Cancel":                  agentstate.OptionKindCancel,
		"Stop the run":            agentstate.OptionKindCancel,
		"Normal mode":             agentstate.OptionKindOther,
		"Notify me":               agentstate.OptionKindOther,
		"Option A":                agentstate.OptionKindOther,
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

	h.a.writer.Attach(failingStdin{})
	err := h.a.Respond(h.ctx, "d1", agentstate.Response{OptionID: optionNo})
	require.Error(t, err)
	assert.NotErrorIs(t, err, native.ErrRequestNotFound)

	h.a.writer.Attach(h.pi.Stdin())
	require.NoError(t, h.a.Respond(h.ctx, "d1", agentstate.Response{OptionID: optionNo}), "the dialog is still open")
	assert.Equal(t, false, h.pi.nextCmd()["confirmed"])
	assert.ErrorIs(t, h.a.Respond(h.ctx, "d1", agentstate.Response{OptionID: optionNo}), native.ErrRequestNotFound)
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

	stdin := &heldStdin{WriteCloser: h.pi.Stdin(), writing: make(chan struct{}), release: make(chan struct{})}
	h.a.writer.Attach(stdin)
	h.call("r", func() error {
		return h.a.Respond(h.ctx, "d1", agentstate.Response{Answers: map[string][]string{valueQuestion: {"typed"}}})
	})
	<-stdin.writing
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	close(stdin.release)
	require.NoError(t, h.result("r"))

	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "value": "typed"}, h.pi.nextCmd())
	assert.Never(t, func() bool { return len(h.pi.cmds) > 0 }, 200*time.Millisecond, 10*time.Millisecond, "no cancel follows the answer")
}

// An answer whose write didn't finish may yet reach Pi: the dialog takes
// no other answer, and the end of the turn doesn't cancel it.
func TestUnconfirmedAnswerIsNotSentTwice(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p", "ask me something")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	h.write(`{"type":"extension_ui_request","id":"d1","method":"input","title":"Enter a value"}`)
	h.settle()

	stdin := &heldStdin{WriteCloser: h.pi.Stdin(), writing: make(chan struct{}), release: make(chan struct{})}
	h.a.writer.Attach(stdin)
	ctx, cancel := context.WithTimeout(h.ctx, 20*time.Millisecond)
	defer cancel()
	answer := func(value string) agentstate.Response {
		return agentstate.Response{Answers: map[string][]string{valueQuestion: {value}}}
	}
	require.ErrorIs(t, h.a.Respond(ctx, "d1", answer("typed")), native.ErrUnconfirmed)
	require.ErrorIs(t, h.a.Respond(h.ctx, "d1", answer("other")), native.ErrRequestNotFound,
		"a second answer could contradict the first")
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	close(stdin.release)

	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "value": "typed"}, h.pi.nextCmd())
	assert.Never(t, func() bool { return len(h.pi.cmds) > 0 }, 200*time.Millisecond, 10*time.Millisecond, "no cancel follows the answer")
	h.a.mu.Lock()
	assert.Empty(t, h.a.dialogs, "the turn's end forgets the dialog")
	h.a.mu.Unlock()
}

func TestDialogTimersStop(t *testing.T) {
	t.Parallel()
	dialog := `{"type":"extension_ui_request","id":"d1","method":"select","title":"Pick","options":["a","b"],"timeout":30000}`

	t.Run("answered", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		h.write(dialog)
		h.settle()
		require.NoError(t, h.a.Respond(h.ctx, "d1", agentstate.Response{Answers: map[string][]string{choiceQuestion: {"b"}}}))
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
		<-h.a.host.LoopDone()
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

	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.State().Results)
	assert.Empty(t, h.sink.State().Closed)
	assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "cancelled": true}, h.pi.nextCmd())
	assert.ErrorIs(t, h.a.Respond(h.ctx, "d1", agentstate.Response{OptionID: "Allow"}), native.ErrRequestNotFound)
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
	assert.Equal(t, []string{"d1"}, h.sink.State().Closed, "the turn goes on until the command returns")
	assert.Empty(t, h.sink.State().Results)

	h.answer(cmd, `{"disposition":"handled"}`)
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.State().Results)
}

// TestUnconfirmedInterrupt: an abort whose write didn't finish may yet
// reach Pi. Its late response still cancels the command's dialog, and a
// Pi that never answers it has the turn ended at the deadline.
func TestUnconfirmedInterrupt(t *testing.T) {
	t.Parallel()
	interrupt := func(t *testing.T) (*harness, map[string]any, *heldStdin) {
		h := startedHarness(t)
		cmd := h.prompt("p", "/rpc-input")
		h.write(`{"type":"extension_ui_request","id":"d1","method":"input","title":"Enter a value","placeholder":"type something..."}`)
		require.NoError(t, h.result("p"))
		h.settle()
		stdin := &heldStdin{WriteCloser: h.pi.Stdin(), writing: make(chan struct{}), release: make(chan struct{})}
		h.a.writer.Attach(stdin)
		ctx, cancel := context.WithTimeout(h.ctx, 20*time.Millisecond)
		defer cancel()
		require.NoError(t, h.a.Interrupt(ctx), "the adapter ends the turn either way")
		return h, cmd, stdin
	}

	t.Run("late response", func(t *testing.T) {
		t.Parallel()
		h, cmd, stdin := interrupt(t)
		close(stdin.release)
		abort := h.pi.nextCmd()
		assert.Equal(t, "abort", abort["type"])
		h.answer(abort, "")
		h.settle()
		assert.Equal(t, map[string]any{"type": "extension_ui_response", "id": "d1", "cancelled": true}, h.pi.nextCmd())
		h.answer(cmd, `{"disposition":"handled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.State().Results)
	})

	t.Run("no response", func(t *testing.T) {
		t.Parallel()
		h, _, stdin := interrupt(t)
		defer close(stdin.release)
		h.advance(h.a.interruptTimeout)
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.State().Results)
	})
}
