package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

func blockTypes(msg jsonlwatcher.RichMessage) []string {
	var types []string
	for _, b := range msg.Content {
		types = append(types, b.Type)
	}
	return types
}

func toolInput(t *testing.T, b jsonlwatcher.RichContentBlock) map[string]any {
	t.Helper()
	var in map[string]any
	require.NoError(t, json.Unmarshal(b.ToolInput, &in))
	return in
}

func optionIDs(req native.Request) []string {
	var ids []string
	for _, o := range req.Options {
		ids = append(ids, o.ID)
	}
	return ids
}

func TestSimpleTextWithReasoning(t *testing.T) {
	h := runFixture(t, "simple_text", "", nil)
	s := snapshot(h.rec)

	// Handshake.
	var init initializeParams
	require.NoError(t, json.Unmarshal(h.sentParams("initialize"), &init))
	assert.Equal(t, "agentapi", init.ClientInfo.Name)
	assert.Contains(t, init.Capabilities.OptOutNotificationMethods, "turn/moderationMetadata")
	assert.JSONEq(t, `{"cwd":"/work/scratch","approvalPolicy":"never","sandbox":"danger-full-access"}`, string(h.sentParams("thread/start")),
		"the thread bypasses approvals and the sandbox unless the command line chooses")
	assert.JSONEq(t, `{"threadId":"01a143b3-8e2d-70d3-92ab-ba72fcfd3a3a","summary":"detailed",
		"input":[{"type":"text","text":"How many integers n with 1 <= n <= 60 make n^2 + n + 41 composite? Work it out carefully, then answer with one short sentence. Do not run any commands."}]}`,
		string(h.sentParams("turn/start")))

	assert.Equal(t, []string{"01a143b3-8e2d-70d3-92ab-ba72fcfd3a3a"}, s.refs)
	assert.Equal(t, []string{"probe simple text"}, s.names, "thread/name/updated is the session name")
	assert.Equal(t, 1, s.started)
	require.Len(t, s.completions, 1)
	assert.Equal(t, native.TurnResult{StopReason: "end_turn"}, s.completions[0])
	assert.Contains(t, s.errors[0], "warning: Codex is ignoring 1 unrecognized configuration setting")

	user, ok := h.rec.Message("01a143b3-952b-7762-873d-7c75dce093a1", "user")
	require.True(t, ok)
	assert.Equal(t, []jsonlwatcher.RichContentBlock{{Type: "text", Text: "How many integers n with 1 <= n <= 60 make n^2 + n + 41 composite? Work it out carefully, then answer with one short sentence. Do not run any commands."}}, user.Content)

	msg, ok := h.rec.Message("01a143b3-906e-7190-8322-7884ae84fa90", "assistant")
	require.True(t, ok)
	assert.Equal(t, []string{"thinking", "thinking", "thinking", "thinking", "thinking", "thinking", "thinking", "text"}, blockTypes(msg))
	assert.Equal(t, "**Checking values through 60**\n\n**Factoring values above 44**", msg.Content[0].Thinking)
	assert.Equal(t, `Exactly 5 integers: \(n=40,41,44,49,\) and \(56\) make \(n^2+n+41\) composite.`, msg.Content[7].Text)
	assert.Equal(t, "gpt-6-luna", msg.Model)
	assert.Equal(t, "end_turn", msg.StopReason)
	assert.Equal(t, &jsonlwatcher.Usage{InputTokens: 13689, OutputTokens: 3509, CacheReadInputTokens: 11008}, msg.Usage)

	// Thinking streamed: a snapshot showed the first summary part alone.
	sawPartial := false
	for _, snap := range h.rec.State().Rich {
		if snap.MessageID == msg.MessageID && len(snap.Content) > 0 && snap.Content[0].Thinking == "**Checking values through 60**" {
			sawPartial = true
		}
	}
	assert.True(t, sawPartial, "reasoning deltas stream into the snapshot")

	system := s.eventsOf("system")
	require.NotEmpty(t, system)
	assert.Equal(t, "session started: /work/scratch (codex 0.160.0)", *system[0].Content)
	assert.Len(t, s.eventsOf("thinking"), 7)
	texts := s.eventsOf("text")
	require.Len(t, texts, 2)
	assert.Equal(t, "user", *texts[0].Role)
	assert.Equal(t, "assistant", *texts[1].Role)
}

