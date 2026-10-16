package httpapi

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

type DeleteMessagesResponse struct {
	Body struct {
		Restarted bool `json:"restarted" doc:"Whether the agent process was restarted."`
	}
}

// deleteMessages clears all conversation state (messages, rich messages,
// timeline, errors) and restarts the agent process.
func (s *Server) deleteMessages(ctx context.Context, _ *struct{}) (*DeleteMessagesResponse, error) {
	if !s.canRestart() {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	// Serialize process replacement with browser and Discord input delivery.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all conversation state.
	s.emitter.Reset()
	s.conversation.Reset()
	s.messageQueue = nil
	s.nextQueueID = 0
	s.queueFailID = 0
	s.queueFailCount = 0

	if err := s.restartLocked(ctx); err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("agent restart failed: %v", err))
	}

	response := &DeleteMessagesResponse{}
	response.Body.Restarted = true
	return response, nil
}

// canRestart reports whether the agent can be replaced while AgentAPI runs.
func (s *Server) canRestart() bool {
	return s.restartAgent != nil || s.native != nil
}

// restartLocked replaces the agent process with a new one in a new session.
// Caller holds s.mu.
func (s *Server) restartLocked(ctx context.Context) error {
	s.emitter.SetLifecycle(LifecycleRestarting)
	if s.native != nil {
		// The conversation reports the new agent's status itself.
		if err := s.native.Restart(ctx); err != nil {
			s.emitter.SetLifecycle(LifecycleFailed)
			return err
		}
		return nil
	}
	newPID, err := s.restartAgent(ctx)
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return err
	}
	s.startJSONLWatcher(newPID)
	s.emitter.SetLifecycle(LifecycleStarting)
	return nil
}
