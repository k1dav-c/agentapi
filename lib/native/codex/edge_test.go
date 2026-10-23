package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/transcript"
)

const testThread = "01a143b5-0000-7000-8000-000000000001"

// boot starts the adapter on a new thread.
func (h *harness) boot() {
	h.t.Helper()
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	h.answerAccount()
	start := h.expectSent("thread/start")
	h.feedJSON(`{"id":` + string(start.ID) + `,"result":{"thread":{"id":"` + testThread + `","cwd":"/work","cliVersion":"0.160.0"},"model":"gpt-6-luna","cwd":"/work"}}`)
	h.settle()
	require.NoError(h.t, h.err("start"))
}

// beginTurn starts turn turnID.
func (h *harness) beginTurn(turnID string) {
	h.t.Helper()
	h.act("turn/start", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"` + turnID + `","items":[],"status":"inProgress"}}}`)
	h.feedJSON(`{"method":"turn/started","params":{"threadId":"` + testThread + `","turn":{"id":"` + turnID + `","items":[],"status":"inProgress"}}}`)
	h.sync()
	h.settle()
	require.NoError(h.t, h.err("turn/start"))
}

func (h *harness) completeTurn(turnID, status string, extra string) {
	h.t.Helper()
	h.feedJSON(`{"method":"turn/completed","params":{"threadId":"` + testThread + `","turn":{"id":"` + turnID + `","items":[],"status":"` + status + `"` + extra + `}}}`)
	h.sync()
}

// item feeds item/started or item/completed (event) for an item of turnID.
func (h *harness) item(event, turnID, item string) {
	h.t.Helper()
	h.feedJSON(`{"method":"item/` + event + `","params":{"threadId":"` + testThread + `","turnId":"` + turnID + `","item":` + item + `}}`)
}

func TestTurnStartWhileBusy(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.act("busy", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32600,"message":"a turn is already running"}}`)
	h.settle()
	require.ErrorIs(t, h.err("busy"), native.ErrTurnActive)

	h.beginTurn("turn-1")
	require.ErrorIs(t, h.a.StartTurn(context.Background(), native.Input{Text: "again"}), native.ErrTurnActive,
		"a second turn is refused without asking app-server")
	h.expectNothingSent()
}

func TestSteerRaceWithTurnEnd(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// The turn ends while the steer is on its way.
	h.act("steer", func(ctx context.Context) error { return h.a.Steer(ctx, native.Input{Text: "more"}) })
	sent := h.expectSent("turn/steer")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32600,"message":"no active turn to steer"}}`)
	h.settle()
	require.ErrorIs(t, h.err("steer"), native.ErrNoActiveTurn)
}

func TestFailedTurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-1","willRetry":true,"error":{"message":"Reconnecting... 1/5"}}}`)
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-1","willRetry":false,"error":{"message":"stream disconnected","additionalDetails":"HTTP 502"}}}`)
	h.completeTurn("turn-1", "failed", `,"error":null`)
	s := snapshot(h.rec)
	assert.Equal(t, []string{"warning: Reconnecting... 1/5"}, s.errors)
	assert.Equal(t, []native.TurnResult{{StopReason: "error", Error: "stream disconnected\nHTTP 502"}}, s.completions)
	sys := s.eventsOf("system")
	assert.Equal(t, "turn failed: stream disconnected\nHTTP 502", *sys[len(sys)-1].Content)
}

func TestTurnStoppedByAUsageLimit(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.completeTurn("turn-1", "failed", `,"error":{"message":"You've hit your usage limit. Try again at 3:05 PM.","codexErrorInfo":"usageLimitExceeded"}`)
	s := snapshot(h.rec)
	assert.Equal(t, []native.TurnResult{{StopReason: "limited", Error: "You've hit your usage limit. Try again at 3:05 PM."}}, s.completions)
}