func TestEnvironmentAndCommandLine(t *testing.T) {
	var spec procutil.Command
	proc := newFakeProc()
	factory, err := NewFactory(Config{
		Program:     "/usr/bin/codex",
		Args:        []string{"-m", "gpt-6-luna", "-c", `model_reasoning_effort="low"`, "--enable", "foo", "-a", "untrusted", "-s", "danger-full-access", "--no-alt-screen"},
		CWD:         "/work",
		Env:         []string{"PATH=/bin", "CODEX_THREAD_ID=x", "CODEX_SANDBOX=seatbelt", "CODEX_HOME=/h", "CLAUDECODE=1"},
		Launch:      func(c procutil.Command) (procutil.Process, error) { spec = c; return proc, nil },
		StopTimeout: 10 * time.Millisecond,
	})
	require.NoError(t, err)
	adapter, err := factory("")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	go func() { _, _ = proc.stdinR.Read(make([]byte, 4096)) }()
	require.Error(t, adapter.Start(ctx, newRecorder(t)), "nothing answers initialize")
	require.NoError(t, adapter.Close())

	assert.Equal(t, "/usr/bin/codex", spec.Program)
	assert.Equal(t, []string{"app-server", "-c", `model_reasoning_effort="low"`, "--enable", "foo"}, spec.Args)
	assert.Equal(t, []string{"PATH=/bin", "CODEX_HOME=/h"}, spec.Env)
	assert.Equal(t, "/work", spec.Dir)
}

func TestThreadSettingsFromFlags(t *testing.T) {
	h := newHarness(t, "", []string{"-m", "gpt-6-luna", "-a", "untrusted", "-s", "danger-full-access", "-C", "sub"})
	lines := loadFixture(t, "command_approval_accept")
	// Up to and including the thread/start request.
	for i, l := range lines {
		if l.Dir == "out" && strings.Contains(string(l.Msg), `"thread/start"`) {
			h.replay(lines[:i+1])
			break
		}
	}
	assert.JSONEq(t, `{"cwd":"/work/scratch/sub","model":"gpt-6-luna","approvalPolicy":"untrusted","sandbox":"danger-full-access"}`,
		string(h.sentParams("thread/start")))
}

func TestCommandApprovalAccept(t *testing.T) {
	h := runFixture(t, "command_approval_accept", "", nil)
	s := snapshot(h.rec)

	require.Len(t, s.opened, 1)
	req := s.opened[0]
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, `python3 -c "print(6*7)"`, req.Title)
	assert.Equal(t, "exec_command", req.ToolName)
	assert.Equal(t, "exec-7ce2bd20-5834-4962-96d7-142110034025", req.ToolUseID)
	assert.JSONEq(t, `{"cmd":"python3 -c \"print(6*7)\"","workdir":"/work/scratch"}`, string(req.Input))
	assert.Equal(t, []string{"accept", "acceptWithExecpolicyAmendment", "decline", "cancel"}, optionIDs(req))
	assert.Equal(t, native.OptionKindAllowAlways, req.Options[1].Kind)
	assert.Equal(t, "Always allow commands starting with `python3 -c print(6*7)`", req.Options[1].Label)
	assert.Equal(t, native.OptionKindDeny, req.Options[2].Kind)
	assert.Equal(t, native.OptionKindCancel, req.Options[3].Kind)
	assert.Empty(t, s.closed, "an answered request is not withdrawn")
	assert.Empty(t, h.a.requests)

	msg, ok := h.rec.Message("01a143b4-473d-7b12-a2e9-39a5c82015ea", "assistant")
	require.True(t, ok)
	assert.Equal(t, []string{"tool_use", "text"}, blockTypes(msg))
	assert.Equal(t, "completed", msg.Content[0].Status)
	assert.Equal(t, "exec_command", msg.Content[0].ToolName)
	assert.Equal(t, "42", msg.Content[1].Text)

	result, ok := h.rec.Message("result-exec-7ce2bd20-5834-4962-96d7-142110034025", "user")
	require.True(t, ok)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "tool_result", result.Content[0].Type)
	assert.Equal(t, "42", result.Content[0].Text)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.False(t, *result.Content[0].IsError)

	calls := s.eventsOf("tool_call")
	require.Len(t, calls, 1)
	assert.Equal(t, "exec_command", *calls[0].ToolName)
	results := s.eventsOf("tool_result")
	require.Len(t, results, 1)
	assert.Equal(t, "42", *results[0].Content)
}

