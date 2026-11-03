package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// TasksResponse is the body of GET /tasks.
type TasksResponse struct {
	Body struct {
		Tasks []Task `json:"tasks" nullable:"false" doc:"The conversation's tasks, the earliest first."`
	}
}

func (s *Server) getTasks(_ context.Context, _ *struct{}) (*TasksResponse, error) {
	resp := &TasksResponse{}
	resp.Body.Tasks = s.emitter.Tasks()
	return resp, nil
}

func (s *Server) registerTaskRoutes() {
	huma.Get(s.api, "/tasks", s.getTasks, func(o *huma.Operation) {
		o.Summary = "Get the conversation's tasks"
		o.Description = "Returns the conversation's tasks: each message of the user's and the agent's turn on it, " +
			"until the agent is done (stable again, with no request open). Messages the user " +
			"adds while the agent works on a task belong to it (steered). A task is running, completed, failed (the agent reported " +
			"an error or exited) or limited (a usage limit stopped it). /events sends task_update when one starts or changes."
	})
}