func TestInterruptWithoutCompletion(t *testing.T) {
	h := newHarness(t, "", nil, func(c *Config) { c.InterruptTimeout = 50 * time.Millisecond })
	h.boot()
	h.beginTurn("turn-1")
	h.act("interrupt", func(ctx context.Context) error { return h.a.Interrupt(ctx) })
	sent := h.expectSent("turn/interrupt")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{}}`)
	h.settle()
	require.NoError(t, h.err("interrupt"))
	h.eventually(func() bool { return len(snapshot(h.rec).completions) == 1 }, "the turn to end")
	assert.Equal(t, "interrupted", snapshot(h.rec).completions[0].StopReason)
	// A turn/completed arriving late is ignored.
	h.completeTurn("turn-1", "interrupted", "")
	assert.Len(t, snapshot(h.rec).completions, 1)
}

func TestUnknownServerRequestIsRefused(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.feedJSON(`{"id":5,"method":"item/tool/call","params":{"threadId":"` + testThread + `","turnId":"t","callId":"c","tool":"x","arguments":{}}}`)
	sent := h.expectSent("")
	assert.JSONEq(t, `5`, string(sent.ID))
	require.NotNil(t, sent.Error)
	assert.Equal(t, -32601, sent.Error.Code)
	assert.Empty(t, snapshot(h.rec).opened)
}

func TestCommandApprovalOptions(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"method":"item/started","params":{"threadId":"` + testThread + `","turnId":"turn-1","item":{"type":"commandExecution","id":"exec-1","command":"/bin/bash -lc 'curl https://example.com'","cwd":"/work","commandActions":[],"status":"inProgress"}}}`)
	h.feedJSON(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"exec-1",
		"command":"/bin/bash -lc 'curl https://example.com'","cwd":"/work","reason":"needs network",
		"networkApprovalContext":{"host":"example.com","protocol":"https"},
		"proposedExecpolicyAmendment":["curl"],
		"proposedNetworkPolicyAmendments":[{"action":"allow","host":"example.com"}]}}`)
	h.sync()
	id := h.requestID(json.RawMessage("0"))
	req, ok := h.rec.Request(id)
	require.True(t, ok)
	assert.Equal(t, "curl https://example.com", req.Title, "the bash -lc wrapper is unwrapped")
	assert.Equal(t, "needs network\nNetwork access to example.com (https)\nWorking directory: /work", req.Detail)
	assert.Equal(t, []string{"accept", "acceptForSession", "acceptWithExecpolicyAmendment", "applyNetworkPolicyAmendment", "decline", "cancel"}, optionIDs(req))

	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{OptionID: "maybe"}), native.ErrInvalidResponse)
	require.ErrorIs(t, h.a.Respond(context.Background(), "nope", native.Response{OptionID: "accept"}), native.ErrRequestNotFound)
	h.act("respond", func(ctx context.Context) error {
		return h.a.Respond(ctx, id, native.Response{OptionID: "applyNetworkPolicyAmendment"})
	})
	sent := h.expectSent("")
	assert.JSONEq(t, `{"decision":{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"action":"allow","host":"example.com"}}}}`, string(sent.Result))
	h.settle()
	require.NoError(t, h.err("respond"))
	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{OptionID: "accept"}), native.ErrRequestNotFound,
		"a request is answered once")
}

func TestRequestWithdrawnByCodex(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":3,"method":"item/tool/requestUserInput","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_1","isBlocking":true,
		"questions":[{"id":"q","header":"Q","question":"Secret?","isOther":false,"isSecret":true,"options":null}]}}`)
	h.sync()
	id := h.requestID(json.RawMessage("3"))
	req, ok := h.rec.Request(id)
	require.True(t, ok)
	assert.Equal(t, []native.Question{{ID: "q", Header: "Q", Question: "Secret?", FreeText: true, Secret: true}}, req.Questions)
	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{}), native.ErrInvalidResponse)

	h.feedJSON(`{"method":"serverRequest/resolved","params":{"threadId":"` + testThread + `","requestId":3}}`)
	h.sync()
	assert.Equal(t, []string{id}, snapshot(h.rec).closed)
	result, ok := h.rec.Message("result-call_1", "user")
	require.True(t, ok)
	assert.Equal(t, "failed", result.Content[0].Status)
	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{Answers: map[string][]string{"q": {"x"}}}), native.ErrRequestNotFound)
}

func TestElicitationForm(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":1,"method":"mcpServer/elicitation/request","params":{"threadId":"` + testThread + `","turnId":"turn-1","serverName":"db","mode":"form",
		"message":"Connection details","requestedSchema":{"type":"object","properties":{
			"port":{"type":"integer","title":"Port"},
			"env":{"type":"string","oneOf":[{"const":"prod","title":"Production"},{"const":"dev","title":"Development"}]},
			"tls":{"type":"boolean","description":"Use TLS?"},
			"tags":{"type":"array","items":{"type":"string","enum":["a","b"]}}}}}}`)
	h.sync()
	id := h.requestID(json.RawMessage("1"))
	req, ok := h.rec.Request(id)
	require.True(t, ok)
	assert.Equal(t, native.RequestKindQuestion, req.Kind)
	assert.Equal(t, []string{"decline", "cancel"}, optionIDs(req))
	require.Len(t, req.Questions, 4)
	assert.Equal(t, "env", req.Questions[0].ID)
	assert.Equal(t, []native.QuestionOption{{Label: "Production"}, {Label: "Development"}}, req.Questions[0].Options)
	assert.True(t, req.Questions[1].FreeText, "port")
	assert.True(t, req.Questions[2].MultiSelect, "tags")

	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{Answers: map[string][]string{
		"env": {"Production"}, "port": {"many"}, "tls": {"Yes"}, "tags": {"a"},
	}}), native.ErrInvalidResponse)
	h.act("respond", func(ctx context.Context) error {
		return h.a.Respond(ctx, id, native.Response{Answers: map[string][]string{
			"env": {"Production"}, "port": {"5432"}, "tls": {"Yes"}, "tags": {"a", "b"},
		}})
	})
	sent := h.expectSent("")
	assert.JSONEq(t, `{"action":"accept","content":{"env":"prod","port":5432,"tls":true,"tags":["a","b"]}}`, string(sent.Result))
}

func TestPlanUpdates(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"method":"turn/plan/updated","params":{"threadId":"` + testThread + `","turnId":"turn-1","explanation":"Start","plan":[{"step":"a","status":"inProgress"}]}}`)
	h.feedJSON(`{"method":"turn/plan/updated","params":{"threadId":"` + testThread + `","turnId":"turn-1","plan":[{"step":"a","status":"completed"}]}}`)
	h.completeTurn("turn-1", "completed", "")
	msg, ok := h.rec.Message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, "update_plan", msg.Content[0].ToolName)
	assert.JSONEq(t, `{"plan":[{"step":"a","status":"completed"}]}`, string(msg.Content[0].ToolInput))
	result, _ := h.rec.Message("result-plan-turn-1", "user")
	assert.Equal(t, "Plan updated", result.Content[0].Text)
}