func TestCommandApprovalDecline(t *testing.T) {
	h := runFixture(t, "command_approval_decline", "", nil)
	s := snapshot(h.rec)
	require.Len(t, s.opened, 1)

	result, ok := h.rec.Message("result-exec-50a6c648-c22d-48e9-9cf1-e11d1a959803", "user")
	require.True(t, ok)
	assert.Equal(t, "Command declined by the user", result.Content[0].Text)
	assert.Equal(t, "failed", result.Content[0].Status)
	assert.True(t, *result.Content[0].IsError)

	msg, _ := h.rec.Message("01a143b4-5c46-70e3-b3d4-31e6c318044d", "assistant")
	assert.Equal(t, "failed", msg.Content[0].Status)
	assert.Equal(t, "The command was not allowed; it was rejected.", msg.Content[1].Text)
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, s.completions)
}

func TestUnansweredApprovalThenInterrupt(t *testing.T) {
	h := runFixture(t, "command_approval_unanswered", "", nil)
	s := snapshot(h.rec)

	require.NoError(t, h.err("turn/interrupt"))
	require.Len(t, s.opened, 1)
	assert.Equal(t, []native.TurnResult{{StopReason: "interrupted"}}, s.completions)
	// The conversation expires the request when the turn ends; the
	// adapter declined it then and refused the late reply (see replayOut).
	assert.Empty(t, h.a.requests)

	msg, _ := h.rec.Message("01a143b4-9c0b-7021-a76f-53bf2628f666", "assistant")
	assert.Equal(t, "interrupted", msg.StopReason)
	require.Equal(t, []string{"tool_use"}, blockTypes(msg))
	assert.Equal(t, "failed", msg.Content[0].Status, "the open command is closed when the turn ends")
	result, ok := h.rec.Message("result-exec-03689302-ad96-4a3c-93c2-e4719fae9881", "user")
	require.True(t, ok)
	assert.Equal(t, "Interrupted", result.Content[0].Text)

	sys := s.eventsOf("system")
	assert.Equal(t, "turn interrupted", *sys[len(sys)-1].Content)
}

func TestFileChangeApproval(t *testing.T) {
	h := runFixture(t, "file_change_approval", "", nil)
	s := snapshot(h.rec)

	require.Len(t, s.opened, 1)
	req := s.opened[0]
	assert.Equal(t, "Edit calc.py", req.Title)
	assert.Equal(t, "apply_patch", req.ToolName)
	assert.Contains(t, req.Detail, "--- /work/scratch/calc.py\n+++ /work/scratch/calc.py\n@@ -2 +2,5 @@")
	assert.Equal(t, []string{"accept", "acceptForSession", "decline", "cancel"}, optionIDs(req))

	msg, _ := h.rec.Message("01a143b4-cbda-7d31-b424-9c0a9fcb3517", "assistant")
	require.Equal(t, "tool_use", msg.Content[0].Type)
	in := toolInput(t, msg.Content[0])
	assert.Equal(t, "/work/scratch/calc.py", in["file_path"])
	assert.Len(t, in["changes"], 1)
	result, _ := h.rec.Message("result-exec-7e3cdad8-a515-418e-b885-052d442aa0ff", "user")
	assert.Equal(t, "Success. Updated the following files:\nM /work/scratch/calc.py", result.Content[0].Text)
	assert.Equal(t, "completed", result.Content[0].Status)
}

