package httpapi

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

type RestartResponse struct {
	Body struct {
		Restarted bool `json:"restarted" doc:"Whether the agent process was restarted."`
	}
}

func (s *Server) restartAgentHandler(ctx context.Context, _ *struct{}) (*RestartResponse, error) {
	if s.restartAgent == nil {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	s.emitter.SetLifecycle(LifecycleRestarting)
	newPID, err := s.restartAgent(ctx)
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return nil, huma.Error500InternalServerError(fmt.Sprintf("agent restart failed: %v", err))
	}
	s.startJSONLWatcher(newPID)
	s.emitter.SetLifecycle(LifecycleStarting)
	response := &RestartResponse{}
	response.Body.Restarted = true
	return response, nil
}
