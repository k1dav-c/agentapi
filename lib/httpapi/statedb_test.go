package httpapi_test

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
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
	"github.com/coder/agentapi/lib/statedb"
)

// persistentServer runs a native server over the state database at path.
func persistentServer(t *testing.T, path, cwd string) (*nativeServer, *httpapi.Server, *statedb.DB, error) {
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
	}}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		NativeFactory:  factory.New,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		CWD:            cwd,
		StateDB:        db,
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
	a.Sink().RichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "thinking", Thinking: "hmm"}, {Type: "text", Text: "Hi there"}}})
	a.Sink().SessionName("Greeting")
	a.Sink().SessionEvents([]jsonlwatcher.SessionEvent{{Kind: "text"}})
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
		Messages []jsonlwatcher.RichMessage `json:"messages"`
	}
	n.do(http.MethodGet, "/rich-messages", nil, &rich)
	require.Len(t, rich.Messages, 1)
	assert.Equal(t, "hmm", rich.Messages[0].Content[0].Thinking)

	var timeline struct {
		Events []jsonlwatcher.SessionEvent `json:"events"`
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
	a.Sink().RichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: "reply"}}})
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
