package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

// newSessionServer starts a native Claude Code server in cwd whose agent
// had the session "earlier" there.
func newSessionServer(t *testing.T) *nativeServer {
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
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) { a.Ref = "current" }}
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
	require.Eventually(t, func() bool { return factory.Last() != nil && factory.Last().Sink() != nil }, 5*time.Second, 5*time.Millisecond)
	return n
}

func TestNative_ListSessions(t *testing.T) {
	n := newSessionServer(t)
	var list struct {
		Sessions []jsonlwatcher.SessionSummary `json:"sessions"`
		Current  string                        `json:"current"`
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

func TestNative_SessionsNeedAnAgentWithLogs(t *testing.T) {
	n := newNativeServer(t, nil)
	_ = n
	// The fake server runs Claude Code: its sessions can be listed (none
	// here). Other agents' can't, which the jsonlwatcher tests cover.
	var list struct {
		Sessions []jsonlwatcher.SessionSummary `json:"sessions"`
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/sessions", nil, &list))
	assert.Empty(t, list.Sessions)
}

