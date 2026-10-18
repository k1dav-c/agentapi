package pi

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
)

// noState answers the handshake of fixtures captured without get_state.
const noState = `{"sessionId":"test-session","sessionFile":"/tmp/test-session.jsonl","model":{"provider":"openai-codex","id":"gpt-5.6-luna","input":["text","image"]}}`

func texts(msg jsonlwatcher.RichMessage, typ string) []string {
	var out []string
	for _, b := range msg.Content {
		if b.Type != typ {
			continue
		}
		switch typ {
		case "thinking":
			out = append(out, b.Thinking)
		default:
			out = append(out, b.Text)
		}
	}
	return out
}

func findRich(msgs []jsonlwatcher.RichMessage, fn func(jsonlwatcher.RichMessage) bool) []jsonlwatcher.RichMessage {
	var out []jsonlwatcher.RichMessage
	for _, m := range msgs {
		if fn(m) {
			out = append(out, m)
		}
	}
	return out
}

func toolResults(msgs []jsonlwatcher.RichMessage) []jsonlwatcher.RichContentBlock {
	var out []jsonlwatcher.RichContentBlock
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				out = append(out, b)
			}
		}
	}
	return out
}

func toolUses(msgs []jsonlwatcher.RichMessage) []jsonlwatcher.RichContentBlock {
	var out []jsonlwatcher.RichContentBlock
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_use" {
				out = append(out, b)
			}
		}
	}
	return out
}

func closeAdapter(t *testing.T, h *harness) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = h.a.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close didn't return")
	}
}

func TestReplaySimpleText(t *testing.T) {
	t.Parallel()
	cwd := "/tmp/agentapi-native/pi/scratch"
	h := newHarness(t, Config{Args: []string{"--model", "openai-codex/gpt-5.6-luna", "--thinking", "medium"}, CWD: cwd}, "")
	h.replay(loadFixture(t, "simple_text.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))

	argv := h.pi.argv
	require.Len(t, argv, 8)
	assert.Equal(t, []string{"--mode", "rpc", "--session-id"}, argv[:3])
	assert.Regexp(t, regexp.MustCompile(`^agentapi-[0-9a-f]{16}$`), argv[3])
	assert.Equal(t, []string{"--model", "openai-codex/gpt-5.6-luna", "--thinking", "medium"}, argv[4:])
	assert.Equal(t, []string{"get_state", "prompt"}, h.sentTypes())

	assert.Equal(t, []string{"agentapi-fixture-simple2"}, h.sink.State().Refs)
	assert.Equal(t, []string{"simple fixture", "renamed fixture"}, h.sink.State().Names)
	assert.Equal(t, 1, h.sink.State().Started)
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, h.sink.State().Results)
	// The context after the reply, in the model's window.
	require.NotEmpty(t, h.sink.State().Usage)
	usage := h.sink.State().Usage[len(h.sink.State().Usage)-1].Context
	require.NotNil(t, usage)
	assert.Equal(t, 272000, usage.Size)
	assert.Positive(t, usage.Used)

	final := h.sink.Final()
	require.Len(t, final, 2)
	user, assistant := final[0], final[1]
	assert.Equal(t, "user", user.Role)
	assert.Equal(t, []string{"Think step by step about what 17*23 is, then reply with just the number."}, texts(user, "text"))
	assert.Equal(t, "assistant", assistant.Role)
	assert.Equal(t, []string{"**Returning numeric answer 391**"}, texts(assistant, "thinking"))
	assert.Equal(t, []string{"391"}, texts(assistant, "text"))
	assert.Equal(t, "thinking", assistant.Content[0].Type, "thinking comes before the text")
	assert.Equal(t, "gpt-5.6-luna", assistant.Model)
	assert.Equal(t, "end_turn", assistant.StopReason)
	require.NotNil(t, assistant.Usage)
	assert.Equal(t, jsonlwatcher.Usage{InputTokens: 1151, OutputTokens: 20}, *assistant.Usage)
	assert.Equal(t, "assistant-1792137197230", assistant.MessageID)

	// The deltas were streamed as snapshots of the same message.
	snaps := h.sink.Snapshots(assistant.MessageID)
	require.GreaterOrEqual(t, len(snaps), 2)
	assert.Empty(t, snaps[0].StopReason, "a snapshot is not final")
	assert.NotEmpty(t, snaps[0].Content)

	require.NotEmpty(t, h.sink.State().Events)
	first := h.sink.State().Events[0]
	assert.Equal(t, "system", first.Kind)
	assert.Equal(t, "session started: "+cwd+" (pi)", *first.Content)
	assert.Equal(t, "agentapi-fixture-simple2", *first.SessionID)
	var kinds []string
	for _, e := range h.sink.State().Events[1:] {
		kinds = append(kinds, e.Kind+"/"+*e.Role)
	}
	assert.Equal(t, []string{"text/user", "thinking/assistant", "text/assistant"}, kinds)
	assert.Empty(t, h.sink.State().Errors)

	closeAdapter(t, h)
	assert.NoError(t, h.a.Wait())
	assert.Equal(t, "stdin closed", h.pi.events()[len(h.pi.events())-1])
}

func TestReplayToolCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "tool_call.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))

	final := h.sink.Final()
	uses := toolUses(final)
	results := toolResults(final)
	require.Len(t, uses, 3)
	require.Len(t, results, 3)
	for i, name := range []string{"read", "edit", "bash"} {
		assert.Equal(t, name, uses[i].ToolName)
		assert.Equal(t, uses[i].ToolUseID, results[i].ToolUseID, "tool_result pairs with its tool_use")
		assert.Equal(t, "completed", results[i].Status)
		require.NotNil(t, results[i].IsError)
		assert.False(t, *results[i].IsError)
	}
	assert.JSONEq(t, `{"path":"hello.txt","offset":null,"limit":null}`, string(uses[0].ToolInput))
	assert.Equal(t, "hello from scratch\n", results[0].Text)
	assert.Equal(t, "Successfully replaced 1 block(s) in hello.txt.", results[1].Text)
	assert.Equal(t, "hello from pi\n M hello.txt\n", results[2].Text)

	// Tool results are user messages, like the Pi parser makes them.
	for _, m := range findRich(final, func(m jsonlwatcher.RichMessage) bool { return strings.HasPrefix(m.MessageID, "result-") }) {
		assert.Equal(t, "user", m.Role)
	}
	// Bash output was shown while the command ran.
	bashID := "result-" + uses[2].ToolUseID
	snaps := h.sink.Snapshots(bashID)
	require.NotEmpty(t, snaps)
	assert.Equal(t, "running", snaps[0].Content[0].Status)

	last := final[len(final)-1]
	assert.Equal(t, []string{"done"}, texts(last, "text"))
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, h.sink.State().Results)
	assert.Equal(t, 1, h.sink.State().Started)
}

