package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

func TestReplaySimpleText(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "simple_text")
	r := newReplay(t, Config{SnapshotInterval: time.Nanosecond}, "", "")
	r.run(runs[0], replayHooks{})
	// The new session is titled after its first prompt.
	title := r.proc.waitWritten(t, 2)
	assert.Equal(t, "control_request:generate_session_title", signature(title))
	assert.Equal(t, "Is 391 prime? Reason it out carefully first, then answer in one sentence.", title["request"].(map[string]any)["description"])
	require.NoError(t, r.finish(nil))

	// The session id is chosen up front and reported once the session
	// has a turn.
	assert.Contains(t, r.cmd.Args, "--session-id=00000000-0000-4000-8000-000000000001")
	assert.Equal(t, []string{"14726d33-5f93-4e64-802f-90997412bc99"}, r.sink.State().Refs)
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	assert.Equal(t, native.TurnResult{StopReason: "end_turn"}, r.sink.State().Results[0])

	final := r.sink.Final()
	require.Len(t, final, 1)
	msg := final[0]
	assert.Equal(t, "msg_011Cg5Z4JkmFq81S3Ts2oGM2", msg.MessageID)
	assert.Equal(t, "assistant", msg.Role)
	assert.Equal(t, "claude-haiku-5-5", msg.Model)
	assert.Equal(t, "end_turn", msg.StopReason)
	require.NotNil(t, msg.Usage)
	assert.Equal(t, transcript.Usage{InputTokens: 2, OutputTokens: 330, CacheCreationInputTokens: 3908, CacheReadInputTokens: 14174}, *msg.Usage)
	require.Len(t, msg.Content, 2)
	assert.Equal(t, "thinking", msg.Content[0].Type)
	assert.True(t, strings.HasPrefix(msg.Content[0].Thinking, "Checking 391 for prime factors"))
	assert.Equal(t, "text", msg.Content[1].Type)
	assert.True(t, strings.HasSuffix(msg.Content[1].Text, "because it equals 17 × 23.**"))

	// Thinking and text grow live.
	var partialThinking, partialText bool
	for _, s := range r.sink.Snapshots(msg.MessageID) {
		if len(s.Content) == 1 && s.Content[0].Thinking == "Checking 391 for pr" {
			partialThinking = true
		}
		if len(s.Content) == 2 && s.Content[1].Text == "391 is not prime:" {
			partialText = true
		}
	}
	assert.True(t, partialThinking, "no snapshot with partial thinking")
	assert.True(t, partialText, "no snapshot with partial text")

	// Timeline events come from the session-log event parser.
	var kinds []string
	for _, e := range r.sink.State().Events {
		kinds = append(kinds, e.Kind)
		require.NotNil(t, e.SessionID)
		assert.Equal(t, "14726d33-5f93-4e64-802f-90997412bc99", *e.SessionID)
	}
	assert.Equal(t, []string{"thinking", "text"}, kinds)
}

func TestReplayToolApproval(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "tool_approval")
	require.Len(t, runs, 2)

	t.Run("allow", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "", "")
		r.run(runs[0], replayHooks{})
		require.NoError(t, r.finish(nil))

		require.Len(t, r.sink.State().Requests, 1)
		req := r.sink.State().Requests[0]
		assert.Equal(t, native.RequestKindApproval, req.Kind)
		assert.Equal(t, "Bash: touch approved.txt", req.Title)
		assert.Equal(t, "Bash", req.ToolName)
		assert.Equal(t, "toolu_01As4N927s3iq8TxUjpaKWye", req.ToolUseID)
		assert.True(t, req.Feedback)
		assert.JSONEq(t, `{"command":"touch approved.txt","description":"Create empty file approved.txt in working directory"}`, string(req.Input))
		var ids []string
		var kinds []native.OptionKind
		for _, o := range req.Options {
			ids = append(ids, o.ID)
			kinds = append(kinds, o.Kind)
		}
		assert.Equal(t, []string{"allow", "allow_always", "deny"}, ids)
		assert.Equal(t, []native.OptionKind{native.OptionKindAllow, native.OptionKindAllowAlways, native.OptionKindDeny}, kinds)
		assert.Contains(t, req.Options[1].Description, "Bash(touch approved.txt)")

		final := r.sink.Final()
		_, use, ok := findBlock(final, "tool_use", req.ToolUseID)
		require.True(t, ok)
		assert.Equal(t, "Bash", use.ToolName)
		resultMsg, result, ok := findBlock(final, "tool_result", req.ToolUseID)
		require.True(t, ok)
		assert.Equal(t, "user", resultMsg.Role)
		assert.Equal(t, "(Bash completed with no output)", result.Text)
		assert.Equal(t, "completed", result.Status)
		assert.Equal(t, "Done: I ran `touch approved.txt` and it completed without output.", final[len(final)-1].Content[0].Text)
		assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	})

	t.Run("deny", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "", "")
		r.run(runs[1], replayHooks{})
		require.NoError(t, r.finish(nil))

		require.Len(t, r.sink.State().Requests, 1)
		_, result, ok := findBlock(r.sink.Final(), "tool_result", "toolu_01NBXXPAtGhz3cvNUwc4YXZE")
		require.True(t, ok)
		assert.Equal(t, "User denied: do not run shell commands.", result.Text)
		require.NotNil(t, result.IsError)
		assert.True(t, *result.IsError)
		assert.Equal(t, "failed", result.Status)
		assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	})
}

