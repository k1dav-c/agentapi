package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
)

// sessionListLimit is how many sessions GET /sessions lists.
const sessionListLimit = 100

// SessionsResponse lists the agent's sessions in the working directory.
type SessionsResponse struct {
	Body struct {
		Sessions []jsonlwatcher.SessionSummary `json:"sessions" nullable:"false" doc:"The agent's sessions in the working directory, the latest first."`
		// Current is empty before the agent opened a session.
		Current string `json:"current,omitempty" doc:"The id of the session the agent has open, if it opened one."`
	}
}

// ResumeSessionRequest asks to open one of the agent's sessions.
type ResumeSessionRequest struct {
	ID   string `path:"id" doc:"The session's id, from GET /sessions."`
	Body struct {
		Fork bool `json:"fork,omitempty" doc:"Open a copy of the session, under a new id, and leave the session itself as it is."`
	}
}

// requireSessionHistory fails for a server whose agent's sessions can't be
// listed: the native transport of Claude Code, Codex and Pi only.
func (s *Server) requireSessionHistory() error {
	if err := s.requireNative(); err != nil {
		return err
	}
	if !jsonlwatcher.HasSessionHistory(string(s.agentType)) {
		return huma.Error400BadRequest("this agent's sessions can't be listed")
	}
	return nil
}

// listSessions handles GET /sessions, without s.mu: it only reads the
// agent's session logs.
func (s *Server) listSessions(_ context.Context, _ *struct{}) (*SessionsResponse, error) {
	sessions, err := jsonlwatcher.ListSessions(string(s.agentType), s.cwd, sessionListLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list the sessions: %w", err)
	}
	resp := &SessionsResponse{}
	resp.Body.Sessions = sessions
	if resp.Body.Sessions == nil {
		resp.Body.Sessions = []jsonlwatcher.SessionSummary{}
	}
	resp.Body.Current = s.native.SessionRef()
	return resp, nil
}

// resumeSession replaces the agent with one that opens another of its
// sessions, or a copy of one, and the conversation with that session's.
func (s *Server) resumeSession(_ context.Context, input *ResumeSessionRequest) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return nil, huma.Error409Conflict(errStopping.Error())
	}
	if s.native.TurnActive() {
		return nil, huma.Error409Conflict("the agent is working on a turn; interrupt it or wait until it is done")
	}
	if input.ID == s.native.SessionRef() && !input.Body.Fork {
		return okResponse(), nil
	}
	history, err := jsonlwatcher.ReadSessionHistory(string(s.agentType), s.cwd, input.ID)
	if errors.Is(err, jsonlwatcher.ErrSessionNotFound) {
		return nil, huma.Error404NotFound("the agent has no such session in this directory")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the session: %w", err)
	}

	s.emitter.SetLifecycle(LifecycleRestarting)
	// The conversation, its queue and its requests are the old session's.
	s.resetConversationLocked()
	for _, msg := range history.Rich {
		s.observer.RichMessage(msg)
	}
	ctx, cancel := s.agentCtx(restartTimeout)
	defer cancel()
	switch err := s.native.Switch(ctx, native.Resume{Ref: input.ID, Fork: input.Body.Fork}, history.Messages); {
	case errors.Is(err, native.ErrTurnActive):
		return nil, huma.Error409Conflict("the agent started a turn; try again once it is done")
	case err != nil:
		s.emitter.SetLifecycle(LifecycleFailed)
		return nil, huma.Error500InternalServerError(fmt.Sprintf("failed to open the session: %v", err))
	}
	if !input.Body.Fork {
		// A copy is named by the agent, if at all.
		s.emitter.SetSessionName(history.Title)
	}
	return okResponse(), nil
}

func (s *Server) registerSessionRoutes() {
	huma.Get(s.api, "/sessions", guard(s.requireSessionHistory, s.listSessions), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "List the agent's sessions"
		o.Description = "Lists the sessions the agent had in the working directory, read from its session logs (Claude Code's projects directory, Codex's sessions, Pi's session directory), the latest first, with the one it has open. Sessions in which the user sent nothing are left out. Native transport of Claude Code, Codex and Pi only."
		o.Errors = []int{400}
	})
	huma.Post(s.api, "/sessions/{id}/resume", guard(s.requireSessionHistory, s.resumeSession), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "Open another session"
		o.Description = "Restarts the agent on one of its sessions from GET /sessions, or with `fork` on a copy of it under a new id, and replaces the conversation with that session's, read from its log. The queue and the open requests are cleared, as on a restart. Native transport of Claude Code, Codex and Pi only; returns 404 for a session the agent has no log of here and 409 while the agent works on a turn."
		o.Errors = []int{400, 404, 409}
	})
}
