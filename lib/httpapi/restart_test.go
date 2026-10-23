package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A terminal agent's restart (its CLI update, its new process) runs
// without s.mu: messages sent meanwhile wait in the queue rather than be
// typed into a process that is being replaced, raw keys are refused, and
// the queue goes on once the new process runs.
func TestPTYRestartQueuesMessages(t *testing.T) {
	s, c, keys := handoffServer()
	restarting, release := make(chan struct{}), make(chan struct{})
	s.restartAgent = func(context.Context) (int, error) {
		close(restarting)
		<-release
		return 0, nil
	}
	restarted := make(chan error, 1)
	go func() {
		_, err := s.deleteMessages(t.Context(), nil)
		restarted <- err
	}()
	<-restarting

	resp, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "hello"}})
	require.NoError(t, err)
	assert.True(t, resp.Body.Queued)
	_, err = s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeRaw, Content: "\r"}})
	var statusErr huma.StatusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusConflict, statusErr.GetStatus())
	_, err = s.deleteMessages(t.Context(), nil)
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, http.StatusConflict, statusErr.GetStatus(), "one restart at a time")
	s.dispatchNextQueuedMessage()
	assert.Zero(t, c.sends)
	assert.Empty(t, keys.writes)

	close(release)
	require.NoError(t, <-restarted)
	assert.Equal(t, LifecycleStarting, s.emitter.StatusSnapshot().Lifecycle)
	s.dispatchNextQueuedMessage()
	assert.Equal(t, 1, c.sends)
	assert.Zero(t, s.queue.Len())
}

// A restart whose slow part panics leaves the server as a failed restart
// does: s.mu held again for the caller, and no restart under way.
func TestRestartPanicKeepsTheLock(t *testing.T) {
	s, _, _ := handoffServer()
	s.restartAgent = func(context.Context) (int, error) { panic("boom") }
	func() {
		defer func() { assert.Equal(t, "boom", recover()) }()
		_, _ = s.deleteMessages(t.Context(), nil)
	}()
	assert.True(t, s.mu.TryLock(), "s.mu was let go")
	assert.False(t, s.restarting)
	s.mu.Unlock()
}