func TestReplayExtensionDialogs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{Args: []string{"-e", "rpc-demo.ts"}}, "")
	h.answerState(noState)
	// The /rpc-input prompt is answered only after its dialog: StartTurn
	// must return before that, or the dialog couldn't be answered.
	h.drive["fbfc5523-4106-4a9e-9de0-1391b3526c04"] = func(m map[string]any) bool {
		require.Eventually(t, func() bool { return h.done("p1") }, 5*time.Second, time.Millisecond)
		require.NoError(t, h.result("p1"))
		return h.defaultDrive(m)
	}
	h.replay(loadFixture(t, "extension_ui.ndjson")[0])
	h.started()
	for _, id := range []string{"p1", "p2", "p3", "fbfc5523-4106-4a9e-9de0-1391b3526c04", "7a0091f5-b02a-4261-b1aa-d6bb410f95b6", "b8dcad4f-73df-4738-8bea-3292f684c796", "ae35aaaa-77c8-4c63-9f15-6d2d8fbc1fa9"} {
		require.NoError(t, h.result(id), id)
	}

	assert.Equal(t, []string{"pi RPC Demo"}, h.sink.State().Names, "setTitle stands in for the session name")
	require.Len(t, h.sink.State().Requests, 4)

	input := h.sink.State().Requests[0]
	assert.Equal(t, native.RequestKindQuestion, input.Kind)
	assert.Equal(t, "Enter a value", input.Title)
	assert.Equal(t, "type something...", input.Detail)
	assert.Equal(t, []native.Question{{ID: valueQuestion, Question: "Enter a value", FreeText: true}}, input.Questions)
	assert.Equal(t, []native.RequestOption{{ID: optionCancel, Label: "Cancel", Kind: native.OptionKindCancel}}, input.Options)

	approval := h.sink.State().Requests[1]
	assert.Equal(t, native.RequestKindApproval, approval.Kind)
	assert.Equal(t, "Dangerous command: rm -rf junkdir", approval.Title)
	assert.Equal(t, "bash", approval.ToolName)
	assert.Equal(t, "call_9362c31ec3e943a19ec8cfab6b71badf|fc_0b32c3692bef9e49016ad1d825dc9c81958f6b90567074a199", approval.ToolUseID)
	assert.JSONEq(t, `{"command":"rm -rf junkdir","timeout":null}`, string(approval.Input))
	assert.Equal(t, []native.RequestOption{
		{ID: "Allow", Label: "Allow", Kind: native.OptionKindAllow},
		{ID: "Block", Label: "Block", Kind: native.OptionKindDeny},
	}, approval.Options)

	confirm := h.sink.State().Requests[2]
	assert.Equal(t, native.RequestKindApproval, confirm.Kind)
	assert.Equal(t, "Clear session?", confirm.Title)
	assert.Equal(t, "All messages will be lost.", confirm.Detail)
	assert.Empty(t, confirm.ToolUseID)
	assert.Equal(t, []string{optionYes, optionNo}, []string{confirm.Options[0].ID, confirm.Options[1].ID})

	assert.Equal(t, native.RequestKindQuestion, h.sink.State().Requests[3].Kind)

	// The commands matched the capture: value, Block, confirmed false,
	// cancelled.
	var replies []string
	for _, c := range h.sent {
		if c["type"] == "extension_ui_response" {
			b, _ := json.Marshal(c)
			replies = append(replies, string(b))
		}
	}
	assert.Equal(t, []string{
		`{"id":"fbfc5523-4106-4a9e-9de0-1391b3526c04","type":"extension_ui_response","value":"typed by client"}`,
		`{"id":"7a0091f5-b02a-4261-b1aa-d6bb410f95b6","type":"extension_ui_response","value":"Block"}`,
		`{"confirmed":false,"id":"b8dcad4f-73df-4738-8bea-3292f684c796","type":"extension_ui_response"}`,
		`{"cancelled":true,"id":"ae35aaaa-77c8-4c63-9f15-6d2d8fbc1fa9","type":"extension_ui_response"}`,
	}, replies)

	// Two extension commands without a run and one run: three turns. The
	// confirm came outside a turn.
	assert.Equal(t, 3, h.sink.State().Started)
	assert.Equal(t, []native.TurnResult{
		{StopReason: native.StopReasonHandled},
		{StopReason: native.StopReasonEndTurn},
		{StopReason: native.StopReasonHandled},
	}, h.sink.State().Results)

	results := toolResults(h.sink.Final())
	require.Len(t, results, 1)
	assert.Equal(t, "Blocked by user", results[0].Text)
	assert.Equal(t, "failed", results[0].Status)
	assert.Empty(t, h.sink.State().Closed)

	// Answered requests are gone.
	assert.ErrorIs(t, h.a.Respond(h.ctx, approval.ID, native.Response{OptionID: "Allow"}), native.ErrRequestNotFound)
}

