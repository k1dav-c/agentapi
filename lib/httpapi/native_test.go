package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

type nativeServer struct {
	t       *testing.T
	url     string
	factory *nativetest.Factory
}

func newNativeServer(t *testing.T, configure func(*nativetest.Adapter)) *nativeServer {
	t.Helper()
	ctx, cancel := context.WithCancel(logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil))))
	factory := &nativetest.Factory{Configure: configure}
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		Transport:      httpapi.TransportNative,
		NativeFactory:  factory.New,
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
	require.Eventually(t, func() bool {
		var status httpapi.StatusChangeBody
		n.do(http.MethodGet, "/status", nil, &status)
		return status.Capabilities != nil
	}, 5*time.Second, 5*time.Millisecond)
	return n
}

// do sends a request and decodes the JSON response into out. It returns the
// status code.
func (n *nativeServer) do(method, path string, body any, out any) int {
	n.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(n.t, err)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, n.url+path, reader)
	require.NoError(n.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(n.t, err)
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < 300 {
		require.NoError(n.t, json.NewDecoder(resp.Body).Decode(out))
	}
	return resp.StatusCode
}

type messageResult struct {
	Ok      bool `json:"ok"`
	Queued  bool `json:"queued"`
	Steered bool `json:"steered"`
}

func (n *nativeServer) send(content string, mode httpapi.DispatchMode) (int, messageResult) {
	var res messageResult
	body := map[string]any{"content": content, "type": "user"}
	if mode != "" {
		body["dispatch_mode"] = mode
	}
	code := n.do(http.MethodPost, "/message", body, &res)
	return code, res
}

func (n *nativeServer) queue() []httpapi.QueuedMessage {
	var q struct {
		Messages []httpapi.QueuedMessage `json:"messages"`
	}
	n.do(http.MethodGet, "/queue", nil, &q)
	return q.Messages
}

func TestNative_StatusAndMessages(t *testing.T) {
	n := newNativeServer(t, nil)
	var status struct {
		Transport    httpapi.Transport    `json:"transport"`
		Status       httpapi.AgentStatus  `json:"status"`
		Capabilities *native.Capabilities `json:"capabilities"`
	}
	n.do(http.MethodGet, "/status", nil, &status)
	assert.Equal(t, httpapi.TransportNative, status.Transport)
	assert.Equal(t, httpapi.AgentStatusStable, status.Status)
	assert.Equal(t, &native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true}, status.Capabilities)

	code, res := n.send("hello", "")
	require.Equal(t, http.StatusOK, code)
	assert.True(t, res.Ok)
	assert.False(t, res.Queued)
	a := n.factory.Last()
	require.Len(t, a.Turns(), 1)

	a.Sink().RichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: "Hi"}}})
	a.Sink().RichMessage(jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant", Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: "Hi there"}}})

	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	require.Len(t, msgs.Messages, 2)
	assert.Equal(t, "Hi there", msgs.Messages[1].Content)

	// Snapshots replace the rich message instead of piling up text blocks.
	var rich struct {
		Messages []jsonlwatcher.RichMessage `json:"messages"`
	}
	n.do(http.MethodGet, "/rich-messages", nil, &rich)
	require.Len(t, rich.Messages, 1)
	require.Len(t, rich.Messages[0].Content, 1)
	assert.Equal(t, "Hi there", rich.Messages[0].Content[0].Text)

	// Raw keystrokes need a terminal.
	assert.Equal(t, http.StatusBadRequest, n.do(http.MethodPost, "/message", map[string]any{"content": "x", "type": "raw"}, nil))
}

