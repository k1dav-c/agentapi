package codex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

const testThread = "01a143b5-0000-7000-8000-000000000001"

// boot starts the adapter on a new thread.
func (h *harness) boot() {
	h.t.Helper()
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
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
	closeGrace, termGrace = 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { closeGrace, termGrace = 3*time.Second, 2*time.Second })
	h := newHarness(t, "", nil)
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

func TestLaunchProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	script := filepath.Join(t.TempDir(), "fake-codex")
	require.NoError(t, os.WriteFile(script, []byte("#!"+sh+"\n"+
		`read line; echo '{"id":1,"result":{"userAgent":"x"}}'; printf '\033[31mERROR\033[0m boom\n' >&2; exit 3`+"\n"), 0o755))
	proc, err := launchProcess(LaunchSpec{Program: script, Args: []string{"app-server"}, Env: os.Environ()})
	require.NoError(t, err)
	_, err = proc.Stdin().Write([]byte("{}\n"))
	require.NoError(t, err)
	out := make([]byte, 64)
	n, _ := proc.Stdout().Read(out)
	assert.True(t, strings.HasPrefix(string(out[:n]), `{"id":1`))
	err = proc.Wait()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 3: ERROR boom")
}