func TestReplayApprovalAllow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "approval_allow.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p2"))
	require.NoError(t, h.result("7a0091f5-b02a-4261-b1aa-d6bb410f95b6"))

	require.Len(t, h.sink.State().Requests, 1)
	assert.Equal(t, native.RequestKindApproval, h.sink.State().Requests[0].Kind)
	allow := h.sent[len(h.sent)-1]
	assert.Equal(t, "Allow", allow["value"])
	results := toolResults(h.sink.Final())
	require.Len(t, results, 1)
	assert.Equal(t, "completed", results[0].Status)
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, h.sink.State().Results)
	final := h.sink.Final()
	assert.Equal(t, []string{"Removed"}, texts(final[len(final)-1], "text"))
}

func TestRespondValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	records := loadFixture(t, "extension_ui.ndjson")[0]
	// Up to the select dialog of the bash tool call.
	cut := slices.IndexFunc(records, func(r record) bool { return strings.Contains(string(r.Msg), `"method":"select"`) })
	h.replay(records[:cut+1])
	h.started()
	id := "7a0091f5-b02a-4261-b1aa-d6bb410f95b6"

	assert.ErrorIs(t, h.a.Respond(h.ctx, "nope", native.Response{OptionID: "Allow"}), native.ErrRequestNotFound)
	assert.ErrorIs(t, h.a.Respond(h.ctx, id, native.Response{OptionID: "Maybe"}), native.ErrInvalidResponse)
	require.NoError(t, h.a.Respond(h.ctx, id, native.Response{OptionID: "Allow"}))
	cmd := h.pi.nextCmd()
	assert.Equal(t, "Allow", cmd["value"])
}

