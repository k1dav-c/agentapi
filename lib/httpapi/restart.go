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
	if s.restartAgent == nil {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}

	// Clear all conversation state.
	s.emitter.Reset()
	s.conversation.Reset()
	s.mu.Lock()
	s.messageQueue = nil
	s.nextQueueID = 0
	s.queueFailID = 0
	s.queueFailCount = 0
	s.mu.Unlock()

	// Restart the agent process.
	s.emitter.SetLifecycle(LifecycleRestarting)
	newPID, err := s.restartAgent(ctx)
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return nil, huma.Error500InternalServerError(fmt.Sprintf("agent restart failed: %v", err))
	}
	s.startJSONLWatcher(newPID)
	s.emitter.SetLifecycle(LifecycleStarting)

	response := &DeleteMessagesResponse{}
	response.Body.Restarted = true
	return response, nil
}