func TestTurnStartedByCodex(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	// No turn/start from AgentAPI: Codex started the turn itself.
	h.feedJSON(`{"method":"item/started","params":{"threadId":"` + testThread + `","turnId":"auto","item":{"type":"agentMessage","id":"m","text":"","phase":"final_answer"}}}`)
	h.feedJSON(`{"method":"item/completed","params":{"threadId":"` + testThread + `","turnId":"auto","item":{"type":"agentMessage","id":"m","text":"hi","phase":"final_answer"}}}`)
	h.completeTurn("auto", "completed", "")
	s := snapshot(h.rec)
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, s.log)
	// Items of another thread are not ours.
	h.feedJSON(`{"method":"item/started","params":{"threadId":"other","turnId":"t2","item":{"type":"agentMessage","id":"m2","text":"x"}}}`)
	h.sync()
	assert.Equal(t, 1, snapshot(h.rec).started)
}

func TestCloseEscalates(t *testing.T) {
	h := newHarness(t, "", nil, func(c *Config) { c.StopTimeout = 20 * time.Millisecond })
	h.proc.ignoreStdinClose = true
	h.proc.ignoreTerm = true
	h.boot()
	require.NoError(t, h.a.Close())
	require.Error(t, h.a.Wait())
	assert.Equal(t, int32(1), h.proc.terminated.Load())
	assert.Equal(t, int32(1), h.proc.killed.Load())
	require.NoError(t, h.a.Close(), "Close is idempotent")
}

func TestCloseBeforeStart(t *testing.T) {
	factory, err := NewFactory(Config{Program: "codex"})
	require.NoError(t, err)
	a, err := factory(native.Resume{})
	require.NoError(t, err)
	require.NoError(t, a.Close())
	require.NoError(t, a.Wait())
	require.ErrorIs(t, a.Start(context.Background(), newRecorder(t)), errClosed)
}

// A Close racing a failed Start used to close the adapter's channels twice.
func TestCloseDuringFailedStart(t *testing.T) {
	factory, err := NewFactory(Config{
		Program:      "codex",
		StartProcess: func(procutil.Command) (procutil.Process, error) { return nil, errors.New("no such program") },
	})
	require.NoError(t, err)
	for range 200 {
		adapter, err := factory(native.Resume{})
		require.NoError(t, err)
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			_ = adapter.Close()
		}()
		require.Error(t, adapter.Start(context.Background(), newRecorder(t)))
		<-closed
		require.Error(t, adapter.Start(context.Background(), newRecorder(t)), "a failed adapter doesn't start again")
		require.NoError(t, adapter.Wait())
	}
}

func TestInitializeErrorFailsStart(t *testing.T) {
	h := newHarness(t, "", nil)
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"error":{"code":-32600,"message":"unsupported client"}}`)
	h.settle()
	require.ErrorContains(t, h.err("start"), "initialize codex app-server: unsupported client")
	h.expectNothingSent()
}

// A thread/start result without a thread fails the start instead of
// leaving an adapter whose every turn fails.
func TestThreadWithoutID(t *testing.T) {
	h := newHarness(t, "", nil)
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	h.answerAccount()
	start := h.expectSent("thread/start")
	h.feedJSON(`{"id":` + string(start.ID) + `,"result":{"thread":{"cwd":"/work","cliVersion":"0.160.0"},"model":"gpt-6-luna","cwd":"/work"}}`)
	h.settle()
	require.ErrorIs(t, h.err("start"), errNoThread)
	require.ErrorIs(t, h.a.StartTurn(context.Background(), native.Input{Text: "hi"}), native.ErrNotStarted)
}

func TestIsBusyError(t *testing.T) {
	for message, want := range map[string]bool{
		"a turn is already running":                    true,
		"thread already has an active or pending turn": true,
		"turn already in progress":                     true,
		"thread is already archived":                   false,
		"model is busy":                                false,
		"no active turn to steer":                      false,
		"input must not be empty":                      false,
	} {
		assert.Equal(t, want, isBusyError(message), message)
	}
}

func TestTurnStartErrors(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	// The conversation retries a busy agent forever, so other errors must
	// not look busy.
	h.act("archived", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32600,"message":"thread is already archived"}}`)
	h.settle()
	require.ErrorContains(t, h.err("archived"), "thread is already archived")
	require.NotErrorIs(t, h.err("archived"), native.ErrTurnActive)

	// Codex started a turn of its own meanwhile.
	h.act("raced", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent = h.expectSent("turn/start")
	h.feedJSON(`{"method":"turn/started","params":{"threadId":"` + testThread + `","turn":{"id":"auto","items":[],"status":"inProgress"}}}`)
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32600,"message":"failed to start turn"}}`)
	h.settle()
	require.ErrorIs(t, h.err("raced"), native.ErrTurnActive)
}

// A failed interrupt leaves the turn as it was: when it completes, it was
// not interrupted.
func TestFailedInterrupt(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.act("interrupt", func(ctx context.Context) error { return h.a.Interrupt(ctx) })
	sent := h.expectSent("turn/interrupt")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32600,"message":"failed to interrupt turn"}}`)
	h.settle()
	require.ErrorContains(t, h.err("interrupt"), "failed to interrupt turn")
	h.completeTurn("turn-1", "completed", "")
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, snapshot(h.rec).completions)
	h.a.mu.Lock()
	defer h.a.mu.Unlock()
	assert.Empty(t, h.a.interrupting)
}

