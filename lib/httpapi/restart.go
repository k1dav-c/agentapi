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
// requests, timeline, errors, the queue) and restarts the agent process.
func (s *Server) deleteMessages(ctx context.Context, _ *struct{}) (*DeleteMessagesResponse, error) {
	if !s.canRestart() {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	// Serialize process replacement with message and handoff reply delivery.
	s.mu.Lock()
	defer s.mu.Unlock()

	// restartLocked clears a native conversation itself.
	if s.native == nil {
		s.resetConversationLocked()
	}
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

// resetConversationLocked clears all conversation state: messages, rich
// messages, requests, timeline, errors and the queue. Caller holds s.mu.
func (s *Server) resetConversationLocked() {
	s.emitter.Reset()
	s.conversation.Reset()
	s.messageQueue = nil
	s.nextQueueID = 0
	s.queueFailID = 0
	s.queueFailCount = 0
}

// restartLocked replaces the agent process with a new one in a new session.
// Caller holds s.mu.
func (s *Server) restartLocked(ctx context.Context) error {
	s.emitter.SetLifecycle(LifecycleRestarting)
	if s.native != nil {
		// The new session starts with an empty conversation, so the old
		// one goes, however the restart was asked for.
		s.resetConversationLocked()
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
