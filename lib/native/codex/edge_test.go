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
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
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
	s := h.rec.snapshot()
	assert.Equal(t, []string{"warning: Reconnecting... 1/5"}, s.errors)
	assert.Equal(t, []native.TurnResult{{StopReason: "error", Error: "stream disconnected\nHTTP 502"}}, s.completions)
	sys := s.eventsOf("system")
	assert.Equal(t, "turn failed: stream disconnected\nHTTP 502", *sys[len(sys)-1].Content)
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
	h.eventually(func() bool { return len(h.rec.snapshot().completions) == 1 }, "the turn to end")
	assert.Equal(t, "interrupted", h.rec.snapshot().completions[0].StopReason)
	// A turn/completed arriving late is ignored.
	h.completeTurn("turn-1", "interrupted", "")
	assert.Len(t, h.rec.snapshot().completions, 1)
}

func TestUnknownServerRequestIsRefused(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.feedJSON(`{"id":5,"method":"item/tool/call","params":{"threadId":"` + testThread + `","turnId":"t","callId":"c","tool":"x","arguments":{}}}`)
	sent := h.expectSent("")
	assert.JSONEq(t, `5`, string(sent.ID))
	require.NotNil(t, sent.Error)
	assert.Equal(t, -32601, sent.Error.Code)
	assert.Empty(t, h.rec.snapshot().opened)
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
	req, ok := h.rec.request(id)
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
	req, ok := h.rec.request(id)
	require.True(t, ok)
	assert.Equal(t, []native.Question{{ID: "q", Header: "Q", Question: "Secret?", FreeText: true, Secret: true}}, req.Questions)
	require.ErrorIs(t, h.a.Respond(context.Background(), id, native.Response{}), native.ErrInvalidResponse)

	h.feedJSON(`{"method":"serverRequest/resolved","params":{"threadId":"` + testThread + `","requestId":3}}`)
	h.sync()
	assert.Equal(t, []string{id}, h.rec.snapshot().closed)
	result, ok := h.rec.message("result-call_1", "user")
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
	req, ok := h.rec.request(id)
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
	msg, ok := h.rec.message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, "update_plan", msg.Content[0].ToolName)
	assert.JSONEq(t, `{"plan":[{"step":"a","status":"completed"}]}`, string(msg.Content[0].ToolInput))
	result, _ := h.rec.message("result-plan-turn-1", "user")
	assert.Equal(t, "Plan updated", result.Content[0].Text)
}

func TestTurnStartedByCodex(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	// No turn/start from AgentAPI: Codex started the turn itself.
	h.feedJSON(`{"method":"item/started","params":{"threadId":"` + testThread + `","turnId":"auto","item":{"type":"agentMessage","id":"m","text":"","phase":"final_answer"}}}`)
	h.feedJSON(`{"method":"item/completed","params":{"threadId":"` + testThread + `","turnId":"auto","item":{"type":"agentMessage","id":"m","text":"hi","phase":"final_answer"}}}`)
	h.completeTurn("auto", "completed", "")
	s := h.rec.snapshot()
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn"}, s.log)
	// Items of another thread are not ours.
	h.feedJSON(`{"method":"item/started","params":{"threadId":"other","turnId":"t2","item":{"type":"agentMessage","id":"m2","text":"x"}}}`)
	h.sync()
	assert.Equal(t, 1, h.rec.snapshot().started)
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
	a, err := factory("")
	require.NoError(t, err)
	require.NoError(t, a.Close())
	require.NoError(t, a.Wait())
	require.Error(t, a.Start(context.Background(), newRecorder(t)))
}

// A Close racing a failed Start used to close the adapter's channels twice.
func TestCloseDuringFailedStart(t *testing.T) {
	factory, err := NewFactory(Config{
		Program: "codex",
		Launch:  func(procutil.Command) (procutil.Process, error) { return nil, errors.New("no such program") },
	})
	require.NoError(t, err)
	for range 200 {
		adapter, err := factory("")
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
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, h.rec.snapshot().completions)
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
	assert.Empty(t, h.rec.snapshot().opened)
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

	req, ok := h.rec.request(h.requestID(json.RawMessage("0")))
	require.True(t, ok)
	assert.Equal(t, "cd src && rg -n TODO | head -5", req.Title)
	assert.JSONEq(t, `{"cmd":"cd src && rg -n TODO | head -5","workdir":"/work"}`, string(req.Input))
	assert.Equal(t, "Searches for \"TODO\" in src\nWorking directory: /work", req.Detail)

	req, ok = h.rec.request(h.requestID(json.RawMessage("1")))
	require.True(t, ok)
	assert.Equal(t, "git -c core.pager=cat log", req.Title, "not a shell wrapper")
	assert.Equal(t, "Working directory: /work", req.Detail)
}

