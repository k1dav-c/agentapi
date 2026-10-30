package httpapi

import (
	"context"

	"github.com/coder/agentapi/lib/handoff"
	"github.com/coder/agentapi/lib/native"
)

// agentBackend is the part of the server's work that depends on the
// transport: a terminal conversation (ptyBackend) or a native one
// (nativeBackend). Its methods are called with s.mu held, but for
// handoffOffer, which only reads, and GET /handoff calls without it.
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
	// beginRestart begins replacing the agent with a new one, with the
	// lifecycle restarting, and returns the rest (see agentRestart). With
	// clear the conversation starts over; a native agent's always does,
	// since the new agent starts a new session.
	beginRestart(clear bool) agentRestart
	// beginReload begins restarting the agent on its session after its
	// configuration (the MCP servers) changed, keeping the conversation.
	// It reports false, having changed nothing, when the agent can't, and
	// needs a restart instead.
	beginReload() (agentRestart, bool, error)
	// close stops the agent for good, when AgentAPI stops.
	close() error

	// beforeQueue runs on the queue's tick, before the queue goes on: what
	// the agent needs for its messages to go out (a continuation after a
	// usage limit that is due, a Codex update dialog to dismiss).
	beforeQueue()

	// dispatchModes reports whether a user message can steer or interrupt
	// the agent's turn rather than wait for it to end.
	dispatchModes() bool
	// deliver sends a user message to the agent according to mode, or
	// queues it, and says which in resp.
	deliver(content string, mode DispatchMode, resp *MessageResponse) error

	// handoffOffer returns what the agent waits on that the human reply
	// protocol offers instead of the agent's last message: its oldest
	// pending request (native, kind "request") or a dialog on its
	// terminal (PTY, kind "terminal"), as text, and for a request the
	// request that a reply answers. It reports false when the agent waits
	// on neither.
	handoffOffer(view handoffView) (kind, content string, request native.RequestState, ok bool)
	// answerHandoff delivers content, a human reply, to what handoffOffer
	// offered as pending (with request, for a native request), records
	// its outcome for the reply replyID, and returns it.
	answerHandoff(pending *handoff.Request, request native.RequestState, replyID, content string) handoff.Result
}

// agentRestart is what is left of a replacement of the agent once it
// began: the old agent no longer changes the conversation, and run does
// the slow part, which can take minutes.
type agentRestart struct {
	// run stops the old agent, updates its CLI if the server does, and
	// starts the new one. It runs without s.mu.
	run func(ctx context.Context) error
	// done, if set, completes the restart once run succeeded. Caller
	// holds s.mu.
	done func()
}

// backend returns the server's agentBackend.
func (s *Server) backend() agentBackend {
	if s.native != nil {
		return nativeBackend{s}
	}
	return ptyBackend{s}
}