func TestReplayQuestion(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "ask_user_question")
	r := newReplay(t, Config{}, "", "")
	r.run(runs[0], replayHooks{
		respond: func(t *testing.T, req native.Request, _ map[string]any) native.Response {
			require.Equal(t, native.RequestKindQuestion, req.Kind)
			assert.Equal(t, "Which color do you prefer?", req.Title)
			assert.Empty(t, req.Options)
			assert.Equal(t, []native.Question{{
				ID: "q1", Header: "Color", Question: "Which color do you prefer?", FreeText: true,
				Options: []native.QuestionOption{{Label: "Red", Description: "Choose red"}, {Label: "Blue", Description: "Choose blue"}},
			}}, req.Questions)
			// Unanswerable responses are refused.
			err := r.a.Respond(context.Background(), req.ID, native.Response{OptionID: "allow"})
			require.ErrorIs(t, err, native.ErrInvalidResponse)
			err = r.a.Respond(context.Background(), req.ID, native.Response{Answers: map[string][]string{"q2": {"x"}}})
			require.ErrorIs(t, err, native.ErrInvalidResponse)
			return native.Response{Answers: map[string][]string{"q1": {"blue"}}}
		},
	})
	require.NoError(t, r.finish(nil))
	final := r.sink.Final()
	assert.Equal(t, "You chose blue.", final[len(final)-1].Content[0].Text)
	err := r.a.Respond(context.Background(), r.sink.State().Requests[0].ID, native.Response{Answers: map[string][]string{"q1": {"red"}}})
	require.ErrorIs(t, err, native.ErrRequestNotFound)
}

func TestReplayPlan(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "plan_mode")
	require.Len(t, runs, 2)

	t.Run("reject then approve", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{Args: []string{"--permission-mode", "plan"}}, "", "")
		r.run(runs[0], replayHooks{})
		require.NoError(t, r.finish(nil))

		assert.NotContains(t, r.cmd.Args, "default", "the user's permission mode is kept")
		var plans []native.Request
		for _, req := range r.sink.State().Requests {
			if req.Kind == native.RequestKindPlan {
				plans = append(plans, req)
			}
		}
		require.Len(t, plans, 2)
		assert.Equal(t, "ExitPlanMode", plans[0].ToolName)
		assert.True(t, strings.HasPrefix(plans[0].Detail, "# Plan: add `sub(a, b)` to calc.py"))
		assert.True(t, plans[0].Feedback)
		var ids []string
		for _, o := range plans[0].Options {
			ids = append(ids, o.ID)
		}
		assert.Equal(t, []string{"bypassPermissions", "acceptEdits", "default", "deny"}, ids)
		assert.Contains(t, plans[1].Detail, "docstring")

		// The approval switched the mode for the session, as captured.
		frames := r.proc.writtenFrames()
		var approved map[string]any
		for _, f := range frames {
			if f["type"] == "control_response" {
				inner := f["response"].(map[string]any)["response"].(map[string]any)
				if inner["toolUseID"] == plans[1].ToolUseID {
					approved = inner
				}
			}
		}
		require.NotNil(t, approved)
		requireJSONEqual(t, []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}, approved["updatedPermissions"])
		_, result, ok := findBlock(r.sink.Final(), "tool_result", plans[0].ToolUseID)
		require.True(t, ok)
		assert.Equal(t, "failed", result.Status)
		assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	})

	t.Run("approve", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{Args: []string{"--permission-mode", "plan"}}, "", "")
		r.run(runs[1], replayHooks{})
		require.NoError(t, r.finish(nil))

		final := r.sink.Final()
		// The ExitPlanMode call came without its input; the plan from the
		// permission request fills it in.
		_, use, ok := findBlock(final, "tool_use", "toolu_017cCBhdXxohz5o14nmFxDqs")
		require.True(t, ok)
		var input struct{ Plan string }
		require.NoError(t, json.Unmarshal(use.ToolInput, &input))
		assert.True(t, strings.HasPrefix(input.Plan, "# Plan: add `sub(a, b)` to calc.py"))

		// The Write result arrived between the two blocks of one message;
		// the message isn't split.
		msg, _, ok := findBlock(final, "tool_use", "toolu_01BjGMp3gDqJjPiD6ktLPcUu")
		require.True(t, ok)
		var tools []string
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				tools = append(tools, b.ToolName)
			}
		}
		assert.Equal(t, []string{"Write", "ExitPlanMode"}, tools)
		count := 0
		for _, m := range final {
			if m.MessageID == msg.MessageID {
				count++
			}
		}
		assert.Equal(t, 1, count)
	})
}