func TestNative_QueueSteerInterrupt(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	_, res := n.send("first", httpapi.DispatchQueue)
	require.False(t, res.Queued)

	// Busy: queue by default.
	_, res = n.send("second", "")
	assert.True(t, res.Queued)
	_, res = n.send("third", "")
	assert.True(t, res.Queued)
	q := n.queue()
	require.Len(t, q, 2)

	// Reorder; a stale list is refused.
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPut, "/queue", map[string]any{"ids": []int{q[0].ID}}, nil))
	require.Equal(t, http.StatusOK, n.do(http.MethodPut, "/queue", map[string]any{"ids": []int{q[1].ID, q[0].ID}}, nil))
	q = n.queue()
	assert.Equal(t, "third", q[0].Content)

	// Steer a message into the running turn.
	code, res := n.send("steer me", httpapi.DispatchSteer)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, res.Steered)
	// Promote a queued message to a steer.
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/queue/"+itoa(q[0].ID)+"/steer", nil, nil))
	assert.Equal(t, []string{"steer me", "third"}, inputTexts(a.Steers()))
	require.Len(t, n.queue(), 1)

	// Interrupt and send next.
	code, res = n.send("urgent", httpapi.DispatchInterrupt)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, res.Queued)
	assert.Equal(t, 1, a.Interrupts())
	q = n.queue()
	require.Len(t, q, 2)
	assert.Equal(t, "urgent", q[0].Content)

	// The interrupted turn ends; the queue sends the urgent message first.
	a.Sink().TurnCompleted(native.TurnResult{StopReason: "interrupted"})
	require.Eventually(t, func() bool { return len(a.Turns()) == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "urgent", a.Turns()[1].Text)

	assert.Equal(t, http.StatusOK, n.do(http.MethodPost, "/interrupt", nil, nil))
	a.Sink().TurnCompleted(native.TurnResult{})
	require.Eventually(t, func() bool { return len(a.Turns()) == 3 }, 5*time.Second, 10*time.Millisecond)
	a.Sink().TurnCompleted(native.TurnResult{})
	require.Eventually(t, func() bool { return len(n.queue()) == 0 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/interrupt", nil, nil))
}

func TestNative_DispatchModesNeedCapabilities(t *testing.T) {
	n := newNativeServer(t, func(a *nativetest.Adapter) { a.Caps = native.Capabilities{} })
	n.send("first", "")
	code, _ := n.send("x", httpapi.DispatchSteer)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = n.send("x", httpapi.DispatchInterrupt)
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = n.send("x", "bogus")
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, http.StatusBadRequest, n.do(http.MethodPost, "/interrupt", nil, nil))
}

func TestNative_Requests(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("do it", "")
	a.Sink().RequestOpened(native.Request{
		ID: "perm-1", Kind: native.RequestKindApproval, Title: "Run: rm -rf build", Feedback: true,
		Options: []native.RequestOption{
			{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow},
			{ID: "deny", Label: "Deny", Kind: native.OptionKindDeny},
		},
	})

	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	assert.Equal(t, 1, status.PendingRequests)
	assert.Equal(t, httpapi.AgentStatusStable, status.Status)

	// A message sent while the agent waits is queued, not typed into it.
	_, res := n.send("later", "")
	assert.True(t, res.Queued)

	var list struct {
		Requests []native.RequestState `json:"requests"`
	}
	n.do(http.MethodGet, "/requests", nil, &list)
	require.Len(t, list.Requests, 1)
	assert.Equal(t, native.RequestStatusPending, list.Requests[0].Status)

	assert.Equal(t, http.StatusBadRequest, n.do(http.MethodPost, "/requests/perm-1/respond", map[string]any{"option_id": "maybe"}, nil))
	assert.Equal(t, http.StatusNotFound, n.do(http.MethodPost, "/requests/nope/respond", map[string]any{"option_id": "allow"}, nil))

	var answered native.RequestState
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/requests/perm-1/respond", map[string]any{"option_id": "deny", "text": "not that"}, &answered))
	assert.Equal(t, native.RequestStatusResolved, answered.Status)
	assert.Equal(t, []nativetest.Response{{RequestID: "perm-1", Response: native.Response{OptionID: "deny", Text: "not that"}}}, a.Responses())
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/requests/perm-1/respond", map[string]any{"option_id": "allow"}, nil))

	var one native.RequestState
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/requests/perm-1", nil, &one))
	assert.Equal(t, native.RequestStatusResolved, one.Status)
	var after httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &after)
	assert.Equal(t, 0, after.PendingRequests)
}

func TestNative_HandoffAnswersRequest(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("do it", "")
	a.Sink().RequestOpened(native.Request{
		ID: "q-1", Kind: native.RequestKindQuestion, Title: "Pick a color",
		Questions: []native.Question{{ID: "color", Question: "Which color?", Options: []native.QuestionOption{{Label: "Red"}, {Label: "Blue"}}}},
	})
	var pending struct {
		Request *struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Content string `json:"content"`
		} `json:"request"`
	}
	n.do(http.MethodGet, "/handoff", nil, &pending)
	require.NotNil(t, pending.Request)
	assert.Equal(t, "request", pending.Request.Kind)
	assert.Contains(t, pending.Request.Content, "2. Blue")
	assert.NotContains(t, pending.Request.Content, "q-1")

	var result struct {
		Outcome string `json:"outcome"`
	}
	n.do(http.MethodPost, "/handoff/reply", map[string]any{"request_id": pending.Request.ID, "id": "r1", "content": "7"}, &result)
	assert.Equal(t, "invalid", result.Outcome)
	n.do(http.MethodPost, "/handoff/reply", map[string]any{"request_id": pending.Request.ID, "id": "r2", "content": "2"}, &result)
	assert.Equal(t, "applied", result.Outcome)
	require.Len(t, a.Responses(), 1)
	assert.Equal(t, map[string][]string{"color": {"Blue"}}, a.Responses()[0].Response.Answers)
	// The same reply again is answered from the receipt.
	n.do(http.MethodPost, "/handoff/reply", map[string]any{"request_id": pending.Request.ID, "id": "r2", "content": "2"}, &result)
	assert.Equal(t, "applied", result.Outcome)
	assert.Len(t, a.Responses(), 1)
}

func TestNative_Restart(t *testing.T) {
	n := newNativeServer(t, nil)
	first := n.factory.Last()
	n.send("hello", "")
	var res struct {
		Restarted bool `json:"restarted"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/messages", nil, &res))
	assert.True(t, res.Restarted)
	assert.True(t, first.Closed())
	assert.Len(t, n.factory.Adapters(), 2)
	var msgs struct {
		Messages []httpapi.Message `json:"messages"`
	}
	n.do(http.MethodGet, "/messages", nil, &msgs)
	assert.Empty(t, msgs.Messages)
	code, sent := n.send("again", "")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, sent.Queued)
	assert.Len(t, n.factory.Last().Turns(), 1)
}

func TestPTY_DispatchModesRejected(t *testing.T) {
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType: msgfmt.AgentTypeClaude, ChatBasePath: "/chat",
		AllowedHosts: []string{"*"}, AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	n := &nativeServer{t: t, url: ts.URL}
	code, _ := n.send("x", httpapi.DispatchSteer)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, http.StatusBadRequest, n.do(http.MethodPost, "/interrupt", nil, nil))
	var list struct {
		Requests []native.RequestState `json:"requests"`
	}
	require.Equal(t, http.StatusOK, n.do(http.MethodGet, "/requests", nil, &list))
	assert.Empty(t, list.Requests)
}

func inputTexts(inputs []native.Input) []string {
	out := make([]string, len(inputs))
	for i, in := range inputs {
		out[i] = in.Text
	}
	return out
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