func TestRequestUserInput(t *testing.T) {
	h := runFixture(t, "request_user_input", "", nil)
	s := snapshot(h.rec)

	require.Len(t, s.opened, 1)
	req := s.opened[0]
	assert.Equal(t, native.RequestKindQuestion, req.Kind)
	assert.Equal(t, "Which language should the tiny greeting CLI use?", req.Title)
	require.Len(t, req.Questions, 1)
	q := req.Questions[0]
	assert.Equal(t, "language", q.ID)
	assert.Equal(t, "Language", q.Header)
	assert.True(t, q.FreeText, "isOther allows a free answer")
	assert.False(t, q.Secret)
	assert.Equal(t, []native.QuestionOption{
		{Label: "Python (Recommended)", Description: "A small script with minimal setup."},
		{Label: "Node.js", Description: "A lightweight JavaScript command-line program."},
		{Label: "Go", Description: "A compiled single-binary CLI."},
	}, q.Options)

	msg, _ := h.rec.Message("01a143b5-063a-7d72-bcc5-4ce3bf922343", "assistant")
	assert.Equal(t, []string{"text", "thinking", "tool_use", "text"}, blockTypes(msg))
	tool := msg.Content[2]
	assert.Equal(t, "request_user_input", tool.ToolName)
	assert.Equal(t, "call_9b32be42d0894afb80f88514aa3ab908", tool.ToolUseID)
	assert.Equal(t, "completed", tool.Status)
	assert.JSONEq(t, `{"questions":[{"header":"Language","id":"language","question":"Which language should the tiny greeting CLI use?","options":[{"label":"Python (Recommended)","description":"A small script with minimal setup."},{"label":"Node.js","description":"A lightweight JavaScript command-line program."},{"label":"Go","description":"A compiled single-binary CLI."}]}]}`,
		string(tool.ToolInput))
	result, _ := h.rec.Message("result-call_9b32be42d0894afb80f88514aa3ab908", "user")
	assert.JSONEq(t, `{"answers":{"language":{"answers":["Python (Recommended)"]}}}`, result.Content[0].Text)
}

func TestInterrupt(t *testing.T) {
	h := runFixture(t, "interrupt", "", nil)
	s := snapshot(h.rec)
	require.NoError(t, h.err("turn/interrupt"))
	assert.Equal(t, []native.TurnResult{{StopReason: "interrupted"}}, s.completions)

	msg, _ := h.rec.Message("01a143b5-3da9-7610-b832-6e7ea0ff8616", "assistant")
	assert.Equal(t, "interrupted", msg.StopReason)
	require.Equal(t, []string{"text"}, blockTypes(msg))
	assert.NotEmpty(t, msg.Content[0].Text, "text streamed before the interrupt is kept")
	assert.JSONEq(t, `{"threadId":"01a143b5-3d33-7091-a463-bfe97d47405c","turnId":"01a143b5-3da9-7610-b832-6e7ea0ff8616"}`,
		string(h.sentParams("turn/interrupt")))

	require.ErrorIs(t, h.a.Interrupt(context.Background()), native.ErrNoActiveTurn)
}