func TestReplayInterrupt(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "interrupt")
	r := newReplay(t, Config{}, "", "")
	r.run(runs[0], replayHooks{})
	require.NoError(t, r.finish(nil))

	assert.Equal(t, []string{"turn_started", "turn_completed:interrupted", "turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	assert.Equal(t, native.TurnResult{StopReason: "interrupted"}, r.sink.State().Results[0])
	final := r.sink.Final()
	essay := final[0]
	assert.Equal(t, "assistant", essay.Role)
	assert.True(t, strings.HasPrefix(essay.Content[0].Text, "# The Long Road of the Abacus"))
	assert.Empty(t, essay.StopReason)
	// The interrupt marker is a text block, not a tool result.
	marker := final[1]
	assert.Equal(t, "user", marker.Role)
	assert.Equal(t, []transcript.RichContentBlock{{Type: "text", Text: "[Request interrupted by user]"}}, marker.Content)
	assert.Equal(t, "OK", final[2].Content[0].Text)

	// Without a turn there is nothing to stop or steer.
	require.ErrorIs(t, r.a.Interrupt(context.Background()), native.ErrNoActiveTurn)
}

func TestReplaySteer(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "steer")
	require.Len(t, runs, 2)
	steerSecond := func(n int, busy bool) bool {
		return n == 1 && busy
	}

	t.Run("queued", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "", "")
		r.run(runs[0], replayHooks{steer: steerSecond})
		require.NoError(t, r.finish(nil))

		// One turn for both messages; the steered message is written
		// without a priority, so it joins the running turn.
		assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
		for _, f := range r.proc.writtenFrames() {
			assert.NotContains(t, f, "priority")
		}
		var prompts []string
		for _, m := range r.sink.Final() {
			if m.Role == "user" && m.Content[0].Type == "text" {
				prompts = append(prompts, m.Content[0].Text)
			}
		}
		assert.Equal(t, []string{
			"Run `sleep 4 && echo first` with Bash, then tell me its output in one sentence.",
			"Also, at the end, add the word BANANA.",
		}, prompts)
		final := r.sink.Final()
		assert.True(t, strings.HasSuffix(final[len(final)-1].Content[1].Text, "BANANA"))
	})

	t.Run("cli starts a second turn", func(t *testing.T) {
		t.Parallel()
		// With priority "now" the CLI ended the first turn and started one
		// for the steered message by itself.
		r := newReplay(t, Config{}, "", "")
		r.run(runs[1], replayHooks{steer: steerSecond})
		require.NoError(t, r.finish(nil))
		assert.Equal(t, []string{"turn_started", "turn_completed:interrupted", "turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	})
}

func TestReplayResume(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "resume")
	first := newReplay(t, Config{}, "", "")
	first.run(runs[0], replayHooks{})
	require.Error(t, first.finish(errSignalKilled))
	require.Equal(t, []string{"6943de8c-744a-4b64-9bd3-1c16bfe01eb4"}, first.sink.State().Refs)

	r := newReplay(t, Config{}, first.sink.State().Refs[0], "")
	r.run(runs[1], replayHooks{})
	require.NoError(t, r.finish(nil))
	assert.Contains(t, r.cmd.Args, "--resume=6943de8c-744a-4b64-9bd3-1c16bfe01eb4")
	for _, arg := range r.cmd.Args {
		assert.False(t, strings.HasPrefix(arg, "--session-id"))
	}
	// The session keeps its id, which the conversation resumed it with.
	assert.Empty(t, r.sink.State().Refs)
	assert.Equal(t, "PELICAN-42", r.sink.Final()[0].Content[0].Text)
	// A resumed session already has its title.
	for _, f := range r.proc.writtenFrames() {
		assert.NotEqual(t, "generate_session_title", subtypeOf(f))
	}
}

func TestReplayImageInput(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "image_input")
	r := newReplay(t, Config{}, "", "")
	// The written message must equal the captured one: the image first,
	// as base64, then the text.
	r.run(runs[0], replayHooks{})
	require.NoError(t, r.finish(nil))
	_, result, ok := findBlock(r.sink.Final(), "tool_result", "toolu_01Ebm38cZF8xYpVCR2oV1tsj")
	require.True(t, ok)
	require.Len(t, result.Images, 1)
	assert.Equal(t, "image/png", result.Images[0].MediaType)
	assert.Equal(t, []byte("\x89PNG"), result.Images[0].Data[:4])
	final := r.sink.Final()
	assert.Equal(t, "Red", final[len(final)-1].Content[0].Text)
}

func TestReplaySubagent(t *testing.T) {
	t.Parallel()
	const agentCall = "toolu_01NwXmqH7fx13SttiSwd5sUD"
	runs := loadRuns(t, "subagent")
	r := newReplay(t, Config{}, "", "")
	r.run(runs[0], replayHooks{})
	require.NoError(t, r.finish(nil))

	// The subagent's own calls stay out of the main thread, as in the
	// session log.
	final := r.sink.Final()
	_, _, ok := findBlock(final, "tool_use", "toolu_018kVCEw4PbMKh1ojY68kvxx")
	assert.False(t, ok)
	for _, m := range final {
		for _, b := range m.Content {
			assert.NotContains(t, b.Text, "The function defined in `/tmp/agentapi-native/claude/scratch/calc.py`")
		}
	}
	// The background agent's call runs until it reports.
	var statuses []string
	for _, m := range r.sink.State().Rich {
		for _, b := range m.Content {
			if b.Type == "tool_result" && b.ToolUseID == agentCall {
				statuses = append(statuses, b.Status)
			}
		}
	}
	assert.Equal(t, []string{"running", "completed"}, statuses)
	// Without session state events every result ends a turn; the wake
	// turn is a turn of its own.
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn", "turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	assert.Equal(t, "The function in `/tmp/agentapi-native/claude/scratch/calc.py` is `add(a, b)`, which returns `a + b`.", final[len(final)-1].Content[0].Text)
}

func TestReplaySubagentSessionState(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "subagent_state")
	r := newReplay(t, Config{}, "", "")
	r.run(runs[0], replayHooks{})
	require.NoError(t, r.finish(nil))
	// "idle" comes only after the background agent and the turn it woke
	// up finished: one turn.
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	final := r.sink.Final()
	assert.Equal(t, "user", final[0].Role)
	assert.Equal(t, "The function in `calc.py` is `add(a, b)`, which returns `a + b`.", final[len(final)-1].Content[0].Text)
	_, result, ok := findBlock(final, "tool_result", "toolu_01CWZvny46frWrrDMVFvTxTB")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Status)
}