func TestReplayAbortDuringTool(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "abort.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))
	require.NoError(t, h.result("a1"))

	assert.Equal(t, []string{"get_state", "prompt", "abort"}, h.sentTypes(), "the second abort, while idle, isn't sent")
	assert.Equal(t, []native.TurnResult{{StopReason: "interrupted"}}, h.sink.State().Results)
	final := h.sink.Final()
	for _, m := range final {
		for _, text := range texts(m, "text") {
			assert.NotContains(t, text, "Error:", "the aborted run's extra message is no error")
		}
	}
	results := toolResults(final)
	require.Len(t, results, 1)
	assert.Equal(t, "Command aborted", results[0].Text)
	assert.Equal(t, "failed", results[0].Status)
	assert.Empty(t, h.sink.State().Errors)
}

func TestReplayAbortDuringStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "abort_stream.ndjson")[0])
	h.started()
	require.NoError(t, h.result("a1"))

	assert.Equal(t, []native.TurnResult{{StopReason: "interrupted"}}, h.sink.State().Results)
	final := h.sink.Final()
	last := final[len(final)-1]
	assert.Equal(t, "aborted", last.StopReason)
	require.Len(t, texts(last, "text"), 1)
	assert.True(t, strings.HasPrefix(texts(last, "text")[0], "Every evening, Elias climbed"))
	// The partial text streamed before the abort.
	assert.Greater(t, len(h.sink.Snapshots(last.MessageID)), 1)
}

func TestReplaySteer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "steer.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))
	assert.ErrorIs(t, h.result("p2"), native.ErrTurnActive, "Pi refuses a second prompt while it works")
	require.NoError(t, h.result("st1"))
	require.NoError(t, h.result("p3"))

	steers := 0
	for _, c := range h.sent {
		if c["streamingBehavior"] == "steer" {
			steers++
			assert.Equal(t, "prompt", c["type"])
		}
	}
	assert.Equal(t, 2, steers)

	final := h.sink.Final()
	users := findRich(final, func(m jsonlwatcher.RichMessage) bool {
		return m.Role == "user" && !strings.HasPrefix(m.MessageID, "result-")
	})
	require.Len(t, users, 3)
	assert.Equal(t, []string{"Also: end your final reply with the word BANANA."}, texts(users[1], "text"))
	assert.Equal(t, []string{"And mention the word KIWI too."}, texts(users[2], "text"))
	assert.NotEqual(t, users[1].MessageID, users[2].MessageID)
	assert.Equal(t, 1, h.sink.State().Started)
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, h.sink.State().Results)
	assert.Equal(t, []string{"The command output was `step1`; KIWI. BANANA"}, texts(final[len(final)-1], "text"))
}

func TestReplayFollowUpsStayInOneTurn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "follow_up.ndjson")[0])
	h.started()
	assert.Equal(t, 1, h.sink.State().Started)
	assert.Len(t, h.sink.State().Results, 1)
}

func TestReplayResume(t *testing.T) {
	t.Parallel()
	procs := loadFixture(t, "resume.ndjson")
	require.Len(t, procs, 4)
	h := newHarness(t, Config{Args: []string{"--session-dir", "/tmp/sessions"}}, "agentapi-fixture-resume")
	h.replay(procs[1])
	h.started()
	require.NoError(t, h.result("p2"))

	assert.Equal(t, []string{"--mode", "rpc", "--session-id", "agentapi-fixture-resume", "--session-dir", "/tmp/sessions"}, h.pi.argv)
	assert.Equal(t, []string{"agentapi-fixture-resume"}, h.sink.State().Refs)
	final := h.sink.Final()
	last := final[len(final)-1]
	assert.Equal(t, []string{"PINEAPPLE-42"}, texts(last, "text"))
	assert.Empty(t, texts(last, "thinking"), "encrypted thinking without text is dropped")
}

