package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/sessions"
	"github.com/coder/agentapi/lib/transcript"
)

// newSessionServer starts a native Claude Code server in cwd whose agent
// had the session "earlier" there.
func newSessionServer(t *testing.T) *nativeServer {
	t.Helper()
	return newSessionServerWith(t, nil)
}

// newSessionServerWith is newSessionServer with the adapters adjusted by
// configure.
func newSessionServerWith(t *testing.T, configure func(*nativetest.Adapter)) *nativeServer {
	t.Helper()
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-work-app")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "earlier.jsonl"), []byte(
		`{"type":"user","timestamp":"2026-10-17T14:10:12.000Z","sessionId":"earlier","message":{"role":"user","content":"Fix the bug"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-17T14:10:16.000Z","sessionId":"earlier","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"Fixed."}],"stop_reason":"end_turn"}}`+"\n"+
			`{"type":"custom-title","customTitle":"Bug hunt","sessionId":"earlier"}`+"\n"), 0o600))

	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		a.Ref = "current"
		if configure != nil {
			configure(a)
		}
	}}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		Native:         httpapi.NativeConfig{Factory: factory.New},
		CWD:            "/work/app",
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Stop(context.Background())
		cancel()
	})
	n := &nativeServer{t: t, url: ts.URL, factory: factory}
	// The agent started: a message sent now starts a turn.
	require.Eventually(t, func() bool {
		var status httpapi.StatusChangeBody
		n.do(http.MethodGet, "/status", nil, &status)
		return status.Status == httpapi.AgentStatusStable
	}, 5*time.Second, 5*time.Millisecond)
	return n
}

