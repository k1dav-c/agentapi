package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/instanceapi"
)

// TasksResponse is the body of GET /tasks.
type TasksResponse struct {
	Body struct {
		Tasks []instanceapi.Task `json:"tasks" nullable:"false" doc:"The page's tasks, the earliest first, each with its number: without before, the latest ones."`
		Page
	}
}

func (s *Server) getTasks(_ context.Context, input *PageInput) (*TasksResponse, error) {
	r := s.emitter.Page(input.Before, input.Limit)
	resp := &TasksResponse{}
	resp.Body.Tasks = r.Tasks
	resp.Body.Page = pageOf(r)
	return resp, nil
}

func (s *Server) registerTaskRoutes() {
	huma.Get(s.api, "/tasks", s.getTasks, func(o *huma.Operation) {
		o.Summary = "Get the conversation's tasks"
		o.Description = "Returns the conversation's tasks: each message of the user's and the agent's turn on it, " +
			"until the agent is done (stable again, with no request open). Messages the user " +
			"adds while the agent works on a task belong to it (steered). A task is running, completed, failed (the agent reported " +
			"an error or exited) or limited (a usage limit stopped it). /events sends task_update when one starts or changes. " +
			"The tasks come a page at a time, as GET /messages pages them: without `before`, the latest 20 (`limit` up to 100), " +
			"and with `has_more`, `next_before` reads the page before. AgentAPI 4.2 and earlier returned them all."
	})
}
