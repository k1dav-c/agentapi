package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/instanceapi"
)

// SummaryResponse is the body of GET /summary.
type SummaryResponse struct {
	Body instanceapi.SummaryBody
}

func (s *Server) getSummary(_ context.Context, _ *struct{}) (*SummaryResponse, error) {
	s.updateProviderHealth()
	return &SummaryResponse{Body: s.emitter.Summary()}, nil
}

func (s *Server) markSeen(_ context.Context, _ *struct{}) (*SummaryResponse, error) {
	s.updateProviderHealth()
	return &SummaryResponse{Body: s.emitter.MarkSeen()}, nil
}

func (s *Server) registerSummaryRoutes() {
	huma.Post(s.api, "/tasks/seen", s.markSeen, func(o *huma.Operation) {
		o.Summary = "Mark the tasks seen"
		o.Description = "Records that a client showed the conversation to the user now, so the tasks that ended " +
			"until now are read: the summary's unread is false until a task ends again. Every client of this " +
			"AgentAPI shares it: the chat calls it while it shows the conversation, and summary clients get " +
			"summary_update. Returns the summary."
	})
	huma.Get(s.api, "/summary", s.getSummary, func(o *huma.Operation) {
		o.Summary = "Get a summary of the agent"
		o.Description = "Returns what a client that watches many AgentAPIs shows of one in a single response: " +
			"who it is (instance), whether the agent works or waits, why it waits for the user (attention), " +
			"a title, the latest task, what waits (requests, queued messages) and the usage limits. " +
			"GET /events?only=summary sends summary_update whenever it changes, and no other events."
	})
}