func TestReplayPendingApproval(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "pending_approval")
	require.Len(t, runs, 3)

	t.Run("interrupt withdraws the request", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "", "")
		r.run(runs[0], replayHooks{})
		require.Error(t, r.finish(errExit1))
		require.Len(t, r.sink.State().Requests, 1)
		id := r.sink.State().Requests[0].ID
		assert.Equal(t, []string{id}, r.sink.State().Closed)
		assert.Equal(t, []string{"turn_started", "turn_completed:interrupted"}, r.sink.TurnLog())
		// The request closed before the turn ended.
		log := strings.Join(r.sink.State().Log, " ")
		assert.Less(t, strings.Index(log, "request_closed"), strings.Index(log, "turn_completed"))
		require.ErrorIs(t, r.a.Respond(context.Background(), id, native.Response{OptionID: "allow"}), native.ErrRequestNotFound)
	})

	t.Run("process exits while a request is pending", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "", "")
		r.run(runs[1], replayHooks{})
		require.Len(t, r.sink.State().Requests, 1)
		err := r.finish(errSignalKilled)
		require.ErrorIs(t, err, errSignalKilled)
		// The conversation expires the request when Wait returns; the
		// adapter can't answer it anymore.
		require.Error(t, r.a.Respond(context.Background(), r.sink.State().Requests[0].ID, native.Response{OptionID: "allow"}))
		assert.Equal(t, []string{"turn_started"}, r.sink.TurnLog())
	})

	t.Run("resume after the kill", func(t *testing.T) {
		t.Parallel()
		r := newReplay(t, Config{}, "1b2c3d4e-0000-4000-8000-000000000000", "")
		r.run(runs[2], replayHooks{})
		require.NoError(t, r.finish(nil))
		assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, r.sink.TurnLog())
	})
}

func TestReplaySessionTitle(t *testing.T) {
	t.Parallel()
	runs := loadRuns(t, "session_title")
	r := newReplay(t, Config{}, "", "")
	r.run(runs[0], replayHooks{})
	require.NoError(t, r.finish(nil))
	assert.Equal(t, []string{"Primality of 391", "AgentAPI capture test"}, r.sink.State().Names)
}
