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

// deleteMessages clears all conversation state (messages, rich messages,
// requests, timeline, errors, the queue) and restarts the agent process.
func (s *Server) deleteMessages(_ context.Context, _ *struct{}) (*DeleteMessagesResponse, error) {
	if !s.canRestart() {
		return nil, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	// Serialize process replacement with message and handoff reply delivery.
	s.mu.Lock()
	defer s.mu.Unlock()

	// restartLocked clears a native conversation itself.
	if s.native == nil && !s.stopping {
		s.stopJSONLWatcher()
		s.resetConversationLocked()
	}
	ctx, cancel := s.agentCtx(restartTimeout)
	defer cancel()
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
	if s.stopping {
		// Stop closed the agent: don't start another one.
		return errStopping
	}
	s.emitter.SetLifecycle(LifecycleRestarting)
	if s.native != nil {
		if s.updateAgent != nil {
			// The update can take minutes: the old agent stops first, so it
			// doesn't go on working, and into the new conversation.
			_ = s.native.Close()
		}
		// The new session starts with an empty conversation, so the old
		// one goes, however the restart was asked for.
		s.resetConversationLocked()
		if s.updateAgent != nil {
			// The new agent is the latest release; a failed update (e.g.
			// offline) still restarts the installed one.
			s.updateAgent(ctx)
		}
		// The conversation reports the new agent's status itself.
		if err := s.native.Restart(ctx); err != nil {
			s.emitter.SetLifecycle(LifecycleFailed)
			return err
		}
		return nil
	}
	s.stopJSONLWatcher()
	newPID, err := s.restartAgent(ctx)
	if err != nil {
		s.emitter.SetLifecycle(LifecycleFailed)
		return err
	}
	s.startJSONLWatcher(newPID)
	s.emitter.SetLifecycle(LifecycleStarting)
	return nil
}
