package pi

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

// startedHarness returns a harness whose adapter has started.
func startedHarness(t *testing.T, opts ...func(*harness)) *harness {
	t.Helper()
	h := newHarness(t, Config{}, "", opts...)
	h.answerState(noState)
	h.started()
	return h
}

// TestExtensionCommandTurns follows prompts that run extension commands,
// which Pi answers with "handled" when the command returns.
func TestExtensionCommandTurns(t *testing.T) {
	t.Parallel()

	t.Run("without a run", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "/cmd")
		h.answer(cmd, `{"disposition":"handled"}`)
		require.NoError(t, h.result("p"))
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonHandled}}, h.sink.State().Results,
			"no model turn, so it doesn't show the login works")
	})

	t.Run("waits for its run", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "/cmd")
		// sendUserMessage, then ctx.waitForIdle(): Pi answers the prompt
		// after agent_settled.
		h.write(`{"type":"agent_start"}`)
		require.NoError(t, h.result("p"), "the run reports the prompt started")
		h.write(`{"type":"agent_settled"}`)
		h.answer(cmd, `{"disposition":"handled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
		assert.Equal(t, 1, h.sink.State().Started, "no second, empty turn")
	})

	t.Run("returns while its run goes on", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "/cmd")
		h.write(`{"type":"agent_start"}`)
		h.answer(cmd, `{"disposition":"handled"}`)
		h.settle()
		assert.Empty(t, h.sink.State().Results)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	})

	t.Run("asks after its run", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "/cmd")
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"agent_settled"}`)
		h.write(`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Commit the changes?","message":"2 files changed"}`)
		require.NoError(t, h.result("p"))
		h.settle()
		require.NoError(t, h.a.Respond(h.ctx, "d1", native.Response{OptionID: optionYes}))
		assert.Equal(t, true, h.pi.nextCmd()["confirmed"])
		h.answer(cmd, `{"disposition":"handled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}, {StopReason: native.StopReasonHandled}}, h.sink.State().Results,
			"the dialog's turn ends with the command")
	})

	t.Run("answered after the next turn began", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p1", "/cmd")
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"agent_settled"}`)
		require.NoError(t, h.result("p1"))
		next := h.prompt("p2", "next")
		h.answer(next, `{"disposition":"started"}`)
		h.answer(cmd, `{"disposition":"handled"}`)
		h.settle()
		assert.Len(t, h.sink.State().Results, 1, "the late answer doesn't end the next turn")
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Len(t, h.sink.State().Results, 2)
	})
}

// TestPromptRefusedAfterAcceptance covers prompts that StartTurn already
// reported started when Pi refuses them.
func TestPromptRefusedAfterAcceptance(t *testing.T) {
	t.Parallel()

	t.Run("while a run goes on", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		// A run an extension started before Pi got to the prompt.
		h.write(`{"type":"agent_start"}`)
		require.NoError(t, h.result("p"))
		h.refuse(cmd, "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
		h.settle()
		assert.Equal(t, []string{"error: Pi refused the message: Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."}, h.sink.State().Errors)
		assert.Empty(t, h.sink.State().Results, "the run goes on")
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	})

	t.Run("after its dialog", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		// An input handler asks first.
		h.write(`{"type":"extension_ui_request","id":"d1","method":"confirm","title":"Send it?","message":"hi"}`)
		require.NoError(t, h.result("p"))
		require.NoError(t, h.a.Respond(h.ctx, "d1", native.Response{OptionID: optionYes}))
		assert.Equal(t, "extension_ui_response", h.pi.nextCmd()["type"])
		h.refuse(cmd, "No API key found for anthropic.\n\nUse /login to log into a provider via OAuth or API key.")
		h.settle()
		results := h.sink.State().Results
		require.Len(t, results, 1)
		assert.Equal(t, native.StopReasonError, results[0].StopReason)
		assert.Contains(t, results[0].Error, "No API key found")
		assert.Equal(t, []string{"turn_started", "request_opened:d1", "login_required", "turn_completed:error"}, h.sink.State().Log)
		assert.Empty(t, h.sink.State().Errors)
	})
}

func TestInterruptTimeout(t *testing.T) {
	t.Parallel()
	h := startedHarness(t, func(h *harness) { h.a.interruptTimeout = time.Second })
	runTurn := func(id string) {
		cmd := h.prompt(id, "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		require.NoError(t, h.result(id))
		h.settle()
	}

	// Pi ends the first turn when it gets the abort.
	runTurn("p1")
	require.NoError(t, h.a.Interrupt(h.ctx))
	abort := h.pi.nextCmd()
	assert.Equal(t, "abort", abort["type"])
	h.write(`{"type":"agent_settled"}`)
	h.answer(abort, "")
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}}, h.sink.State().Results)

	// The second turn outlives the first one's timer, and Pi never
	// confirms its abort.
	runTurn("p2")
	h.advance(time.Second)
	assert.Len(t, h.sink.State().Results, 1, "the first interrupt's timer doesn't end the next turn")
	require.NoError(t, h.a.Interrupt(h.ctx))
	assert.Equal(t, "abort", h.pi.nextCmd()["type"])
	h.advance(time.Second - time.Millisecond)
	assert.Len(t, h.sink.State().Results, 1)
	h.advance(time.Millisecond)
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonInterrupted}, {StopReason: native.StopReasonInterrupted}}, h.sink.State().Results,
		"the adapter ends the turn")

	// A late end doesn't end it again.
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Len(t, h.sink.State().Results, 2)
}