func TestNative_ListSessions(t *testing.T) {
	n := newSessionServer(t)
	var list struct {
		Sessions []sessions.SessionSummary `json:"sessions"`
		Current  string                    `json:"current"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/sessions", nil, &list))
	require.Len(t, list.Sessions, 1)
	assert.Equal(t, "earlier", list.Sessions[0].ID)
	assert.Equal(t, "Bug hunt", list.Sessions[0].Title)
	assert.Equal(t, "current", list.Current)
}

func TestNative_ResumeSession(t *testing.T) {
	n := newSessionServer(t)
	first := n.factory.Last()
	n.send("hello", "")
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/sessions/earlier/resume", map[string]any{}, nil), "a turn runs")
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	assert.Equal(t, http.StatusNotFound, n.do(http.MethodPost, "/sessions/missing/resume", map[string]any{}, nil))

	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/sessions/earlier/resume", map[string]any{}, nil))
	second := n.factory.Last()
	assert.NotSame(t, first, second)
	assert.Equal(t, "earlier", second.ResumeRef)
	assert.False(t, second.Fork)

	var msgs struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 2)
	assert.Equal(t, "Fix the bug", msgs.Messages[0].Content)
	assert.Equal(t, "Fixed.", msgs.Messages[1].Content)
	var rich struct {
		Messages []struct {
			MessageID string `json:"message_id"`
		} `json:"messages"`
	}
	n.do(http.MethodGet, "/rich-messages", nil, &rich)
	require.Len(t, rich.Messages, 1)
	assert.Equal(t, "history-msg_2", rich.Messages[0].MessageID)
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	assert.Equal(t, "Bug hunt", status.SessionName)

	// A fork of it.
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/sessions/earlier/resume", map[string]any{"fork": true}, nil))
	fork := n.factory.Last()
	assert.Equal(t, "earlier", fork.ResumeRef)
	assert.True(t, fork.Fork)
}

// A session can't be opened while the agent compacts its context either,
// and a refused switch leaves the conversation, the agent and the lifecycle
// as they were.
func TestNative_ResumeDuringCompactionChangesNothing(t *testing.T) {
	compacting, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	releaseCompaction := func() { releaseOnce.Do(func() { close(release) }) }
	n := newSessionServerWith(t, func(a *nativetest.Adapter) {
		a.Caps.Compact = true
		a.OnCompact = func(context.Context) error {
			compacting <- struct{}{}
			<-release
			return nil
		}
	})
	// Before the server stops, which waits for the compaction's request.
	t.Cleanup(releaseCompaction)
	first := n.factory.Last()
	n.send("hello", "")
	first.Sink().RichMessage(transcript.RichMessage{MessageID: "cur_1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: "current answer"}}})
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	epoch := n.epoch()

	compacted := make(chan int, 1)
	go func() { compacted <- n.do(http.MethodPost, "/compact", nil, nil) }()
	<-compacting
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/sessions/earlier/resume", map[string]any{}, nil))

	assert.Len(t, n.factory.Adapters(), 1, "no agent was started")
	assert.False(t, first.Closed(), "the agent goes on compacting")
	var rich struct {
		Messages []struct {
			MessageID string `json:"message_id"`
		} `json:"messages"`
	}
	n.do(http.MethodGet, "/rich-messages", nil, &rich)
	require.Len(t, rich.Messages, 1)
	assert.Equal(t, "cur_1", rich.Messages[0].MessageID)
	var msgs struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 2)
	assert.Equal(t, "hello", msgs.Messages[0].Content)
	assert.Equal(t, "current answer", msgs.Messages[1].Content)
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	assert.Equal(t, httpapi.LifecycleRunning, status.Lifecycle, "compacting, not restarting")
	assert.Equal(t, epoch, n.epoch())

	// Once the compaction is done, the session opens.
	releaseCompaction()
	assert.Equal(t, http.StatusOK, <-compacted)
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/sessions/earlier/resume", map[string]any{}, nil))
	assert.Equal(t, "earlier", n.factory.Last().ResumeRef)
	assert.True(t, first.Closed())
}

func TestNative_SessionsNeedAnAgentWithLogs(t *testing.T) {
	n := newNativeServer(t, nil)
	_ = n
	// The fake server runs Claude Code: its sessions can be listed (none
	// here). Other agents' can't, which the sessions tests cover.
	var list struct {
		Sessions []sessions.SessionSummary `json:"sessions"`
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/sessions", nil, &list))
	assert.Empty(t, list.Sessions)
}

func TestNative_TimelineWithSubagents(t *testing.T) {
	n := newSessionServer(t)
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	dir := filepath.Join(config, "projects", "-work-app", "current", "subagents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1.jsonl"), []byte(
		`{"isSidechain":true,"agentId":"a1","type":"user","timestamp":"2026-10-18T10:00:01.000Z","sessionId":"current","message":{"role":"user","content":"Find the bug"}}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1.meta.json"), []byte(`{"description":"Find","toolUseId":"toolu_1"}`), 0o600))

	type event struct {
		ID              int    `json:"id"`
		Kind            string `json:"kind"`
		SubagentID      string `json:"subagent_id"`
		ParentToolUseID string `json:"parent_tool_use_id"`
	}
	var plain, with struct {
		Events []event `json:"events"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/timeline", nil, &plain))
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/timeline?subagents=true", nil, &with))
	require.Len(t, with.Events, len(plain.Events)+2)
	sub := with.Events[len(plain.Events):]
	for _, e := range sub {
		assert.Equal(t, "a1", e.SubagentID)
		assert.Equal(t, "toolu_1", e.ParentToolUseID)
	}
	assert.Equal(t, "system", sub[0].Kind)
	assert.Greater(t, sub[0].ID, 0)
	assert.Equal(t, sub[0].ID+1, sub[1].ID)
}

func TestNative_Subagents(t *testing.T) {
	n := newSessionServer(t)
	a := n.factory.Last()
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-app", "current", "subagents")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1.jsonl"), []byte(
		`{"isSidechain":true,"agentId":"a1","type":"user","timestamp":"2026-10-18T10:00:01.000Z","sessionId":"current","message":{"role":"user","content":"List the files"}}`+"\n"+
			`{"isSidechain":true,"agentId":"a1","type":"assistant","timestamp":"2026-10-18T10:00:02.000Z","sessionId":"current","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_ls","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use"}}`+"\n"+
			`{"isSidechain":true,"agentId":"a1","type":"user","uuid":"u2","timestamp":"2026-10-18T10:00:03.000Z","sessionId":"current","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_ls","content":"main.go"}]}}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-a1.meta.json"), []byte(`{"description":"List files","agentType":"general-purpose","toolUseId":"toolu_agent"}`), 0o600))

	// The agent's Agent tool call, still without a result.
	n.send("delegate", "")
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "p1", Role: "assistant", Content: []transcript.RichContentBlock{
		{Type: "tool_use", ToolUseID: "toolu_agent", ToolName: "Agent"},
	}})
	type state struct {
		ID              string `json:"id"`
		Description     string `json:"description"`
		ParentToolUseID string `json:"parent_tool_use_id"`
		Status          string `json:"status"`
	}
	var list struct {
		Subagents []state `json:"subagents"`
	}
	require.Eventually(t, func() bool {
		n.do(http.MethodGet, "/subagents", nil, &list)
		return len(list.Subagents) == 1 && list.Subagents[0].Status == "running"
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "a1", list.Subagents[0].ID)
	assert.Equal(t, "List files", list.Subagents[0].Description)
	assert.Equal(t, "toolu_agent", list.Subagents[0].ParentToolUseID)

	var one struct {
		Subagent state                    `json:"subagent"`
		Prompt   string                   `json:"prompt"`
		Messages []transcript.RichMessage `json:"messages"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/subagents/a1", nil, &one))
	assert.Equal(t, "List the files", one.Prompt)
	require.Len(t, one.Messages, 2)
	assert.Equal(t, "subagent-a1-m1", one.Messages[0].MessageID)
	assert.Equal(t, "tool_result", one.Messages[1].Content[0].Type)

	// The Agent tool call's result: the subagent is done.
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "p2", Role: "user", Content: []transcript.RichContentBlock{
		{Type: "tool_result", ToolUseID: "toolu_agent", Text: "main.go"},
	}})
	require.Eventually(t, func() bool {
		n.do(http.MethodGet, "/subagents/a1", nil, &one)
		return one.Subagent.Status == "completed"
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, http.StatusNotFound, n.do(http.MethodGet, "/subagents/nope", nil, nil))
}

func TestNative_SessionCosts(t *testing.T) {
	n := newSessionServer(t)
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-work-app")
	call := func(session, at, id string, cacheRead, output int) string {
		return `{"type":"assistant","timestamp":"` + at + `","sessionId":"` + session + `","message":{"id":"` + id + `","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],` +
			`"usage":{"input_tokens":10,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) + `,"cache_creation_input_tokens":0,"output_tokens":` + strconv.Itoa(output) + `}}}`
	}
	// The session the agent has open ("current"), with two tasks.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "current.jsonl"), []byte(
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","sessionId":"current","message":{"role":"user","content":"First"}}`+"\n"+
			call("current", "2026-10-20T10:00:05.000Z", "msg_a", 100000, 1000)+"\n"+
			`{"type":"user","timestamp":"2026-10-20T11:00:00.000Z","sessionId":"current","message":{"role":"user","content":"Second"}}`+"\n"+
			call("current", "2026-10-20T11:00:05.000Z", "msg_b", 200000, 2000)+"\n"), 0o600))

	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	first := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 100000, Output: 1000})
	second := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 200000, Output: 2000})

	var list struct {
		Sessions []struct {
			ID   string               `json:"id"`
			Cost sessions.SessionCost `json:"cost"`
		} `json:"sessions"`
		TotalCost sessions.SessionCost `json:"total_cost"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/sessions", nil, &list))
	require.Len(t, list.Sessions, 2)
	for _, s := range list.Sessions {
		if s.ID == "current" {
			assert.InDelta(t, first+second, s.Cost.USD, 1e-9)
			assert.Equal(t, 2, s.Cost.Calls)
		} else {
			assert.Zero(t, s.Cost.USD, "a session without usage")
		}
	}
	assert.InDelta(t, first+second, list.TotalCost.USD, 1e-9)
	require.Len(t, list.TotalCost.Hourly, 2)

	var cost struct {
		AgentSessionID string               `json:"agent_session_id"`
		Cost           sessions.SessionCost `json:"cost"`
		Calls          []sessions.ModelCall `json:"calls"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/cost", nil, &cost))
	assert.Equal(t, "current", cost.AgentSessionID)
	require.Len(t, cost.Calls, 2)
	assert.InDelta(t, first, cost.Calls[0].USD, 1e-9)
	assert.True(t, cost.Calls[0].Priced)
	assert.InDelta(t, first+second, cost.Cost.USD, 1e-9)

	// A client with the first call gets the second only; with a digest
	// that isn't the first call's, all of them again.
	var all, since, stale struct {
		Calls      []sessions.ModelCall `json:"calls"`
		From       int                  `json:"from"`
		TotalCalls int                  `json:"total_calls"`
		Digest     string               `json:"digest"`
		Cost       sessions.SessionCost `json:"cost"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/cost", nil, &all))
	assert.Equal(t, 2, all.TotalCalls)
	assert.Equal(t, sessions.CallsDigest(all.Calls), all.Digest)
	firstDigest := sessions.CallsDigest(all.Calls[:1])
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/cost?from=1&digest="+firstDigest, nil, &since))
	assert.Equal(t, 1, since.From)
	require.Len(t, since.Calls, 1)
	assert.InDelta(t, second, since.Calls[0].USD, 1e-9)
	assert.InDelta(t, first+second, since.Cost.USD, 1e-9, "the totals are whole")
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/cost?from=1&digest=0", nil, &stale))
	assert.Equal(t, 0, stale.From)
	assert.Len(t, stale.Calls, 2)
}
