package httpapi

import (
	"context"

	"github.com/coder/agentapi/lib/native"
)

// agentBackend is the part of the server's work that depends on the
// transport: a terminal or ACP conversation (ptyBackend) or a native one
// (nativeBackend). Its methods are called with s.mu held.
type agentBackend interface {
	// readyForQueued reports whether the agent can take the next queued
	// message.
	readyForQueued() bool
	// sendQueued sends a queued message to the agent.
	sendQueued(content string) error
	// notResent says why a queued message whose send failed with err must
	// not be sent again, or "" when it can be.
	notResent(err error) string

	// canRestart reports whether the agent can be replaced while AgentAPI
	// runs.
	canRestart() bool
	// restart replaces the agent with a new one. With clear the
	// conversation starts over; a native agent's always does, since the new
	// agent starts a new session.
	restart(ctx context.Context, clear bool) error
	// reload restarts the agent on its session after its configuration
	// (the MCP servers) changed, keeping the conversation. It reports false
	// when the agent can't, and needs a restart instead.
	reload(ctx context.Context) (bool, error)
	// close stops the agent for good, when AgentAPI stops.
	close() error

	// dispatchModes reports whether a user message can steer or interrupt
	// the agent's turn rather than wait for it to end.
	dispatchModes() bool
	// deliver sends a user message to the agent according to mode, or
	// queues it, and says which in resp.
	deliver(content string, mode DispatchMode, resp *MessageResponse) error
	// pendingRequest returns the request the agent waits on that the
	// human reply protocol offers, as text, or false.
	pendingRequest() (native.RequestState, string, bool)
}

// backend returns the server's agentBackend.
func (s *Server) backend() agentBackend {
	if s.native != nil {
		return nativeBackend{s}
	}
	return ptyBackend{s}
}
