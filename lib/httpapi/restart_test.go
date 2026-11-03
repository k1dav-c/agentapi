package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/handoff"
)

// A restart whose slow part panics leaves s.mu held again for the caller,
// whose deferred Unlock needs it, and no restart under way.
func TestRestartPanicKeepsTheLock(t *testing.T) {
	s, _ := newQuietNativeServer(t, nil)
	s.updateAgent = func(context.Context) { panic("boom") }
	func() {
		defer func() { assert.Equal(t, "boom", recover()) }()
		_, _ = s.deleteMessages(t.Context(), nil)
	}()
	assert.True(t, s.mu.TryLock(), "s.mu was let go")
	assert.False(t, s.restarting.Load())
	s.mu.Unlock()
}

// While the agent restarts, the lifecycle stays restarting and the human
// reply protocol offers nothing, even once a status reads as ready: what
// was offered is the old agent's, and a reply would reach no agent.
func TestRestartOffersNoHandoff(t *testing.T) {
	s, a, _ := offeringServer(t)
	updating, release := make(chan struct{}), make(chan struct{})
	s.updateAgent = func(context.Context) {
		close(updating)
		<-release
	}
	restarted := make(chan error, 1)
	go func() {
		_, err := s.deleteMessages(t.Context(), nil)
		restarted <- err
	}()
	<-updating

	s.emitter.EmitStatus(conv.StatusStable)
	require.Equal(t, LifecycleRestarting, s.emitter.StatusSnapshot().Lifecycle)
	assert.Nil(t, pendingHandoff(s))
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: "any", ID: "r1", Content: "go on"}})
	require.NoError(t, err)
	assert.Equal(t, "superseded", result.Body.Outcome)
	assert.Equal(t, []string{"Check the build"}, turnTexts(a))

	close(release)
	require.NoError(t, <-restarted)
}
