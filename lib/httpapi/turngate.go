package httpapi

import (
	"errors"
	"time"
)

// Why no turn may start now (turnGateLocked). errStopping and
// errRestarting are the others.
var (
	errUpdateRestarts = errors.New("AgentAPI restarts into a new version")
	errUpdateWaits    = errors.New("an update waits for the agent to be idle")
	errAgentBusy      = errors.New("the agent can't take a turn now")
	errQueueHeld      = errors.New("a usage limit stop or a failed turn holds the queue")
)

// turnSource is who would start a turn.
type turnSource int

const (
	// turnUser is a message the user sends now (POST /message, a handoff
	// reply), or a replacement of the agent. Neither waits for an update
	// that waits for the agent; whether the agent takes the message is
	// the agent's to say (createNativeMessageLocked).
	turnUser turnSource = iota
	// turnContinuation is the message that continues a turn a usage limit
	// stopped, or tries a failed turn again, which the queue's hold waits
	// for (native.Conversation.ContinueIfDue checks the agent and the
	// time).
	turnContinuation
	// turnQueued is the head of the queue.
	turnQueued
)

// turnGateLocked says why a turn from source may not start now, or nil
// when it may. It is the one place that decides it: whatever starts a turn
// or replaces the agent asks it first. In order:
//
//   - AgentAPI stops: the queue is stored for after the restart.
//   - An update restarts AgentAPI: the messages wait for the new program.
//   - The agent restarts: they wait for the new agent.
//   - An update waits for the agent to be idle (turnContinuation,
//     turnQueued): a turn AgentAPI starts by itself would keep it waiting;
//     it goes out after the restart, the hold being stored too.
//   - The agent can't take a turn, or a usage limit stop or a failed turn
//     holds the queue (turnQueued; see agentGateLocked).
//
// Caller holds s.mu; for turnUser it reads only what needs no lock.
func (s *Server) turnGateLocked(source turnSource, now time.Time) error {
	switch {
	case s.shutdownCtx.Err() != nil:
		return errStopping
	case s.updating.Load():
		return errUpdateRestarts
	case s.restarting.Load():
		return errRestarting
	case source == turnUser:
		return nil
	case s.updater.holdsQueue():
		return errUpdateWaits
	case source == turnContinuation:
		return nil
	}
	return s.agentGateLocked(now)
}

// agentGateLocked says why the conversation keeps the head of the queue
// from going out now, or nil: the agent can't take a turn
// (errAgentBusy), or a usage limit stop or a failed turn holds the queue
// (errQueueHeld). Caller holds s.mu.
func (s *Server) agentGateLocked(now time.Time) error {
	switch {
	case !s.native.Idle():
		return errAgentBusy
	case s.native.HoldsQueue(now):
		return errQueueHeld
	}
	return nil
}
