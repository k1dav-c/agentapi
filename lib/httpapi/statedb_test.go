package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/transcript"
)

// persistentServer runs a native server over the state database at path.
func persistentServer(t *testing.T, path, cwd string) (*nativeServer, *httpapi.Server, *statedb.DB, error) {
	t.Helper()
	return persistentServerWith(t, path, cwd, nil)
}

// persistentServerWith is persistentServer with configure adjusting each
// new adapter.
func persistentServerWith(t *testing.T, path, cwd string, configure func(*nativetest.Adapter)) (*nativeServer, *httpapi.Server, *statedb.DB, error) {
	t.Helper()
	db, err := statedb.Open(path, nil)
	require.NoError(t, err)
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sessions := 0
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			sessions++
			a.Ref = "session-" + strconv.Itoa(sessions)
		}
		if configure != nil {
			configure(a)
		}
	}}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		Native:         httpapi.NativeConfig{Factory: factory.New, StateDB: db},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            cwd,
		UploadDir:      path + ".uploads",
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	n := &nativeServer{t: t, url: ts.URL, factory: factory}
	require.Eventually(t, func() bool {
		var status httpapi.StatusChangeBody
		n.do(http.MethodGet, "/status", nil, &status)
		return status.Capabilities != nil
	}, 5*time.Second, 5*time.Millisecond)
	return n, srv, db, nil
}

func stop(t *testing.T, srv *httpapi.Server, db *statedb.DB) {
	t.Helper()
	require.NoError(t, srv.Stop(context.Background()))
	require.NoError(t, db.Close())
}

func TestStateDB_RestoresConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	n, srv, db, err := persistentServer(t, path, "/work")
	require.NoError(t, err)
	a := n.factory.Last()
	assert.Empty(t, a.ResumeRef)

	n.send("hello", "")
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "thinking", Thinking: "hmm"}, {Type: "text", Text: "Hi there"}}})
	a.Sink().SessionName("Greeting")
	a.Sink().SessionEvents([]transcript.SessionEvent{{Kind: "text"}})
	a.Sink().RequestOpened(native.Request{ID: "perm", Kind: native.RequestKindApproval, Title: "Run ls", Options: []native.RequestOption{{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow}}})
	_, queued := n.send("next task", "")
	require.True(t, queued.Queued)

	var before struct {
		Epoch string `json:"epoch"`
	}
	n.do(http.MethodGet, "/status", nil, &before)
	require.NotEmpty(t, before.Epoch)
	stop(t, srv, db)

	// Another directory's session is refused.
	_, _, _, err = persistentServer(t, path, "/elsewhere")
	require.ErrorContains(t, err, "belongs to another session")

	n, srv, db, err = persistentServer(t, path, "/work")
	require.NoError(t, err)
	defer stop(t, srv, db)
	a = n.factory.Last()
	assert.Equal(t, "session-1", a.ResumeRef, "the agent's session is resumed")

	var status struct {
		Epoch       string `json:"epoch"`
		SessionName string `json:"session_name"`
	}
	n.do(http.MethodGet, "/status", nil, &status)
	assert.Equal(t, before.Epoch, status.Epoch, "browser caches stay valid")
	assert.Equal(t, "Greeting", status.SessionName)

	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 2)
	assert.Equal(t, "hello", msgs.Messages[0].Content)
	assert.Equal(t, "Hi there", msgs.Messages[1].Content)

	var rich struct {
		Messages []transcript.RichMessage `json:"messages"`
	}
	n.do(http.MethodGet, "/rich-messages", nil, &rich)
	require.Len(t, rich.Messages, 1)
	assert.Equal(t, "hmm", rich.Messages[0].Content[0].Thinking)

	var timeline struct {
		Events []transcript.SessionEvent `json:"events"`
	}
	n.do(http.MethodGet, "/timeline", nil, &timeline)
	assert.Len(t, timeline.Events, 1)

	// The request's agent is gone.
	var reqs struct {
		Requests []native.RequestState `json:"requests"`
	}
	n.do(http.MethodGet, "/requests", nil, &reqs)
	require.Len(t, reqs.Requests, 1)
	assert.Equal(t, native.RequestStatusExpired, reqs.Requests[0].Status)
	assert.Equal(t, native.ResponseNotResumable, reqs.Requests[0].Capability)
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/requests/perm/respond", map[string]any{"option_id": "allow"}, nil), "it is no longer pending")

	// The queued message survived and is sent to the resumed agent.
	require.Eventually(t, func() bool { return len(a.Turns()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "next task", a.Turns()[0].Text)
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 3)
	assert.Equal(t, 2, msgs.Messages[2].Id, "message ids continue")
}