func TestServerRequestWithBadParams(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.feedJSON(`{"id":5,"method":"item/commandExecution/requestApproval","params":"not an object"}`)
	sent := h.expectSent("")
	assert.JSONEq(t, `5`, string(sent.ID))
	require.NotNil(t, sent.Error)
	assert.Equal(t, -32602, sent.Error.Code)
	h.feedJSON(`{"id":6,"method":"item/tool/requestUserInput","params":{"threadId":"` + testThread + `","turnId":"t","itemId":"c","questions":[]}}`)
	sent = h.expectSent("")
	assert.JSONEq(t, `6`, string(sent.ID))
	require.NotNil(t, sent.Error)
	assert.Equal(t, -32602, sent.Error.Code)
	assert.Empty(t, snapshot(h.rec).opened)
}

// An approval shows the command that runs; Codex's reading of it, which
// can leave parts out, goes into the detail.
func TestCommandApprovalShowsTheCommand(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":0,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"exec-1",
		"command":"/bin/bash -lc 'cd src && rg -n TODO | head -5'","cwd":"/work",
		"commandActions":[{"type":"search","command":"rg -n TODO","query":"TODO","path":"src"}]}}`)
	h.feedJSON(`{"id":1,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"exec-2",
		"command":"git -c core.pager=cat log","cwd":"/work","commandActions":[{"type":"unknown","command":"git -c core.pager=cat log"}]}}`)
	h.sync()

	req, ok := h.rec.Request(h.requestID(json.RawMessage("0")))
	require.True(t, ok)
	assert.Equal(t, "cd src && rg -n TODO | head -5", req.Title)
	assert.JSONEq(t, `{"cmd":"cd src && rg -n TODO | head -5","workdir":"/work"}`, string(req.Input))
	assert.Equal(t, "Searches for \"TODO\" in src\nWorking directory: /work", req.Detail)

	req, ok = h.rec.Request(h.requestID(json.RawMessage("1")))
	require.True(t, ok)
	assert.Equal(t, "git -c core.pager=cat log", req.Title, "not a shell wrapper")
	assert.Equal(t, "Working directory: /work", req.Detail)
}

// A long command (a script in a heredoc) would fill the chat's request
// card; the title is its first line, the input has all of it.
func TestLongCommandApprovalTitle(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	script := "node <<'JS'\n" + strings.Repeat("assert.equal(element('score').textContent, '010');", 40) + "\nJS"
	params, err := json.Marshal(map[string]any{
		"threadId": testThread, "turnId": "turn-1", "itemId": "exec-1", "command": script, "cwd": "/work",
	})
	require.NoError(t, err)
	h.feedJSON(`{"id":0,"method":"item/commandExecution/requestApproval","params":` + string(params) + `}`)
	h.sync()

	req, ok := h.rec.Request(h.requestID(json.RawMessage("0")))
	require.True(t, ok)
	assert.Equal(t, "node <<'JS' …", req.Title)
	var input struct {
		Cmd string `json:"cmd"`
	}
	require.NoError(t, json.Unmarshal(req.Input, &input))
	assert.Equal(t, script, input.Cmd)
}

func TestPermissionsRequest(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":2,"method":"item/permissions/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_p","cwd":"/work",
		"reason":"needs the network","permissions":{"network":{"enabled":true}}}}`)
	h.sync()
	id := h.requestID(json.RawMessage("2"))
	req, ok := h.rec.Request(id)
	require.True(t, ok)
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, "Grant Codex additional permissions", req.Title)
	assert.Equal(t, "needs the network\nRequested: {\"network\":{\"enabled\":true}}", req.Detail)
	assert.Equal(t, "call_p", req.ToolUseID)
	assert.JSONEq(t, `{"network":{"enabled":true}}`, string(req.Input))
	assert.Equal(t, []string{"turn", "session", "decline"}, optionIDs(req))

	h.act("respond", func(ctx context.Context) error { return h.a.Respond(ctx, id, native.Response{OptionID: "session"}) })
	sent := h.expectSent("")
	assert.JSONEq(t, `{"permissions":{"network":{"enabled":true}},"scope":"session"}`, string(sent.Result))
	h.settle()
	require.NoError(t, h.err("respond"))
}

func TestURLElicitation(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.feedJSON(`{"id":1,"method":"mcpServer/elicitation/request","params":{"threadId":"` + testThread + `","turnId":null,"serverName":"github","mode":"url",
		"elicitationId":"e1","message":"Authorize GitHub","url":"https://github.com/login/device"}}`)
	h.sync()
	id := h.requestID(json.RawMessage("1"))
	req, ok := h.rec.Request(id)
	require.True(t, ok)
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.Equal(t, "Authorize GitHub", req.Title)
	assert.Equal(t, "Open https://github.com/login/device", req.Detail)
	assert.Equal(t, []string{"accept", "decline", "cancel"}, optionIDs(req))

	h.act("respond", func(ctx context.Context) error { return h.a.Respond(ctx, id, native.Response{OptionID: "accept"}) })
	sent := h.expectSent("")
	assert.JSONEq(t, `{"action":"accept"}`, string(sent.Result))
	h.settle()
	require.NoError(t, h.err("respond"))
}