func TestSteer(t *testing.T) {
	h := runFixture(t, "steer", "", nil, func(c *Config) { c.SnapshotInterval = time.Hour })
	s := snapshot(h.rec)
	require.NoError(t, h.err("turn/steer"))
	assert.ErrorIs(t, h.err("turn/steer-idle"), native.ErrNoActiveTurn)
	assert.JSONEq(t, `{"threadId":"01a143b5-599f-76b1-9545-d3965ad4119d","expectedTurnId":"01a143b5-5a1e-7e53-bdc6-8093d2690a68",
		"input":[{"type":"text","text":"Change of plan: stop now and reply only with the word STEERED."}]}`,
		string(h.sentParams("turn/steer")))

	assert.Equal(t, 1, s.started)
	assert.Equal(t, []native.TurnResult{{StopReason: "end_turn"}}, s.completions)

	first, ok := h.rec.Message("01a143b5-5a1e-7e53-bdc6-8093d2690a68", "assistant")
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(first.Content[0].Text, "one\ntwo\nthree"))
	assert.True(t, strings.HasSuffix(first.Content[0].Text, "one hundred twenty"))
	assert.Empty(t, first.StopReason)
	// The answer to the steer is a message of its own, after the steering
	// message.
	second, ok := h.rec.Message("01a143b5-5a1e-7e53-bdc6-8093d2690a68-1", "assistant")
	require.True(t, ok)
	assert.Equal(t, "STEERED", second.Content[0].Text)
	assert.Equal(t, "end_turn", second.StopReason)
	assert.False(t, second.Timestamp.Before(first.Timestamp))
	steered, ok := h.rec.Message("01a143b5-8045-7d02-8a68-0e3cddd49a60", "user")
	require.True(t, ok)
	assert.Equal(t, "Change of plan: stop now and reply only with the word STEERED.", steered.Content[0].Text)

	// With an hour between snapshots, 445 deltas send the message twice:
	// when it started streaming and when it completed.
	assert.LessOrEqual(t, len(h.rec.Snapshots(first.MessageID)), 3)
}

func TestResume(t *testing.T) {
	first := runFixture(t, "resume_first", "", nil)
	ref := snapshot(first.rec).refs
	require.Equal(t, []string{"01a143b5-be16-7d02-ac40-7722c0e95c6e"}, ref)

	h := runFixture(t, "resume_second", ref[0], nil)
	s := snapshot(h.rec)
	assert.JSONEq(t, `{"threadId":"01a143b5-be16-7d02-ac40-7722c0e95c6e","excludeTurns":true,"cwd":"/work/scratch","approvalPolicy":"never","sandbox":"danger-full-access"}`,
		string(h.sentParams("thread/resume")))
	assert.Equal(t, ref, s.refs)
	assert.Equal(t, 1, s.started, "usage of the old turn doesn't start a turn")
	assert.Equal(t, "session resumed: /work/scratch (codex 0.160.0)", *s.eventsOf("system")[0].Content)
	msg, _ := h.rec.Message("01a143b5-d620-7a02-9e77-5110cc64e42e", "assistant")
	assert.Equal(t, "PELICAN-42", msg.Content[0].Text)
}

func TestResumeMissingThreadStartsNewOne(t *testing.T) {
	h := newHarness(t, "00000000-0000-0000-0000-000000000000", nil)
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	h.answerAccount()
	resume := h.expectSent("thread/resume")
	h.feedJSON(`{"id":` + string(resume.ID) + `,"error":{"code":-32600,"message":"no rollout found for thread id 00000000-0000-0000-0000-000000000000"}}`)
	start := h.expectSent("thread/start")
	h.feedJSON(`{"id":` + string(start.ID) + `,"result":{"thread":{"id":"01a143b5-be16-7d02-ac40-7722c0e95c6e","cwd":"/work/scratch","cliVersion":"0.160.0","createdAt":1792137346},"model":"gpt-6-luna","cwd":"/work/scratch"}}`)
	h.settle()
	require.NoError(t, h.err("start"))
	s := snapshot(h.rec)
	assert.Equal(t, []string{"01a143b5-be16-7d02-ac40-7722c0e95c6e"}, s.refs)
	require.Len(t, s.errors, 1)
	assert.Contains(t, s.errors[0], "warning: Could not resume Codex thread 00000000-0000-0000-0000-000000000000")
}

func TestImageInput(t *testing.T) {
	h := runFixture(t, "image_input", "", nil)
	assert.JSONEq(t, `{"threadId":"01a143b6-1880-7bc2-95e4-d5d823576483","summary":"detailed","input":[
		{"type":"text","text":"What single color fills this image? Answer in one word."},
		{"type":"localImage","path":"/work/red16.png"}]}`, string(h.sentParams("turn/start")))
	msg, _ := h.rec.Message("01a143b6-19a6-74c1-925e-0a3d76641000", "assistant")
	assert.Equal(t, "Red", msg.Content[0].Text)

	assert.Equal(t, []userInput{{Type: "text", Text: "see"}, {Type: "localImage", Path: "/work/shot.png"}},
		inputItems(native.Input{Text: "see", Images: []native.Image{{Path: "/work/shot.png"}}}))
}

