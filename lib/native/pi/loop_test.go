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
		h.a.host.StdinGrace = 10 * time.Millisecond
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
	assert.Empty(t, h.a.cmds, "nothing waits for the abort's response")
	h.a.mu.Unlock()
	_, timer := h.clock.Peek()
	assert.False(t, timer, "no interrupt timeout runs")

	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results, "the turn wasn't interrupted")
	closeEscalating(t, h)
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
		// AgentAPI tries it again later.
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonTransient, Error: "529 overloaded after 3 attempts"}}, h.sink.State().Results)
	})

	t.Run("retries that fail on an error worded otherwise", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"Provider hiccup"}`)
		h.write(`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"Provider hiccup"}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonTransient, Error: "Provider hiccup"}}, h.sink.State().Results)
	})

	t.Run("a cancelled retry is an error", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"auto_retry_end","success":false,"attempt":1,"finalError":"Retry cancelled"}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonError, Error: "Retry cancelled"}}, h.sink.State().Results)
	})

	t.Run("a gateway error without retries", func(t *testing.T) {
		t.Parallel()
		h := startedHarness(t)
		cmd := h.prompt("p", "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		h.write(`{"type":"message_end","message":{"role":"assistant","content":[],"model":"m","stopReason":"error","errorMessage":"502 Bad Gateway","timestamp":1}}`)
		h.write(`{"type":"agent_settled"}`)
		h.settle()
		assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonTransient, Error: "502 Bad Gateway", APIStatus: 502}}, h.sink.State().Results)
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

// StartTurn refuses a prompt while a turn runs, before Pi does; a refusal
// for being busy that Pi sends while the adapter saw nothing run is drift.
func TestStartTurnWhileBusy(t *testing.T) {
	t.Parallel()
	h := startedHarness(t)
	cmd := h.prompt("p1", "hi")
	h.answer(cmd, `{"disposition":"started"}`)
	h.write(`{"type":"agent_start"}`)
	require.NoError(t, h.result("p1"))
	h.settle()
	assert.ErrorIs(t, h.a.StartTurn(h.ctx, native.Input{Text: "again"}), native.ErrTurnActive)
	assert.Empty(t, pendingCommands(h), "Pi doesn't get it")
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Len(t, h.sink.State().Results, 1)

	cmd = h.prompt("p2", "hi")
	h.refuse(cmd, "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message.")
	assert.ErrorIs(t, h.result("p2"), native.ErrTurnActive)
	assert.Equal(t, 1, h.a.drift.Counts()["unexpected busy without a run"])
}

// pendingCommands returns the commands the adapter wrote that the test
// hasn't read.
func pendingCommands(h *harness) []map[string]any {
	out := []map[string]any{}
	for {
		select {
		case cmd := <-h.pi.cmds:
			out = append(out, cmd)
		default:
			return out
		}
	}
}

// A run that ends with agent_end and no agent_settled ends its turn
// settleGrace after Pi went quiet; the agent_settled that comes late is
// ignored. A follow-up run, compaction or retry after agent_end keep the
// turn going.
func TestMissingSettle(t *testing.T) {
	t.Parallel()
	h := startedHarness(t, func(h *harness) { h.a.settleGrace = time.Second })
	runTurn := func(id string) {
		cmd := h.prompt(id, "hi")
		h.answer(cmd, `{"disposition":"started"}`)
		h.write(`{"type":"agent_start"}`)
		require.NoError(t, h.result(id))
		h.settle()
	}

	runTurn("p1")
	h.write(`{"type":"agent_end","messages":[],"willRetry":false}`)
	h.settle()
	h.advance(500 * time.Millisecond)
	h.write(`{"type":"queue_update","steering":[],"followUp":[]}`)
	h.settle()
	h.advance(700 * time.Millisecond)
	assert.Empty(t, h.sink.State().Results, "Pi still sends records")
	h.advance(300 * time.Millisecond)
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.sink.State().Results)
	assert.Equal(t, 1, h.a.drift.Counts()["unexpected missing agent_settled"])
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Len(t, h.sink.State().Results, 1, "the late agent_settled is ignored")

	// A follow-up run starts after agent_end.
	runTurn("p2")
	h.write(`{"type":"agent_end","messages":[],"willRetry":false}`)
	h.write(`{"type":"agent_start"}`)
	h.settle()
	h.advance(2 * time.Second)
	assert.Len(t, h.sink.State().Results, 1, "the follow-up runs")
	// Compaction after the run takes long without a record.
	h.write(`{"type":"agent_end","messages":[],"willRetry":false}`)
	h.write(`{"type":"compaction_start","reason":"threshold"}`)
	h.settle()
	h.advance(5 * time.Second)
	assert.Len(t, h.sink.State().Results, 1, "compaction runs")
	h.write(`{"type":"compaction_end","reason":"threshold","aborted":false,"willRetry":false}`)
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Len(t, h.sink.State().Results, 2)
	h.advance(2 * time.Second)
	assert.Len(t, h.sink.State().Results, 2)

	// A retried run doesn't wait for agent_settled.
	runTurn("p3")
	h.write(`{"type":"agent_end","messages":[],"willRetry":true}`)
	h.settle()
	h.advance(2 * time.Second)
	assert.Len(t, h.sink.State().Results, 2)
	h.write(`{"type":"agent_settled"}`)
	h.settle()
	assert.Len(t, h.sink.State().Results, 3)
	assert.Equal(t, 1, h.a.drift.Counts()["unexpected missing agent_settled"])
}
