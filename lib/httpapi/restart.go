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

// errRestarting is returned for a restart asked for while another one is
// under way.
var errRestarting = errors.New("the agent is restarting; try again once it is ready")

// deleteMessages clears all conversation state (messages, rich messages,
// requests, timeline, errors, the queue) and restarts the agent process.
func (s *Server) deleteMessages(_ context.Context, _ *struct{}) (*DeleteMessagesResponse, error) {
	if !s.backend().canRestart() {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	// Serialize process replacement with message and handoff reply delivery.
	s.mu.Lock()
	defer s.mu.Unlock()
	switch err := s.restartLocked(true); {
	case errors.Is(err, errRestarting), errors.Is(err, errStopping):
		return nil, huma.Error409Conflict(err.Error())
	case err != nil:
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

// restartRefusedLocked returns why the agent can't be replaced now, or
// nil: AgentAPI stops, or another replacement is under way. Caller holds
// s.mu.
func (s *Server) restartRefusedLocked() error {
	switch {
	case s.stopping:
		// Stop closed the agent: don't start another one.
		return errStopping
	case s.restarting:
		return errRestarting
	}
	return nil
}

// restartLocked replaces the agent with a new one. With clear the
// conversation starts over; a native agent's always does. Caller holds
// s.mu, which is released while the agent restarts (see
// runRestartLocked).
func (s *Server) restartLocked(clear bool) error {
	if err := s.restartRefusedLocked(); err != nil {
		return err
	}
	s.emitter.SetLifecycle(LifecycleRestarting)
	return s.runRestartLocked(s.backend().beginRestart(clear))
}

// runRestartLocked does what is left of a replacement of the agent that
// began with s.mu held: stopping the old agent, updating its CLI, starting
// the new one can take minutes, so they run without s.mu, which the caller
// has again when it returns. Meanwhile s.restarting is set: messages wait
// in the queue, which sends nothing, and other replacements are refused,
// so nothing gets in between; requests that don't take s.mu (GET /status,
// /handoff, /queue) answer, and Stop can cancel the restart. A restart
// that fails leaves the lifecycle failed.
func (s *Server) runRestartLocked(r agentRestart) error {
	s.restarting = true
	s.mu.Unlock()
	ctx, cancel := s.agentCtx(restartTimeout)
	err := r.run(ctx)
	cancel()
	s.mu.Lock()
	s.restarting = false
	switch {
	case err != nil:
		s.emitter.SetLifecycle(LifecycleFailed)
		return err
	case s.stopping:
		// Stop closed the new agent meanwhile.
		return errStopping
	}
	if r.done != nil {
		r.done()
	}
	return nil
}