func TestPermissionsRequest(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"id":2,"method":"item/permissions/requestApproval","params":{"threadId":"` + testThread + `","turnId":"turn-1","itemId":"call_p","cwd":"/work",
		"reason":"needs the network","permissions":{"network":{"enabled":true}}}}`)
	h.sync()
	id := h.requestID(json.RawMessage("2"))
	req, ok := h.rec.request(id)
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
	req, ok := h.rec.request(id)
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
	opened := a.sink.(*recorder).snapshot().opened
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

	result, ok := h.rec.message("result-call_1", "user")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.JSONEq(t, `{"answers":{"user":{"answers":["ada"]},"token":{"answers":["[redacted]"]}}}`, result.Content[0].Text)
	h.rec.mu.Lock()
	shown, err := json.Marshal([]any{h.rec.snapshots, h.rec.events})
	h.rec.mu.Unlock()
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

	result, ok := h.rec.message("result-call_1", "user")
	require.True(t, ok)
	assert.Equal(t, "completed", result.Content[0].Status)
	assert.JSONEq(t, `{"answers":{"q":{"answers":["yes"]}}}`, result.Content[0].Text)
	msg, _ := h.rec.message("turn-1", "assistant")
	assert.Equal(t, "completed", msg.Content[0].Status)
	assert.Empty(t, h.rec.snapshot().closed, "an answered request is not withdrawn")
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
	msg, _ := h.rec.message("turn-1", "assistant")
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

	msg, ok := h.rec.message("turn-1", "assistant")
	require.True(t, ok)
	assert.Equal(t, native.StopReasonInterrupted, msg.StopReason)
	_, ok = h.rec.message("turn-1-1", "assistant")
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
	assert.Equal(t, turns, h.rec.snapshot().started)
}

// An error app-server doesn't retry after is shown even when the turn goes
// on, and once when it fails the turn.
func TestErrorsOfATurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-1","willRetry":false,"error":{"message":"MCP server db timed out"}}}`)
	h.sync()
	assert.Empty(t, h.rec.snapshot().errors, "shown when the turn ends")
	h.completeTurn("turn-1", "completed", "")
	s := h.rec.snapshot()
	assert.Equal(t, []string{"error: MCP server db timed out"}, s.errors)
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, s.completions)

	h.beginTurn("turn-2")
	h.feedJSON(`{"method":"error","params":{"threadId":"` + testThread + `","turnId":"turn-2","willRetry":false,"error":{"message":"tool output too large"}}}`)
	h.completeTurn("turn-2", "failed", `,"error":{"message":"stream disconnected"}`)
	s = h.rec.snapshot()
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

	msg, ok := h.rec.message("turn-1", "assistant")
	require.True(t, ok)
	require.Len(t, msg.Content, 5)
	img := jsonlwatcher.RichImage{MediaType: "image/png", Data: png}
	for i, want := range []struct {
		name, input string
		result      jsonlwatcher.RichContentBlock
	}{
		{"browser__screenshot", `{"url":"https://go.dev"}`, jsonlwatcher.RichContentBlock{Text: "Took a screenshot", Status: "completed", Images: []jsonlwatcher.RichImage{img}}},
		{"lookup", `{}`, jsonlwatcher.RichContentBlock{Text: "not found", Status: "failed"}},
		{"web_search", `{"query":"quartz clock","action":{"type":"search","query":"quartz clock"}}`, jsonlwatcher.RichContentBlock{Text: "Searched the web: quartz clock", Status: "completed"}},
		{"view_image", `{"path":` + string(mustJSON(shot)) + `}`, jsonlwatcher.RichContentBlock{Status: "completed", Images: []jsonlwatcher.RichImage{img}}},
		{"view_image", `{"path":` + string(mustJSON(missing)) + `}`, jsonlwatcher.RichContentBlock{Text: "Could not read " + missing, Status: "failed"}},
	} {
		tool := msg.Content[i]
		assert.Equal(t, want.name, tool.ToolName)
		assert.JSONEq(t, want.input, string(tool.ToolInput), want.name)
		assert.Equal(t, want.result.Status, tool.Status, want.name)
		result, ok := h.rec.message("result-"+tool.ToolUseID, "user")
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
		Program:     "codex",
		Logger:      slog.New(slog.DiscardHandler),
		StopTimeout: 20 * time.Millisecond,
		Launch:      func(procutil.Command) (procutil.Process, error) { return proc, nil },
	})
	require.NoError(t, err)
	adapter, err := factory("")
	require.NoError(t, err)
	a := adapter.(*Adapter)
	started := make(chan error, 1)
	go func() { started <- a.Start(context.Background(), newRecorder(t)) }()
	// Start blocks writing initialize.
	require.Eventually(t, func() bool {
		if a.writeMu.TryLock() {
			a.writeMu.Unlock()
			return false
		}
		return true
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
