package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

// newQuietNativeServer starts a native server whose queue loop never runs on
// its own (its clock is a mock), so tests can dispatch by hand.
func newQuietNativeServer(t *testing.T, configure func(*nativetest.Adapter)) (*Server, *nativetest.Factory) {
	t.Helper()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	factory := &nativetest.Factory{Configure: configure}
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      mf.AgentTypeClaude,
		Transport:      TransportNative,
		Native:         NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Clock:          quartz.NewMock(t),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	require.Eventually(t, srv.native.Idle, 5*time.Second, 5*time.Millisecond)
	return srv, factory
}

func sendUserMessage(t *testing.T, srv *Server, content string) *MessageResponse {
	t.Helper()
	resp, err := srv.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: content}})
	require.NoError(t, err)
	return resp
}

func errorMessages(e *EventEmitter) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var messages []string
	for _, err := range e.errors {
		messages = append(messages, err.Message)
	}
	return messages
}

func TestNativeQueueRetriesARefusedMessage(t *testing.T) {
	srv, factory := newQuietNativeServer(t, func(a *nativetest.Adapter) { a.TurnErr = errors.New("connection reset") })
	a := factory.Last()
	a.Sink().TurnStarted()
	require.True(t, sendUserMessage(t, srv, "fix the build").Body.Queued)
	a.Sink().TurnCompleted(native.TurnResult{})

	for range maxQueueDispatchAttempts - 1 {
		srv.dispatchNextQueuedMessage()
	}
	require.Len(t, srv.queue.Messages(), 1, "a refused message waits for the next try")
	assert.Empty(t, errorMessages(srv.emitter))
	a.TurnErr = nil
	srv.dispatchNextQueuedMessage()
	assert.Empty(t, srv.queue.Messages())
	require.Len(t, a.Turns(), 1)
	assert.Equal(t, "fix the build", a.Turns()[0].Text)
}

func TestNativeQueueDropsAMessageRefusedTooOften(t *testing.T) {
	srv, factory := newQuietNativeServer(t, func(a *nativetest.Adapter) { a.TurnErr = errors.New("connection reset") })
	a := factory.Last()
	a.Sink().TurnStarted()
	sendUserMessage(t, srv, "fix the build\nand run the tests")
	sendUserMessage(t, srv, "then deploy")
	a.Sink().TurnCompleted(native.TurnResult{})

	for range maxQueueDispatchAttempts {
		srv.dispatchNextQueuedMessage()
	}
	require.Len(t, srv.queue.Messages(), 1)
	assert.Equal(t, "then deploy", srv.queue.Messages()[0].Content)
	assert.Equal(t, []string{`Dropped queued message "fix the build and run the tests" after 5 failed attempts: connection reset`}, errorMessages(srv.emitter))
}

// A delivery that timed out may have reached the agent, so it isn't sent
// again.
func TestNativeQueueDropsAMessageThatTimedOut(t *testing.T) {
	srv, factory := newQuietNativeServer(t, func(a *nativetest.Adapter) { a.TurnErr = context.DeadlineExceeded })
	a := factory.Last()
	a.Sink().TurnStarted()
	sendUserMessage(t, srv, "fix the build")
	a.Sink().TurnCompleted(native.TurnResult{})

	srv.dispatchNextQueuedMessage()
	assert.Empty(t, srv.queue.Messages())
	assert.Equal(t, []string{`Queued message "fix the build" may not have reached the agent, which didn't answer in time: context deadline exceeded`}, errorMessages(srv.emitter))
}

// Stop cuts short a queued message on its way to the agent. The agent may
// have taken it, so it leaves the queue: kept, it would be stored and, with
// --state-db, sent again once AgentAPI restarted.
func TestNativeQueueDropsAMessageStopCutShort(t *testing.T) {
	srv, factory := newQuietNativeServer(t, nil)
	a := factory.Last()
	a.Sink().TurnStarted()
	sendUserMessage(t, srv, "fix the build")
	a.Sink().TurnCompleted(native.TurnResult{})
	sending := make(chan struct{})
	a.OnStartTurn = func(ctx context.Context) {
		close(sending)
		<-ctx.Done()
	}
	// What an adapter returns for a request it wrote when its call is
	// cancelled.
	a.TurnErr = native.Unconfirmed(context.Canceled)

	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		srv.dispatchNextQueuedMessage()
	}()
	<-sending
	require.NoError(t, srv.Stop(context.Background()))
	<-dispatched
	assert.Empty(t, srv.queue.Messages())
	assert.Equal(t, []string{`Queued message "fix the build" may have reached the agent, which didn't confirm it: context canceled`}, errorMessages(srv.emitter))
}

// A call cut short counts as unconfirmed even when the adapter doesn't
// say so.
func TestNativeBackendTreatsCancelledCallsAsUncertain(t *testing.T) {
	b := nativeBackend{}
	assert.NotEmpty(t, b.notResent(context.Canceled))
	assert.NotEmpty(t, b.notResent(context.DeadlineExceeded))
	assert.NotEmpty(t, b.notResent(native.Unconfirmed(errors.New("codex app-server exited"))))
	assert.Empty(t, b.notResent(errors.New("connection reset")))
}

func TestNativeHandoffReplyAnswersTheOfferedRequest(t *testing.T) {
	srv, factory := newQuietNativeServer(t, nil)
	a := factory.Last()
	sendUserMessage(t, srv, "do it")
	sink := a.Sink()
	sink.RequestOpened(native.Request{
		ID: "r1", Kind: native.RequestKindApproval, Title: "Run ls",
		Options: []native.RequestOption{{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow}},
	})
	sink.RequestOpened(native.Request{
		ID: "r2", Kind: native.RequestKindApproval, Title: "Run rm",
		Options: []native.RequestOption{{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow}},
	})
	srv.mu.Lock()
	defer srv.mu.Unlock()
	pending, offered := srv.pendingHandoffLocked()
	require.NotNil(t, pending)
	require.Equal(t, "r1", offered.ID)

	// The agent stops waiting for the offered request before the reply
	// arrives. The reply must not approve the next request instead, and
	// nothing was sent.
	sink.RequestClosed("r1")
	result := srv.replyNativeHandoffLocked(pending.ID, "reply-1", offered, "1")
	assert.Equal(t, "superseded", result.Body.Outcome)
	assert.Empty(t, a.Responses())

	// A reply the request doesn't take is invalid, and as nothing was sent
	// a corrected one may follow. (The reply answers the question, but an
	// approval takes one of its options.)
	sink.RequestOpened(native.Request{
		ID: "r3", Kind: native.RequestKindApproval, Title: "Run make",
		Questions: []native.Question{{ID: "why", Question: "Why?", Options: []native.QuestionOption{{Label: "To build"}}}},
	})
	r3, ok := srv.native.Request("r3")
	require.True(t, ok)
	result = srv.replyNativeHandoffLocked("handoff-r3", "reply-2", r3, "1")
	assert.Equal(t, "invalid", result.Body.Outcome)
	assert.NotContains(t, srv.handoffReceipts, "handoff-r3")
	assert.Empty(t, a.Responses())
}