// Every option a request offers is one Respond accepts, and the other way
// round.
func TestOfferedOptionsAnswerTheRequest(t *testing.T) {
	proc := newFakeProc()
	go func() { _, _ = io.Copy(io.Discard, proc.stdinR) }()
	a := newAdapter(Config{Logger: slog.New(slog.DiscardHandler)}, launchOptions{}, "")
	a.proc, a.sink = proc, newRecorder(t)
	t.Cleanup(func() { proc.exit(nil) })
	for i, tc := range []struct{ method, params string }{
		{methodCommandApproval, `{"turnId":"t","itemId":"i","command":"make","proposedExecpolicyAmendment":["make"],
			"proposedNetworkPolicyAmendments":[{"action":"allow","host":"a.example"},{"action":"deny","host":"b.example"}]}`},
		{methodCommandApproval, `{"turnId":"t","itemId":"i","command":"make","availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["make"]}},"cancel"]}`},
		{methodFileChangeApproval, `{"turnId":"t","itemId":"i","grantRoot":"/work"}`},
		{methodPermissions, `{"turnId":"t","itemId":"i","permissions":{"network":{"enabled":true}}}`},
		{methodElicitation, `{"serverName":"github","mode":"url","message":"Authorize","url":"https://github.com/login/device"}`},
		{methodElicitation, `{"serverName":"echo","mode":"form","message":"Run?","requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call"}}`},
		{methodElicitation, `{"serverName":"db","mode":"form","message":"Pick","requestedSchema":{"type":"object","properties":{"env":{"type":"string","enum":["dev","prod"]}}}}`},
	} {
		a.loop.serverRequest(rpcMessage{ID: json.RawMessage(strconv.Itoa(i)), Method: tc.method, Params: json.RawMessage(tc.params)})
	}
	opened := snapshot(a.sink.(*recorder)).opened
	require.Len(t, opened, 7)
	for _, req := range opened {
		r := a.requests[req.ID]
		require.NotNil(t, r)
		require.NotEmpty(t, req.Options, req.Title)
		for _, opt := range req.Options {
			_, err := r.result(native.Response{OptionID: opt.ID})
			assert.NoError(t, err, "%s: %s", req.Title, opt.ID)
		}
		assert.Len(t, r.results, len(req.Options), "%s: every result is offered", req.Title)
	}
}

// Answers to secret questions reach Codex, but not the tool result the chat
// shows and --state-db stores.
func TestSecretAnswerIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":3,"method":"item/tool/requestUserInput","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_1","isBlocking":true,"questions":[
		{"id":"user","header":"User","question":"User name?","isOther":true,"isSecret":false,"options":null},
		{"id":"token","header":"Token","question":"Paste the token","isOther":true,"isSecret":true,"options":null}]}}`)
	h.sync()
	id := h.requestID(json.RawMessage("3"))
	h.act("respond", func(ctx context.Context) error {
		return h.a.Respond(ctx, id, native.Response{Answers: map[string][]string{"user": {"ada"}, "token": {"hunter2"}}})
	})
	sent := h.expectSent("")
	assert.JSONEq(t, `{"answers":{"user":{"answers":["ada"]},"token":{"answers":["hunter2"]}}}`, string(sent.Result))
	h.settle()
	require.NoError(t, h.err("respond"))
	h.feedJSON(`{"method":"serverRequest/resolved","params":{"threadId":"` + testThread + `","requestId":3}}`)
	h.sync()

	result, ok := h.rec.Message("result-call_1", "user")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.JSONEq(t, `{"answers":{"user":{"answers":["ada"]},"token":{"answers":["[redacted]"]}}}`, result.Content[0].Text)
	shown, err := json.Marshal([]any{h.rec.State().Rich, h.rec.State().Events})
	require.NoError(t, err)
	assert.NotContains(t, string(shown), "hunter2")
}

// The answer to request_user_input ends its tool call once app-server has
// it, however Respond's context ends and however soon the turn ends.
func TestAnsweredQuestionThenTurnEnd(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":4,"method":"item/tool/requestUserInput","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_1","isBlocking":true,
		"questions":[{"id":"q","header":"Q","question":"Go on?","isOther":true,"isSecret":false,"options":null}]}}`)
	h.sync()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, h.a.Respond(ctx, h.requestID(json.RawMessage("4")), native.Response{Answers: map[string][]string{"q": {"yes"}}}))
	h.expectSent("")
	h.feedJSON(`{"method":"serverRequest/resolved","params":{"threadId":"` + testThread + `","requestId":4}}`)
	h.completeTurn("turn-1", "completed", "")

	result, ok := h.rec.Message("result-call_1", "user")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.JSONEq(t, `{"answers":{"q":{"answers":["yes"]}}}`, result.Content[0].Text)
	msg, _ := h.rec.Message("turn-1", "assistant")
	assert.Equal(t, "completed", msg.Content[0].Status)
	assert.Empty(t, snapshot(h.rec).closed, "an answered request is not withdrawn")
}

// Once the loop ended, inLoop fails instead of queueing a task nobody runs.
func TestInLoopAfterExit(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.proc.exit(nil)
	require.NoError(t, h.a.Wait())
	for range 32 {
		require.ErrorIs(t, h.a.inLoop(context.Background(), func() { t.Error("a task ran after the loop ended") }), errExited)
	}
}