func TestInterruptWriteFails(t *testing.T) {
	t.Parallel()
	h := startedHarness(t, func(h *harness) {
		h.pi.exitOnStdinClose = false
		h.a.stdinGrace = 10 * time.Millisecond
	})
	cmd := h.prompt("p", "hi")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	require.NoError(t, h.result("p"))
	h.settle()

	// Pi stops reading its stdin.
	require.NoError(t, h.pi.stdinR.Close())
	assert.ErrorIs(t, h.a.Interrupt(h.ctx), io.ErrClosedPipe)
	h.a.mu.Lock()
	assert.Empty(t, h.a.pending, "nothing waits for the abort's response")
	h.a.mu.Unlock()
	_, timer := h.clock.Peek()
	assert.False(t, timer, "no interrupt timeout runs")

	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results, "the turn wasn't interrupted")
}

// A record whose fields changed type is read all the same: a lost record
// loses what it shows. Each change, and each record type the adapter
// doesn't know, is logged once.
func TestDriftedRecordsAreRead(t *testing.T) {
	t.Parallel()
	logger, log := nativetest.NewLog()
	h := newHarness(t, Config{Logger: logger}, "")
	h.answerState(noState)
	h.started()
	cmd := h.prompt("p", "hi")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	h.write(`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"seq 2"}}`)
	for range 2 {
		h.write(`{"type":"agent_paused","reason":1}`)
	}
	h.write(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"1"}],"isError":"no"}}`)
	h.settle()
	h.advance(snapshotInterval)
	h.write(`{"type":"tool_execution_update","toolCallId":"c1","toolName":"bash","partialResult":{"content":[{"type":"text","text":"1\n2"}],"isError":"no"}}`)
	h.write(`{"type":"agent_settled"}`)
	require.NoError(t, h.result("p"))
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	results := h.sink.Snapshots("result-c1")
	require.Len(t, results, 2, "the output shows")
	assert.Equal(t, "1\n2", results[1].Content[0].Text)

	assert.Len(t, log.Matching("type=tool_execution_update", "field=partialResult.isError"), 1, "%v", log.Warnings())
	assert.Len(t, log.Matching("type=agent_paused"), 1)
	assert.Empty(t, log.Matching("type=agent_start"))
}

func TestRecords(t *testing.T) {
	t.Parallel()

	t.Run("retries that fail", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		failed := `{"role":"assistant","content":[],"model":"m","stopReason":"error","errorMessage":"529 overloaded","timestamp":1}`
		h.write(`{"type":"message_end","message":` + failed + `}`)
		h.write(`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"529 overloaded"}`)
		h.write(`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"529 overloaded after 3 attempts"}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonError, Error: "529 overloaded after 3 attempts"}}, h.sink.State().Results)
	})

	t.Run("a retry that works", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"message_end","message":{"role":"assistant","content":[],"model":"m","stopReason":"error","errorMessage":"529 overloaded","timestamp":1}}`)
		h.write(`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"529 overloaded"}`)
		h.write(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"model":"m","stopReason":"stop","timestamp":2}}`)
		h.write(`{"type":"auto_retry_end","success":true,"attempt":1}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	})

	t.Run("compaction and extension errors", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		h.write(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":false,"errorMessage":"Compaction failed: context too small"}`)
		h.write(`{"type":"compaction_end","reason":"manual","aborted":true,"willRetry":false,"errorMessage":"Compaction cancelled"}`)
		h.write(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":false}`)
		h.write(`{"type":"extension_error","extensionPath":"/home/me/.pi/agent/extensions/guard.ts","event":"tool_call","error":"boom"}`)
		// A field of another type is logged as drift; without an error
		// there is nothing to report.
		h.write(`{"type":"extension_error","extensionPath":7}`)
		h.settle()
		assert.Equal(t, []string{
			"warning: Compaction failed: context too small",
			"warning: Pi extension /home/me/.pi/agent/extensions/guard.ts failed on tool_call: boom",
		}, h.sink.State().Errors)
	})
}

// A prompt Pi got but neither answered nor started may run all the same:
// the error says so, whether the call was cut short or Pi exited first.
func TestPromptWithoutAnswerIsUnconfirmed(t *testing.T) {
	t.Parallel()

	t.Run("cut short", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		ctx, cancel := context.WithCancel(h.ctx)
		h.call("p", func() error { return h.a.StartTurn(ctx, native.Input{Text: "hi"}) })
		assert.Equal(t, "prompt", h.pi.nextCmd()["type"])
		cancel()
		err := h.result("p")
		require.ErrorIs(t, err, native.ErrUnconfirmed)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("refused late", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		ctx, cancel := context.WithCancel(h.ctx)
		h.call("p", func() error { return h.a.StartTurn(ctx, native.Input{Text: "hi"}) })
		cmd := h.pi.nextCmd()
		cancel()
		require.ErrorIs(t, h.result("p"), native.ErrUnconfirmed)
		// The conversation keeps the turn of a message that may have gone
		// out; Pi's refusal ends it.
		h.refuse(cmd, "No API key found for openai")
		h.settle()
		assert.Equal(t, []string{"turn_started", "turn_completed:error"}, h.sink.TurnLog())
		assert.Equal(t, "No API key found for openai", h.sink.State().Results[0].Error)
	})

	t.Run("exited", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		h.prompt("p", "hi")
		h.pi.exit(nil)
		err := h.result("p")
		require.ErrorIs(t, err, native.ErrUnconfirmed)
		assert.ErrorIs(t, err, errExited)
		assert.NotErrorIs(t, h.a.StartTurn(h.ctx, native.Input{Text: "again"}), native.ErrUnconfirmed, "never sent")
	})
}
