package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

type DeleteMessagesResponse struct {
	Body struct {
		Restarted bool `json:"restarted" doc:"Whether the agent process was restarted."`
	}
}

// restartTimeout bounds a restart: updating the agent CLI (up to 3
// minutes) and starting the new agent.
const restartTimeout = 5 * time.Minute

// agentCtx bounds a call that changes the agent's state (a message, an
// answer, a setting, a restart). It ends with the server, not with the
// request: a client that goes away mid-call, such as a reloaded tab, must
// not cut an answer, a restart or a CLI update short.
func (s *Server) agentCtx(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.shutdownCtx, timeout)
}

// errStopping is returned for a restart asked for while AgentAPI stops.
var errStopping = errors.New("AgentAPI is stopping")

// deleteMessages clears all conversation state (messages, rich messages,
// requests, timeline, errors, the queue) and restarts the agent process.
func (s *Server) deleteMessages(_ context.Context, _ *struct{}) (*DeleteMessagesResponse, error) {
	if !s.backend().canRestart() {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	// Serialize process replacement with message and handoff reply delivery.
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := s.agentCtx(restartTimeout)
	defer cancel()
	if err := s.restartLocked(ctx, true); err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("agent restart failed: %v", err))
	}

	response := &DeleteMessagesResponse{}
	response.Body.Restarted = true
	return response, nil
}

// forgetConversationLocked clears what the server keeps of the
// conversation: the emitter's state (messages, rich messages, requests,
// timeline, errors) and the queue, whose IDs go on counting. It leaves the
// conversation itself alone, so the native conversation can have it run
// while it replaces its agent. Caller holds s.mu.
func (s *Server) forgetConversationLocked() {
	s.emitter.Reset()
	s.queue.Clear()
}

// resetConversationLocked clears all conversation state: the
// conversation's messages, and what forgetConversationLocked clears.
// Caller holds s.mu.
func (s *Server) resetConversationLocked() {
	s.forgetConversationLocked()
	s.conversation.Reset()
}

// restartLocked replaces the agent with a new one. With clear the
// conversation starts over; a native agent's always does. Caller holds s.mu.
func (s *Server) restartLocked(ctx context.Context, clear bool) error {
	if s.stopping {
		// Stop closed the agent: don't start another one.
		return errStopping
	}
	s.emitter.SetLifecycle(LifecycleRestarting)
	if err := s.backend().restart(ctx, clear); err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return err
	}
	return nil
}