// The conversation closes every open request when a turn ends; those not
// answered are dismissed, so neither app-server nor an MCP server keeps
// waiting for them.
func TestOpenRequestsDismissedAtTurnEnd(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// An MCP elicitation outside the turn, a permission request of the
	// turn, and an approval answered before it ended.
	h.feedJSON(`{"id":7,"method":"mcpServer/elicitation/request","params":{"threadId":"` + testThread + `","turnId":null,"serverName":"db","mode":"form",
		"message":"Pick an environment","requestedSchema":{"type":"object","properties":{"env":{"type":"string","enum":["dev","prod"]}}}}}`)
	h.feedJSON(`{"id":8,"method":"item/permissions/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_p","cwd":"/work",
		"reason":null,"permissions":{"network":{"enabled":true}}}}`)
	h.feedJSON(`{"id":9,"method":"item/commandExecution/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"exec-1","command":"make","cwd":"/work"}}`)
	h.sync()
	require.NoError(t, h.a.Respond(context.Background(), h.requestID(json.RawMessage("9")), native.Response{OptionID: "accept"}))
	h.expectSent("")

	h.completeTurn("turn-1", "interrupted", "")
	dismissed := map[string]string{}
	for range 2 {
		sent := h.expectSent("")
		dismissed[string(sent.ID)] = string(sent.Result)
	}
	assert.JSONEq(t, `{"action":"cancel"}`, dismissed["7"])
	assert.JSONEq(t, `{"permissions":{},"scope":"turn"}`, dismissed["8"])
	h.expectNothingSent()
	require.ErrorIs(t, h.a.Respond(context.Background(), h.requestID(json.RawMessage("7")), native.Response{OptionID: "decline"}),
		native.ErrRequestNotFound)
}

// A snapshot that went out early (the message completed) leaves nothing to
// wake up for.
func TestNoWakeUpForASentSnapshot(t *testing.T) {
	clock := quartz.NewMock(t)
	h := newHarness(t, "", nil, func(c *Config) { c.Clock = clock })
	h.boot()
	h.beginTurn("turn-1")
	h.item("started", "turn-1", `{"type":"agentMessage","id":"m","text":""}`)
	for _, delta := range []string{"Hel", "lo"} {
		h.feedJSON(`{"method":"item/agentMessage/delta","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"m","delta":"` + delta + `"}}`)
	}
	h.sync()
	h.eventually(func() bool { _, ok := clock.Peek(); return ok }, "the second delta to wait for the snapshot interval")
	h.item("completed", "turn-1", `{"type":"agentMessage","id":"m","text":"Hello"}`)
	h.sync()
	h.eventually(func() bool { _, ok := clock.Peek(); return !ok }, "the wake-up to be dropped")
	msg, _ := h.rec.Message("turn-1", "assistant")
	assert.Equal(t, "Hello", msg.Content[0].Text)
}

// The stop reason goes on the turn's last message with content, not on the
// empty one a steer started.
func TestStopReasonAfterSteer(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.item("completed", "turn-1", `{"type":"userMessage","id":"u1","content":[{"type":"text","text":"go"}]}`)
	h.item("completed", "turn-1", `{"type":"agentMessage","id":"m1","text":"Working on it"}`)
	h.item("started", "turn-1", `{"type":"userMessage","id":"u2","content":[{"type":"text","text":"stop"}]}`)
	h.completeTurn("turn-1", "interrupted", "")

	msg, ok := h.rec.Message("turn-1", "assistant")
	require.True(t, ok)
	assert.Equal(t, native.StopReasonInterrupted, msg.StopReason)
	_, ok = h.rec.Message("turn-1-1", "assistant")
	assert.False(t, ok, "the steer's message has no content")
}

func TestEndedTurnsAreForgotten(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	turns := endedTurns + 4
	for i := range turns {
		id := fmt.Sprintf("turn-%d", i)
		h.beginTurn(id)
		h.completeTurn(id, "completed", "")
	}
	assert.Len(t, h.a.loop.ended, endedTurns)
	// A late item of the last turn doesn't start it again.
	h.item("completed", fmt.Sprintf("turn-%d", turns-1), `{"type":"agentMessage","id":"late","text":"late"}`)
	h.sync()
	assert.Equal(t, turns, snapshot(h.rec).started)
}

// An error app-server doesn't retry after is shown even when the turn goes
// on, and once when it fails the turn.
func TestErrorsOfATurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-1","willRetry":false,"error":{"message":"MCP server db timed out"}}}`)
	h.sync()
	assert.Empty(t, snapshot(h.rec).errors, "shown when the turn ends")
	h.completeTurn("turn-1", "completed", "")
	s := snapshot(h.rec)
	assert.Equal(t, []string{"error: MCP server db timed out"}, s.errors)
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, s.completions)

	h.beginTurn("turn-2")
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-2","willRetry":false,"error":{"message":"tool output too large"}}}`)
	h.completeTurn("turn-2", "failed", `,"error":{"message":"stream disconnected"}`)
	s = snapshot(h.rec)
	assert.Equal(t, []string{"error: MCP server db timed out", "error: tool output too large"}, s.errors)
	assert.Equal(t, "stream disconnected", s.completions[1].Error)
}

