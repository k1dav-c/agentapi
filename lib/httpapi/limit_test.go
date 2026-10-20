package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/native"
)

func (n *nativeServer) limitStop() *native.LimitStop {
	var status httpapi.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	return status.LimitStop
}

func TestNative_LimitStopHoldsTheQueue(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/limit/continue", map[string]any{"at": "now"}, nil))

	_, res := n.send("first", "")
	require.False(t, res.Queued)
	_, res = n.send("second", "")
	require.True(t, res.Queued)
	resets := time.Now().Add(time.Hour)
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonLimited, Error: "You've hit your limit", ResetsAt: &resets})

	ls := n.limitStop()
	require.NotNil(t, ls)
	assert.Equal(t, "You've hit your limit", ls.Message)
	// The queued message waits for the limit: the queue would have sent it
	// on its next tick.
	time.Sleep(700 * time.Millisecond)
	assert.Len(t, a.Turns(), 1)
	assert.Len(t, n.queue(), 1)

	// Scheduled for the reset, then cancelled.
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/limit/continue", map[string]any{"at": "reset"}, nil))
	require.NotNil(t, n.limitStop().ContinueAt)
	assert.True(t, n.limitStop().ContinueAt.After(resets))
	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/limit/continue", nil, nil))
	assert.Nil(t, n.limitStop().ContinueAt)

	// Continue now: the continuation goes before the queued message.
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/limit/continue", map[string]any{"at": "now"}, nil))
	require.Len(t, a.Turns(), 2)
	assert.Equal(t, native.ContinuePrompt, a.Turns()[1].Text)
	assert.Nil(t, n.limitStop())
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	require.Eventually(t, func() bool { return len(a.Turns()) == 3 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "second", a.Turns()[2].Text)
}

func TestNative_MessageSentPastAHeldQueue(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("first", "")
	n.send("queued", "")
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonLimited, Error: "You're out of usage credits"})
	require.NotNil(t, n.limitStop())

	// The user sends a message: it goes now, not behind the held queue.
	_, res := n.send("try again", "")
	assert.False(t, res.Queued)
	require.Len(t, a.Turns(), 2)
	assert.Equal(t, "try again", a.Turns()[1].Text)
	assert.Nil(t, n.limitStop())
	assert.Len(t, n.queue(), 1)
}

func TestNative_DismissLimitStop(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("first", "")
	n.send("queued", "")
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonLimited, Error: "You're out of usage credits"})
	require.NotNil(t, n.limitStop())
	// Without a reset time there is nothing to schedule.
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/limit/continue", map[string]any{"at": "reset"}, nil))

	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/limit", nil, nil))
	assert.Nil(t, n.limitStop())
	require.Eventually(t, func() bool { return len(a.Turns()) == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "queued", a.Turns()[1].Text)
	assert.Equal(t, http.StatusConflict, n.do(http.MethodDelete, "/limit", nil, nil))
}
