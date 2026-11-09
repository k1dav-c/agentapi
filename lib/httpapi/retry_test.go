package httpapi_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/statedb"
)

func (n *nativeServer) retryWait() *agentstate.RetryWait {
	var status convstate.StatusChangeBody
	n.do(http.MethodGet, "/status", nil, &status)
	return status.RetryWait
}

// badGateway fails the agent's turn the way Claude Code does when its
// gateway answers 502.
var badGateway = native.TurnResult{
	StopReason: native.StopReasonTransient, APIStatus: 502,
	Error: "Claude Code: the turn failed (api_error), API status 502: API Error: 502 Bad Gateway. This is a server-side issue, usually temporary — try again in a moment.",
}

func withRetryDelays(delays ...time.Duration) func(*httpapi.ServerConfig) {
	return func(config *httpapi.ServerConfig) { config.Native.RetryDelays = delays }
}

func TestNative_RetryHoldsTheQueue(t *testing.T) {
	n := newNativeServerConfig(t, nil, withRetryDelays(time.Hour, time.Hour))
	a := n.factory.Last()
	assert.Equal(t, http.StatusConflict, n.do(http.MethodPost, "/retry", nil, nil))
	assert.Equal(t, http.StatusConflict, n.do(http.MethodDelete, "/retry", nil, nil))

	n.send("first", "")
	_, res := n.send("second", "")
	require.True(t, res.Queued)
	a.Sink().TurnCompleted(badGateway)

	rw := n.retryWait()
	require.NotNil(t, rw)
	assert.Equal(t, 502, rw.APIStatus)
	assert.Equal(t, 1, rw.Attempt)
	assert.Equal(t, 2, rw.Attempts)
	// The queued message waits for the retry: the queue would have sent it
	// on its next tick.
	time.Sleep(700 * time.Millisecond)
	assert.Len(t, a.Turns(), 1)

	// Retry now: the continuation goes before the queued message.
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/retry", nil, nil))
	require.Len(t, a.Turns(), 2)
	assert.Equal(t, native.ContinuePrompt, a.Turns()[1].Text)
	assert.Nil(t, n.retryWait())

	// It fails again: the second and last retry waits.
	a.Sink().TurnCompleted(badGateway)
	require.NotNil(t, n.retryWait())
	assert.Equal(t, 2, n.retryWait().Attempt)
	require.Equal(t, http.StatusOK, n.do(http.MethodPost, "/retry", nil, nil))

	// The last fails too: AgentAPI gives up, and the queue goes on.
	a.Sink().TurnCompleted(badGateway)
	assert.Nil(t, n.retryWait())
	require.Eventually(t, func() bool { return len(a.Turns()) == 4 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "second", a.Turns()[3].Text)
}

func TestNative_RetryGoesOutWhenDue(t *testing.T) {
	n := newNativeServerConfig(t, nil, withRetryDelays(100*time.Millisecond))
	a := n.factory.Last()
	n.send("first", "")
	n.send("second", "")
	a.Sink().TurnCompleted(badGateway)
	require.NotNil(t, n.retryWait())

	require.Eventually(t, func() bool { return len(a.Turns()) == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, native.ContinuePrompt, a.Turns()[1].Text)
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	require.Eventually(t, func() bool { return len(a.Turns()) == 3 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "second", a.Turns()[2].Text)
}

func TestNative_CancelRetry(t *testing.T) {
	n := newNativeServer(t, nil)
	a := n.factory.Last()
	n.send("first", "")
	n.send("queued", "")
	a.Sink().TurnCompleted(badGateway)
	require.NotNil(t, n.retryWait(), "retries are on by default")
	assert.Equal(t, 15, n.retryWait().Attempts, "30s, 1m, 2m, then 5m twelve times")

	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/retry", nil, nil))
	assert.Nil(t, n.retryWait())
	require.Eventually(t, func() bool { return len(a.Turns()) == 2 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "queued", a.Turns()[1].Text, "no retry, the queue goes on")
}

func TestNative_RetriesOff(t *testing.T) {
	n := newNativeServerConfig(t, nil, func(config *httpapi.ServerConfig) { config.Native.RetryDelays = []time.Duration{} })
	a := n.factory.Last()
	n.send("first", "")
	a.Sink().TurnCompleted(badGateway)
	assert.Nil(t, n.retryWait())
}

func TestStateDB_KeepsTheRetryWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	n, srv, db, err := persistentServer(t, path, "/work")
	require.NoError(t, err)
	n.send("first", "")
	n.factory.Last().Sink().TurnCompleted(badGateway)
	before := n.retryWait()
	require.NotNil(t, before)
	stop(t, srv, db)

	n, srv, db, err = persistentServer(t, path, "/work")
	require.NoError(t, err)
	after := n.retryWait()
	require.NotNil(t, after, "the retry still waits after a restart")
	assert.True(t, before.RetryAt.Equal(after.RetryAt))
	assert.Equal(t, before.Attempt, after.Attempt)
	require.Equal(t, http.StatusOK, n.do(http.MethodDelete, "/retry", nil, nil))
	stop(t, srv, db)

	n, srv, db, err = persistentServer(t, path, "/work")
	require.NoError(t, err)
	defer stop(t, srv, db)
	assert.Nil(t, n.retryWait())
}

// A state database AgentAPI 3.7.2 wrote, with the retry wait and the
// settings in keys of their own, comes back the same.
func TestStateDB_RestoresTheStateOfAnOlderAgentAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	n, srv, db, err := persistentServer(t, path, "/work")
	require.NoError(t, err)
	n.send("first", "")
	n.factory.Last().Sink().TurnCompleted(badGateway)
	before := n.retryWait()
	require.NotNil(t, before)
	stop(t, srv, db)

	// As 3.7.2 left it: a key each, and no conversation_state.
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	var data string
	require.NoError(t, raw.QueryRow("SELECT value FROM meta WHERE key = ?", statedb.MetaState).Scan(&data))
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(data), &object))
	require.Contains(t, string(object["retry_wait"]), "Bad Gateway")
	for key, value := range map[string]string{"settings": string(object["settings"]), "limit_stop": "", "retry_wait": string(object["retry_wait"])} {
		_, err = raw.Exec("INSERT INTO meta(key, value) VALUES(?, ?)", key, value)
		require.NoError(t, err)
	}
	_, err = raw.Exec("DELETE FROM meta WHERE key = ?", statedb.MetaState)
	require.NoError(t, err)
	require.NoError(t, raw.Close())

	n, srv, db, err = persistentServer(t, path, "/work")
	require.NoError(t, err)
	defer stop(t, srv, db)
	after := n.retryWait()
	require.NotNil(t, after, "the retry still waits")
	assert.True(t, before.RetryAt.Equal(after.RetryAt))
	assert.Equal(t, before.Attempt, after.Attempt)
}