func TestMCPToolApproval(t *testing.T) {
	h := runFixture(t, "mcp_tool_on_request", "", nil)
	s := snapshot(h.rec)
	require.Len(t, s.opened, 1)
	req := s.opened[0]
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, `Allow the echo MCP server to run tool "echo_upper"?`, req.Title)
	assert.Equal(t, "mcp__echo__echo_upper", req.ToolName)
	assert.Equal(t, "exec-957169c5-e7a0-4f5f-a702-9ea0f167f8da", req.ToolUseID)
	assert.JSONEq(t, `{"text":"hello mcp"}`, string(req.Input))
	assert.Equal(t, []string{"accept", "decline", "cancel"}, optionIDs(req))

	msg, _ := h.rec.Message("01a143b7-17b9-7580-aca3-804c2e4ada05", "assistant")
	assert.Equal(t, []string{"tool_use", "text"}, blockTypes(msg))
	result, _ := h.rec.Message("result-exec-957169c5-e7a0-4f5f-a702-9ea0f167f8da", "user")
	assert.Equal(t, "HELLO MCP", result.Content[0].Text)
}

// fixtureUntil returns the lines up to and including the first one that
// contains marker.
func fixtureUntil(t *testing.T, name, marker string) []fixtureLine {
	lines := loadFixture(t, name)
	for i, l := range lines {
		if strings.Contains(string(l.Msg), marker) {
			return lines[:i+1]
		}
	}
	t.Fatalf("%s has no %q", name, marker)
	return nil
}

func TestProcessExitWithPendingRequest(t *testing.T) {
	h := newHarness(t, "", nil)
	h.replay(fixtureUntil(t, "command_approval_unanswered", "item/commandExecution/requestApproval"))
	id := h.requestID(json.RawMessage("0"))
	h.eventually(func() bool { _, ok := h.rec.Request(id); return ok }, "the approval request")
	h.settle()

	h.proc.exit(errors.New("signal: killed"))
	require.EqualError(t, h.a.Wait(), "signal: killed")
	s := snapshot(h.rec)
	assert.Empty(t, s.completions, "the conversation ends the turn when Wait returns")
	assert.Empty(t, s.closed, "the conversation expires the request when Wait returns")
	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{OptionID: "accept"}), native.ErrRequestNotFound)
	require.Error(t, h.a.StartTurn(context.Background(), native.Input{Text: "hi"}))

	msg, _ := h.rec.Message("01a143b4-9c0b-7021-a76f-53bf2628f666", "assistant")
	assert.Equal(t, "error", msg.StopReason)
	assert.Equal(t, "failed", msg.Content[0].Status)
}

func TestCrashMidCommand(t *testing.T) {
	h := newHarness(t, "", nil)
	h.replay(loadFixture(t, "crash_mid_command"))
	h.settle()
	// The live output preview of the command.
	h.eventually(func() bool {
		r, ok := h.rec.Message("result-exec-2800aae4-7b1f-4893-a222-3547d8499613", "user")
		return ok && r.Content[0].Status == "running" && strings.HasPrefix(r.Content[0].Text, "tick 2\n")
	}, "the output preview")
	h.proc.exit(errors.New("signal: killed"))
	require.Error(t, h.a.Wait())
	r, _ := h.rec.Message("result-exec-2800aae4-7b1f-4893-a222-3547d8499613", "user")
	assert.Equal(t, "failed", r.Content[0].Status)
	assert.Equal(t, "The turn failed before the tool finished", r.Content[0].Text)
	msg, _ := h.rec.Message("01a143b9-d9b9-78d0-a3fd-2a1ed19aebe1", "assistant")
	assert.JSONEq(t, `{"cmd":"for i in 1 2 3 4 5 6; do echo tick $i; sleep 1; done","workdir":"/work/scratch"}`, string(msg.Content[0].ToolInput))
}
