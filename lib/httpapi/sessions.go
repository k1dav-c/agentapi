package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/sessions"
)

// sessionListLimit is how many sessions GET /sessions lists.
const sessionListLimit = 100

// SessionInfo is one of the agent's sessions and what it spent.
type SessionInfo struct {
	sessions.SessionSummary
	Cost sessions.SessionCost `json:"cost" doc:"What the session spent, its subagents included, at the model providers' list prices (API-equivalent cost). A call a fork copied counts in the session it copied it from."`
}

// SessionsResponse lists the agent's sessions in the working directory.
type SessionsResponse struct {
	Body struct {
		Sessions []SessionInfo `json:"sessions" nullable:"false" doc:"The agent's sessions in the working directory, the latest first."`
		// Current is empty before the agent opened a session.
		Current string `json:"current,omitempty" doc:"The id of the session the agent has open, if it opened one."`
		// TotalCost covers every session of the directory, also those past
		// the list's limit.
		TotalCost sessions.SessionCost `json:"total_cost" doc:"What all the agent's sessions in the working directory spent, each call counted once, with its hours for totals by day, week or month."`
	}
}

// CostResponse is what the session the agent has open spent.
type CostResponse struct {
	Body struct {
		// SessionID is empty before the agent opened a session.
		SessionID string               `json:"session_id,omitempty" doc:"The session the agent has open."`
		Cost      sessions.SessionCost `json:"cost" doc:"What the session spent, its subagents included (API-equivalent cost)."`
		Calls     []sessions.ModelCall `json:"calls" nullable:"false" doc:"Its model calls, the earliest first, for what each task spent: a task's calls are those made from its message on."`
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
	if s.sessions == nil {
		return huma.Error400BadRequest("this agent's sessions can't be listed")
	}
	return nil
}

// listSessions handles GET /sessions, without s.mu: it only reads the
// agent's session logs.
func (s *Server) listSessions(_ context.Context, _ *struct{}) (*SessionsResponse, error) {
	list, err := s.sessions.List(sessionListLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to list the sessions: %w", err)
	}
	costs, err := s.sessions.Costs()
	if err != nil {
		return nil, fmt.Errorf("failed to read what the sessions spent: %w", err)
	}
	resp := &SessionsResponse{}
	resp.Body.Sessions = make([]SessionInfo, 0, len(list))
	for _, session := range list {
		resp.Body.Sessions = append(resp.Body.Sessions, SessionInfo{SessionSummary: session, Cost: costs.Cost(session.ID)})
	}
	resp.Body.Current = s.native.SessionRef()
	resp.Body.TotalCost = costs.Total
	return resp, nil
}

// getCost handles GET /cost: what the session the agent has open spent,
// call by call. Without s.mu: it only reads the agent's session logs.
func (s *Server) getCost(_ context.Context, _ *struct{}) (*CostResponse, error) {
	resp := &CostResponse{}
	resp.Body.SessionID = s.native.SessionRef()
	resp.Body.Calls = []sessions.ModelCall{}
	if resp.Body.SessionID != "" {
		costs, err := s.sessions.Costs()
		if err != nil {
			return nil, fmt.Errorf("failed to read what the session spent: %w", err)
		}
		if calls := costs.Sessions[resp.Body.SessionID]; calls != nil {
			resp.Body.Calls = calls
		}
	}
	resp.Body.Cost = sessions.CostOf(resp.Body.Calls)
	return resp, nil
}

// resumeSession replaces the agent with one that opens another of its
// sessions, or a copy of one, and the conversation with that session's.
// The session's log is read, and its images stored, before s.mu is taken:
// a long session takes a while.
func (s *Server) resumeSession(_ context.Context, input *ResumeSessionRequest) (*InterruptResponse, error) {
	if input.ID == s.native.SessionRef() && !input.Body.Fork {
		return okResponse(), nil
	}
	history, err := s.sessions.ReadHistory(input.ID)
	if errors.Is(err, sessions.ErrSessionNotFound) {
		return nil, huma.Error404NotFound("the agent has no such session in this directory")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the session: %w", err)
	}
	for i := range history.Rich {
		s.storeRichImages(&history.Rich[i])
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.restartRefusedLocked(); err != nil {
		return nil, huma.Error409Conflict(err.Error())
	}
	// The check that no turn runs and the retirement of the agent are one
	// step, before anything is cleared: a refused switch leaves the
	// conversation as it was.
	replacement, err := s.native.BeginSwitch(native.Resume{Ref: input.ID, Fork: input.Body.Fork}, history.Messages, func() {
		s.emitter.SetLifecycle(LifecycleRestarting)
		// The conversation, its queue and its requests are the old
		// session's.
		s.forgetConversationLocked()
		s.emitter.LoadRichMessages(history.Rich)
	})
	if errors.Is(err, native.ErrTurnActive) {
		return nil, huma.Error409Conflict("the agent is working on a turn or compacting its context; interrupt it or wait until it is done")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open the session: %w", err)
	}
	if err := s.runRestartLocked(agentRestart{run: replacement.Start, done: func() {
		if !input.Body.Fork {
			// A copy is named by the agent, if at all.
			s.emitter.SetSessionName(history.Title)
		}
	}}); err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("failed to open the session: %v", err))
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
	huma.Get(s.api, "/cost", guard(s.requireSessionHistory, s.getCost), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "What the session spent"
		o.Description = "Returns what the session the agent has open spent, its subagents included, and its model calls one by one, read from the agent's logs: the tokens of each call priced at the model providers' list prices (the API-equivalent cost, which a subscription doesn't charge), or at Pi's own prices for Pi. A model without a known price counts its tokens and no cost; --prices adds prices. For Claude Code, what it counted but didn't log call by call (compacting the context) comes as unitemized calls. Native transport of Claude Code, Codex and Pi only."
		o.Errors = []int{400}
	})
	huma.Post(s.api, "/sessions/{id}/resume", guard(s.requireSessionHistory, s.resumeSession), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "Open another session"
		o.Description = "Restarts the agent on one of its sessions from GET /sessions, or with `fork` on a copy of it under a new id, and replaces the conversation with that session's, read from its log. The queue and the open requests are cleared, as on a restart. Native transport of Claude Code, Codex and Pi only; returns 404 for a session the agent has no log of here, and 409 while the agent works on a turn or compacts its context (nothing changes then) or another restart is under way."
		o.Errors = []int{400, 404, 409}
	})
}