func TestOtherToolItems(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	require.NoError(t, os.WriteFile(shot, png, 0o600))
	missing := filepath.Join(dir, "missing.png")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	for _, item := range []string{
		`{"type":"dynamicToolCall","id":"dyn-1","namespace":"browser","tool":"screenshot","arguments":{"url":"https://go.dev"},"status":"completed","success":true,
			"contentItems":[{"type":"inputText","text":"Took a screenshot"},{"type":"inputImage","imageUrl":"` + dataURL + `"}]}`,
		`{"type":"dynamicToolCall","id":"dyn-2","namespace":null,"tool":"lookup","arguments":null,"status":"completed","success":false,"contentItems":[{"type":"inputText","text":"not found"}]}`,
		`{"type":"webSearch","id":"ws-1","query":"quartz clock","action":{"type":"search","query":"quartz clock"}}`,
		`{"type":"imageView","id":"iv-1","path":` + string(mustJSON(shot)) + `}`,
		`{"type":"imageView","id":"iv-2","path":` + string(mustJSON(missing)) + `}`,
	} {
		h.item("completed", "turn-1", item)
	}
	h.completeTurn("turn-1", "completed", "")

	msg, ok := h.rec.Message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 5)
	img := transcript.RichImage{MediaType: "image/png", Data: png}
	for i, want := range []struct {
		name, input string
		result      transcript.RichContentBlock
	}{
		{"browser__screenshot", `{"url":"https://go.dev"}`, transcript.RichContentBlock{Text: "Took a screenshot", Status: "completed", Images: []transcript.RichImage{img}}},
		{"lookup", `{}`, transcript.RichContentBlock{Text: "not found", Status: "failed"}},
		{"web_search", `{"query":"quartz clock","action":{"type":"search","query":"quartz clock"}}`, transcript.RichContentBlock{Text: "Searched the web: quartz clock", Status: "completed"}},
		{"view_image", `{"path":` + string(mustJSON(shot)) + `}`, transcript.RichContentBlock{Status: "completed", Images: []transcript.RichImage{img}}},
		{"view_image", `{"path":` + string(mustJSON(missing)) + `}`, transcript.RichContentBlock{Text: "Could not read " + missing, Status: "failed"}},
	} {
		tool := msg.Content[i]
		assert.Equal(t, want.name, tool.ToolName)
		assert.JSONEq(t, want.input, string(tool.ToolInput), want.name)
		assert.Equal(t, want.result.Status, tool.Status, want.name)
		result, ok := h.rec.Message("result-"+tool.ToolUseID, "user")
		require.True(t, ok, want.name)
		got := result.Content[0]
		assert.Equal(t, want.result.Text, got.Text, want.name)
		assert.Equal(t, want.result.Status, got.Status, want.name)
		assert.Equal(t, want.result.Status == "failed", *got.IsError, want.name)
		assert.Equal(t, want.result.Images, got.Images, want.name)
	}
}

// A write app-server doesn't read blocks while it holds the write lock;
// Close doesn't wait for it, and still stops app-server.
func TestCloseWithBlockedWrite(t *testing.T) {
	proc := newFakeProc() // nothing reads its stdin
	proc.ignoreTerm = true
	factory, err := NewFactory(Config{
		Program:      "codex",
		Logger:       slog.New(slog.DiscardHandler),
		StopTimeout:  20 * time.Millisecond,
		StartProcess: func(procutil.Command) (procutil.Process, error) { return proc, nil },
	})
	require.NoError(t, err)
	adapter, err := factory(native.Resume{})
	require.NoError(t, err)
	a := adapter.(*Adapter)
	started := make(chan error, 1)
	go func() { started <- a.Start(context.Background(), newRecorder(t)) }()
	// Start blocks writing initialize.
	require.Eventually(t, func() bool {
		return a.writer.Writing()
	}, waitTimeout, time.Millisecond)

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = a.Close()
	}()
	select {
	case <-closed:
	case <-time.After(waitTimeout):
		t.Fatal("Close waited for the blocked write")
	}
	require.ErrorContains(t, <-started, "write to codex app-server")
	assert.Equal(t, int32(1), proc.terminated.Load())
	assert.Equal(t, int32(1), proc.killed.Load())
	require.EqualError(t, a.Wait(), "signal: killed")
}

// slowExit is app-server taking a while to exit after SIGKILL.
type slowExit struct{ *fakeProc }

func (p slowExit) Kill() error {
	p.killed.Add(1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		p.exit(errors.New("signal: killed"))
	}()
	return nil
}

// Close returns once app-server has exited, not when SIGKILL is sent: a
// Reload resumes the thread at once, which the old process must not hold.
func TestCloseWaitsForTheExit(t *testing.T) {
	h := newHarness(t, "", nil, func(c *Config) {
		c.StopTimeout = 10 * time.Millisecond
		launch := c.StartProcess
		c.StartProcess = func(cmd procutil.Command) (procutil.Process, error) {
			p, err := launch(cmd)
			return slowExit{p.(*fakeProc)}, err
		}
	})
	h.boot()
	h.proc.ignoreStdinClose = true
	h.proc.ignoreTerm = true
	require.NoError(t, h.a.Close())
	select {
	case <-h.proc.done:
	default:
		t.Fatal("Close returned before app-server exited")
	}
	assert.Equal(t, int32(1), h.proc.killed.Load())
}