func TestReplayImageInput(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.replay(loadFixture(t, "image_input.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))

	assert.True(t, h.a.Capabilities().Images)
	prompt := h.sent[1]
	images := prompt["images"].([]any)
	require.Len(t, images, 1)
	assert.Equal(t, "image/png", images[0].(map[string]any)["mimeType"])
	final := h.sink.Final()
	assert.Equal(t, []string{"What single colour is this image? Reply with one word."}, texts(final[0], "text"))
	assert.Equal(t, []string{"Red"}, texts(final[len(final)-1], "text"))
}

func TestReplayModelError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.replay(loadFixture(t, "model_error.ndjson")[0])
	h.started()
	require.NoError(t, h.result("p1"))

	results := h.sink.State().Results
	require.Len(t, results, 1)
	assert.Equal(t, "error", results[0].StopReason)
	assert.Contains(t, results[0].Error, "Codex error: The 'gpt-5.3-codex-spark' model is not supported")
	final := h.sink.Final()
	last := final[len(final)-1]
	assert.Equal(t, "error", last.StopReason)
	require.Len(t, texts(last, "text"), 1)
	assert.True(t, strings.HasPrefix(texts(last, "text")[0], "Error: Codex error"))
	assert.Empty(t, h.sink.State().Logins, "a model the account can't use is not a login problem")
}

func TestExitWhileRequestPending(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	records := loadFixture(t, "extension_ui.ndjson")[0]
	cut := slices.IndexFunc(records, func(r record) bool { return strings.Contains(string(r.Msg), `"method":"select"`) })
	h.replay(records[:cut+1])
	h.started()
	require.Len(t, h.sink.State().Requests, 2)

	h.pi.stderr = "Error: out of memory"
	h.pi.exit(errors.New("exit status 1"))
	err := h.a.Wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "out of memory")

	select {
	case <-h.a.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the event loop didn't stop")
	}
	assert.ErrorIs(t, h.a.Respond(h.ctx, "7a0091f5-b02a-4261-b1aa-d6bb410f95b6", native.Response{OptionID: "Allow"}), native.ErrRequestNotFound)
	assert.ErrorIs(t, h.a.StartTurn(h.ctx, native.Input{Text: "hi"}), errExited)
	closeAdapter(t, h)
}

func TestCloseAbortsRunningTurn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	closed := make(chan struct{})
	h.drive["a1"] = func(map[string]any) bool {
		go func() {
			_ = h.a.Close()
			close(closed)
		}()
		return true
	}
	records := loadFixture(t, "abort.ndjson")[0]
	// Stop after the abort response; Close then closes stdin.
	cut := slices.IndexFunc(records, func(r record) bool { return strings.Contains(string(r.Msg), `"command":"abort"`) })
	h.replay(records[:cut+1])
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close didn't return")
	}
	events := h.pi.events()
	assert.Equal(t, []string{"cmd:get_state", "cmd:prompt", "cmd:abort", "stdin closed"}, events)
	assert.Equal(t, []native.TurnResult{{StopReason: "interrupted"}}, h.sink.State().Results)
	assert.NoError(t, h.a.Wait())
}

func TestCloseEscalates(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "", func(h *harness) {
		h.pi.exitOnStdinClose = false
		h.pi.ignoreTerminate = true
		h.a.stdinGrace, h.a.termGrace = 20*time.Millisecond, 20*time.Millisecond
	})
	h.answerState(noState)
	h.started()
	closeAdapter(t, h)
	closeAdapter(t, h) // idempotent
	assert.Equal(t, []string{"cmd:get_state", "stdin closed", "SIGTERM", "SIGKILL"}, h.pi.events())
	assert.Error(t, h.a.Wait())
}

func TestStartFailsWhenPiExits(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	_ = h.pi.nextCmd()
	h.pi.stderr = "Error: No models available"
	h.pi.exit(errors.New("exit status 1"))
	select {
	case err := <-h.startErr:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No models available", "Start says why Pi exited")
	case <-time.After(5 * time.Second):
		t.Fatal("Start didn't return")
	}
	err := h.a.Wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No models available")
}

func TestDialogTimeoutClosesRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.started()
	h.writeLine(map[string]any{"type": "extension_ui_request", "id": "d1", "method": "select", "title": "Pick", "options": []string{"a", "b"}, "timeout": 30})
	h.settle()
	h.advance(29 * time.Millisecond)
	assert.Empty(t, h.sink.State().Closed)
	h.advance(time.Millisecond)
	assert.Equal(t, []string{"d1"}, h.sink.State().Closed)
	req, ok := h.sink.Request("d1")
	require.True(t, ok)
	assert.Equal(t, native.RequestKindQuestion, req.Kind)
	assert.ErrorIs(t, h.a.Respond(h.ctx, "d1", native.Response{Answers: map[string][]string{choiceQuestion: {"a"}}}), native.ErrRequestNotFound)
}

func TestNotifyErrorIsReported(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.started()
	h.writeLine(map[string]any{"type": "extension_ui_request", "id": "n1", "method": "notify", "message": "MCP server failed", "notifyType": "error"})
	h.writeLine(map[string]any{"type": "extension_ui_request", "id": "n2", "method": "notify", "message": "fine", "notifyType": "info"})
	h.settle()
	assert.Equal(t, []string{"error: MCP server failed"}, h.sink.State().Errors)
}

func TestThrottle(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.started()
	h.write(`{"type":"message_start","message":{"role":"assistant","content":[],"model":"m","timestamp":5}}`)
	h.write(`{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`)
	for _, d := range []string{"a", "b", "c", "d"} {
		h.write(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + d + `"}}`)
	}
	h.settle()
	snaps := h.sink.Snapshots("assistant-5")
	require.Len(t, snaps, 1, "one snapshot per interval")
	assert.Equal(t, []string{"a"}, texts(snaps[0], "text"))
	h.advance(snapshotInterval - time.Millisecond)
	assert.Len(t, h.sink.Snapshots("assistant-5"), 1)
	h.advance(time.Millisecond)
	snaps = h.sink.Snapshots("assistant-5")
	require.Len(t, snaps, 2)
	assert.Equal(t, []string{"abcd"}, texts(snaps[1], "text"), "the latest state is sent when the interval ends")
	h.write(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"abcd!"}],"model":"m","stopReason":"stop","timestamp":5}}`)
	h.settle()
	snaps = h.sink.Snapshots("assistant-5")
	assert.Equal(t, []string{"abcd!"}, texts(snaps[len(snaps)-1], "text"))
	assert.Equal(t, "end_turn", snaps[len(snaps)-1].StopReason)
	_, pending := h.clock.Peek()
	assert.False(t, pending, "no snapshot waits after the final message")
}

func TestToolResultImages(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noState)
	h.started()
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message_end","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"Read image file [image/png]"},{"type":"image","data":"`+png+`","mimeType":"image/png"}],"isError":false,"timestamp":7}}`), &v))
	h.writeLine(v)
	h.settle()
	results := toolResults(h.sink.Final())
	require.Len(t, results, 1)
	require.Len(t, results[0].Images, 1)
	assert.Equal(t, "image/png", results[0].Images[0].MediaType)
	assert.NotEmpty(t, results[0].Images[0].Data)
}

// TestParity compares the adapter's final messages with what the existing
// session-log parser produces from the session file of the same run.
func TestParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture, session string
		noHandshake      bool
	}{
		{fixture: "simple_text.ndjson", session: "session_simple_text.jsonl"},
		{fixture: "misc_commands.ndjson", session: "session_misc_commands.jsonl", noHandshake: true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, Config{}, "")
			if tc.noHandshake {
				h.answerState(noState)
			}
			h.replay(loadFixture(t, tc.fixture)[0])
			h.started()

			data, err := os.ReadFile("testdata/" + tc.session)
			require.NoError(t, err)
			parser := jsonlwatcher.NewPiParser()
			var want []jsonlwatcher.RichMessage
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if strings.Contains(line, `"role":"bashExecution"`) {
					// The RPC bash command, which AgentAPI doesn't send.
					continue
				}
				msgs, err := parser.ParseLine([]byte(line))
				require.NoError(t, err)
				want = append(want, msgs...)
			}
			require.NotEmpty(t, want)
			got := stripVolatile(h.sink.Final())
			assert.True(t, equalJSON(stripVolatile(want), got), "want %s\n got %s", mustJSON(stripVolatile(want)), mustJSON(got))
		})
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