func TestStateDB_RestartStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	n, srv, db, err := persistentServer(t, path, "/work")
	require.NoError(t, err)
	n.send("hello", "")
	n.factory.Last().Sink().RequestOpened(approval("perm"))
	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/messages", nil, nil))
	stop(t, srv, db)

	n, srv, db, err = persistentServer(t, path, "/work")
	require.NoError(t, err)
	defer stop(t, srv, db)
	assert.Equal(t, "session-2", n.factory.Last().ResumeRef, "the session started by the restart is resumed")
	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	assert.Empty(t, msgs.Messages)
	assert.Empty(t, n.requests(), "the old session's request was not stored again")
}

// Restarting the agent to apply an MCP change resumes its session: the
// conversation stays, also across an AgentAPI restart.
func TestStateDB_MCPRestartResumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	n, srv, db, err := persistentServer(t, path, dir)
	require.NoError(t, err)
	a := n.factory.Last()
	n.send("hello", "")
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: "Hi"}}})
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	epoch := n.epoch()

	var res struct {
		Restarted bool `json:"restarted"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodPut, "/mcp?restart=true", map[string]any{"servers": map[string]any{}}, &res))
	require.True(t, res.Restarted)
	assert.Equal(t, "session-1", n.factory.Last().ResumeRef)
	assert.Equal(t, epoch, n.epoch(), "clients keep the conversation")
	assertMessages(t, n, 2)
	stop(t, srv, db)

	n, srv, db, err = persistentServer(t, path, dir)
	require.NoError(t, err)
	defer stop(t, srv, db)
	assert.Equal(t, "session-1", n.factory.Last().ResumeRef)
	assertMessages(t, n, 2)
}

func assertMessages(t *testing.T, n *nativeServer, count int) {
	t.Helper()
	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	assert.Len(t, msgs.Messages, count)
}

// After a crash the changes of the last moments may be missing from the
// file. Clients that saw them must not resume their cache, so the restored
// conversation has a new epoch.
func TestStateDB_CrashStartsNewEpoch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	n, srv, db, err := persistentServer(t, path, "/work")
	require.NoError(t, err)
	a := n.factory.Last()
	n.send("hello", "")
	db.Flush()
	// Not written yet when the crash happens.
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: "Hi"}}})
	epoch := n.epoch()
	crashed := filepath.Join(dir, "crashed.db")
	copyFiles(t, path, crashed)
	stop(t, srv, db)

	n, srv, db, err = persistentServer(t, crashed, "/work")
	require.NoError(t, err)
	assert.NotEqual(t, epoch, n.epoch())
	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 1, "what was written is restored")
	epoch = n.epoch()
	stop(t, srv, db)

	// A clean stop keeps the epoch.
	n, srv, db, err = persistentServer(t, crashed, "/work")
	require.NoError(t, err)
	defer stop(t, srv, db)
	assert.Equal(t, epoch, n.epoch())
}

// copyFiles copies a state database as it is on disk, like a crash would
// leave it.
func copyFiles(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(from + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(to+suffix, data, 0o600))
	}
}

// slowCloseAdapter holds Close until release is closed, like an agent that
// takes a while to stop.
type slowCloseAdapter struct {
	*nativetest.Adapter
	closing chan struct{}
	release chan struct{}
}

func (a *slowCloseAdapter) Close() error {
	close(a.closing)
	<-a.release
	return a.Adapter.Close()
}

// Stop stops the HTTP server before the agent, which can take seconds to
// stop, and stores the queue last: a message accepted while the agent
// stops would otherwise be answered as queued and never stored.
func TestStateDB_StopStoresTheQueueLast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := statedb.Open(path, nil)
	require.NoError(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	closing, release := make(chan struct{}), make(chan struct{})
	factory := &nativetest.Factory{}
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType: msgfmt.AgentTypeClaude,
		Transport: httpapi.TransportNative,
		Native: httpapi.NativeConfig{
			Factory: func(ref string) (native.Adapter, error) {
				a, err := factory.New(ref)
				if err != nil {
					return nil, err
				}
				return &slowCloseAdapter{Adapter: a.(*nativetest.Adapter), closing: closing, release: release}, nil
			},
			StateDB: db,
		},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            "/work",
		UploadDir:      path + ".uploads",
		Port:           port,
		Bind:           "127.0.0.1",
	})
	require.NoError(t, err)
	go func() { _ = srv.Start() }()
	n := &nativeServer{t: t, url: fmt.Sprintf("http://127.0.0.1:%d", port), factory: factory}
	require.Eventually(t, func() bool {
		resp, err := http.Get(n.url + "/status")
		if err != nil {
			return false
		}
		var status httpapi.StatusChangeBody
		err = json.NewDecoder(resp.Body).Decode(&status)
		_ = resp.Body.Close()
		return err == nil && status.Capabilities != nil
	}, 5*time.Second, 10*time.Millisecond)
	n.send("first", "")
	_, res := n.send("second", "")
	require.True(t, res.Queued)

	stopped := make(chan error, 1)
	go func() { stopped <- srv.Stop(context.Background()) }()
	<-closing
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Post(n.url+"/message", "application/json", strings.NewReader(`{"content":"late","type":"user"}`))
	if err == nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err, "no message is accepted while the agent stops")
	close(release)
	require.NoError(t, <-stopped)
	require.NoError(t, db.Close())

	db, err = statedb.Open(path, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	require.Len(t, snap.Queue, 1)
	assert.Equal(t, "second", snap.Queue[0].Content)
}

// readEvents reads SSE events from /events until n events with data were
// read, returning their ids ("" if none).
func readEvents(t *testing.T, url string, header http.Header, n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var ids []string
	id := ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() && len(ids) < n {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			ids = append(ids, id)
			id = ""
		}
	}
	return ids
}

func TestEvents_LastEventID(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("one", "")
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "m1", Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: "reply"}}})
	var status struct {
		Epoch string `json:"epoch"`
	}
	n.do(http.MethodGet, "/status", nil, &status)

	// session_sync, 2 messages, status, rich message: only the last carries
	// the replay's seq.
	ids := readEvents(t, n.url+"/events?sync=1", nil, 5)
	require.Len(t, ids, 5)
	assert.Equal(t, []string{"", "", "", "", "3"}, ids)

	// Resuming from that id replays nothing but session_sync and status.
	header := http.Header{"Last-Event-ID": {"3"}}
	ids = readEvents(t, n.url+"/events?sync=1&epoch="+status.Epoch, header, 2)
	require.Len(t, ids, 2)
	assert.Equal(t, "3", ids[1], "the replay end repeats the client's position")
}

func TestStateDB_RestoresSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	n, srv, db, err := persistentServerWith(t, path, "/work", configurableAdapter)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, n.do(http.MethodPut, "/settings", map[string]any{"permission_mode": "ask", "model": "plain"}, nil))
	// A new conversation keeps the settings.
	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/messages", nil, nil))
	require.Eventually(t, func() bool { return len(n.factory.Adapters()) == 2 }, 5*time.Second, 5*time.Millisecond)
	stop(t, srv, db)

	n, srv, db, err = persistentServerWith(t, path, "/work", configurableAdapter)
	require.NoError(t, err)
	defer stop(t, srv, db)
	assert.Equal(t, []native.Settings{{Model: "plain"}, {PermissionMode: native.PermissionModeAsk}}, n.factory.Last().Applied())
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	require.NotNil(t, status.Settings)
	// The model has no efforts.
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "plain"}, *status.Settings)
}