// Close interrupts the turn app-server works on and waits for it to end
// before it closes stdin: closing it mid-turn leaves the thread with a
// turn that never finished.
func TestCloseInterruptsTheTurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.a.closeGrace = waitTimeout
	closed := make(chan error, 1)
	go func() { closed <- h.a.Close() }()

	sent := h.expectSent("turn/interrupt")
	assert.JSONEq(t, `{"threadId":"`+testThread+`","turnId":"turn-1"}`, string(sent.Params))
	select {
	case <-h.proc.done:
		t.Fatal("stdin closed before the turn ended")
	case <-closed:
		t.Fatal("Close returned before the turn ended")
	case <-time.After(50 * time.Millisecond):
	}
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{}}`)
	h.completeTurn("turn-1", "interrupted", "")
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(waitTimeout):
		t.Fatal("Close didn't go on once the turn ended")
	}
	assert.Equal(t, "turn_completed:interrupted", snapshot(h.rec).log[len(snapshot(h.rec).log)-1])
}

// An app-server that doesn't end its turn is closed after the grace.
func TestCloseGivesUpOnTheTurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.a.closeGrace = 30 * time.Millisecond
	start := time.Now()
	require.NoError(t, h.a.Close())
	h.expectSent("turn/interrupt")
	assert.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond)
	require.NoError(t, h.a.Wait())
}

// Without a turn, Close interrupts nothing.
func TestCloseWithoutATurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.a.closeGrace = waitTimeout
	require.NoError(t, h.a.Close())
	h.expectNothingSent()
}

func TestCollabAgentToolCalls(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// As codex app-server 0.163.0 reports a spawned worker and the wait.
	for _, item := range []string{
		`{"type":"collabAgentToolCall","id":"call-spawn","tool":"spawnAgent","status":"completed","senderThreadId":"` + testThread + `",
			"receiverThreadIds":["sub-1"],"prompt":null,"model":"gpt-6.1-sol","agentsStates":{"sub-1":{"status":"pendingInit","message":null}}}`,
		`{"type":"collabAgentToolCall","id":"call-wait","tool":"wait","status":"completed","senderThreadId":"` + testThread + `",
			"receiverThreadIds":["sub-1"],"agentsStates":{"sub-1":{"status":"completed","message":"README.md"}}}`,
	} {
		h.item("completed", "turn-1", item)
	}
	h.completeTurn("turn-1", "completed", "")

	msg, ok := h.rec.Message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 2)
	assert.Equal(t, "spawn_agent", msg.Content[0].ToolName)
	assert.JSONEq(t, `{"receiver_thread_ids":["sub-1"],"model":"gpt-6.1-sol"}`, string(msg.Content[0].ToolInput))
	assert.Equal(t, "wait_agent", msg.Content[1].ToolName)
	result, ok := h.rec.Message("result-call-wait", "user")
	require.True(t, ok)
	assert.Equal(t, "sub-1: completed: README.md", result.Content[0].Text)
	assert.Equal(t, "completed", result.Content[0].Status)
}

func TestCollabToolName(t *testing.T) {
	t.Parallel()
	for tool, want := range map[string]string{"spawnAgent": "spawn_agent", "wait": "wait_agent", "sendInput": "send_input", "closeAgent": "close_agent", "": "agent"} {
		assert.Equal(t, want, collabToolName(tool), tool)
	}
}

func TestSubagentActivity(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// As codex app-server 0.163.0 reports a worker spawned with multi-agent
	// v2: a start, under the spawn call's id, and a completion.
	h.item("completed", "turn-1", `{"type":"subAgentActivity","id":"call-spawn","kind":"started","agentThreadId":"sub-1","agentPath":"/root/list_files","model":"gpt-6.1-sol"}`)
	// The turn goes on, so its messages are sent on the snapshot timer.
	h.eventually(func() bool {
		_, call := h.rec.Message("turn-1", "assistant")
		_, result := h.rec.Message("result-call-spawn", "user")
		return call && result
	}, "the spawn call and its result")
	msg, ok := h.rec.Message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, "spawn_agent", msg.Content[0].ToolName)
	assert.JSONEq(t, `{"receiver_thread_ids":["sub-1"],"agent_path":"/root/list_files","model":"gpt-6.1-sol"}`, string(msg.Content[0].ToolInput))
	result, ok := h.rec.Message("result-call-spawn", "user")
	require.True(t, ok)
	assert.Equal(t, "running", result.Content[0].Status, "the subagent goes on")

	h.item("completed", "turn-1", `{"type":"subAgentActivity","id":"subagent-completed-x","kind":"completed","agentThreadId":"sub-1","agentPath":"/root/list_files"}`)
	h.completeTurn("turn-1", "completed", "")
	result, ok = h.rec.Message("result-call-spawn", "user")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.Equal(t, "Finished /root/list_files", result.Content[0].Text)
	msg, _ = h.rec.Message("turn-1", "assistant")
	assert.Len(t, msg.Content, 1, "the completion is not a tool call of its own")
}

// A turn/start app-server got but didn't answer may run all the same: the
// error says so, whether the call timed out or was cancelled, so that the
// message isn't sent again.
func TestTurnStartWithoutAnswerIsUnconfirmed(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.a.StartTurn(ctx, native.Input{Text: "go"}) }()
	h.expectSent("turn/start")
	cancel()
	err := <-done
	require.ErrorIs(t, err, native.ErrUnconfirmed)
	assert.ErrorIs(t, err, context.Canceled)

	// A request that never went out is no such case.
	h.proc.exit(nil)
	h.eventually(h.a.loopEnded, "the loop to end")
	assert.NotErrorIs(t, h.a.StartTurn(context.Background(), native.Input{Text: "go"}), native.ErrUnconfirmed)
}
