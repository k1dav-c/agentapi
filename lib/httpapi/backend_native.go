package httpapi

import (
	"context"
	"errors"

	"github.com/coder/agentapi/lib/handoff"
	"github.com/coder/agentapi/lib/native"
)

// nativeBackend drives an agent over the native transport.
type nativeBackend struct{ s *Server }

// readyForQueued: queued messages also wait while a usage limit holds
// them (see native.Conversation.HoldsQueue).
func (b nativeBackend) readyForQueued() bool {
	return b.s.native.Idle() && !b.s.native.HoldsQueue(b.s.clock.Now())
}

func (b nativeBackend) sendQueued(content string) error {
	ctx, cancel := b.s.agentCtx(nativeCallTimeout)
	defer cancel()
	return b.s.native.StartTurn(ctx, native.InputFromText(content))
}

// notResent: a message the agent may have taken could run its task twice
// if it were sent again. The adapters mark a message that went out but
// wasn't confirmed; a call cut short counts as one too, whether it timed
// out or AgentAPI stopped meanwhile, which would otherwise keep the message
// queued and, with --state-db, send it again after the restart.
func (b nativeBackend) notResent(err error) string {
	switch {
	case errors.Is(err, native.ErrUnconfirmed):
		return "may have reached the agent, which didn't confirm it"
	case errors.Is(err, context.DeadlineExceeded):
		return "may not have reached the agent, which didn't answer in time"
	case errors.Is(err, context.Canceled):
		return "may have reached the agent before AgentAPI stopped"
	}
	return ""
}

func (b nativeBackend) canRestart() bool {
	return true
}

// beginRestart begins a new agent in a new session, which starts after the
// agent CLI was updated when the server can. The old agent is retired
// before the conversation is cleared, so nothing it still reports lands in
// the new one, which starts empty however the restart was asked for.
func (b nativeBackend) beginRestart(bool) agentRestart {
	s := b.s
	replacement := s.native.BeginRestart(s.forgetConversationLocked)
	return agentRestart{run: func(ctx context.Context) error {
		if s.updateAgent != nil {
			// The update can take minutes: the old agent stops first, so
			// it doesn't go on working meanwhile.
			replacement.CloseOld()
			// The new agent is the latest release; a failed update (e.g.
			// offline) still restarts the installed one.
			s.updateAgent(ctx)
		}
		// The conversation reports the new agent's status itself.
		return replacement.Start(ctx)
	}}
}

// beginReload begins restarting the agent on its session, which it
// resumes. One whose session can't be resumed needs a restart, into a new
// session.
func (b nativeBackend) beginReload() (agentRestart, bool, error) {
	replacement, err := b.s.native.BeginReload()
	switch {
	case errors.Is(err, native.ErrUnsupported):
		return agentRestart{}, false, nil
	case err != nil:
		return agentRestart{}, false, err
	}
	return agentRestart{run: replacement.Start}, true, nil
}

func (b nativeBackend) close() error {
	return b.s.native.Close()
}

func (b nativeBackend) dispatchModes() bool {
	return true
}

func (b nativeBackend) deliver(content string, mode DispatchMode, resp *MessageResponse) error {
	return b.s.createNativeMessageLocked(content, mode, resp)
}

// beforeQueue sends the continuation of a turn a usage limit stopped once
// it is due: the queued messages go after it.
func (b nativeBackend) beforeQueue() {
	b.s.continueIfDueLocked()
}

// handoffOffer offers the oldest pending request.
func (b nativeBackend) handoffOffer(handoffView) (string, string, native.RequestState, bool) {
	request, text, ok := b.s.pendingNativeHandoff()
	return "request", text, request, ok
}

// answerHandoff answers the request the handoff offered. The server's lock
// is held, so no other reply is delivered meanwhile.
func (b nativeBackend) answerHandoff(pending *handoff.Request, r native.RequestState, replyID, content string) handoff.Result {
	s := b.s
	response, ok := nativeHandoffResponse(r, content)
	if !ok {
		return handoff.Result{Outcome: "invalid"}
	}
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	result := handoff.Result{Outcome: "applied"}
	switch _, err := s.native.Respond(ctx, r.ID, response); {
	case errors.Is(err, native.ErrInvalidResponse):
		// Nothing was sent, so a corrected reply may follow.
		return handoff.Result{Outcome: "invalid"}
	case errors.Is(err, native.ErrRequestNotFound), errors.Is(err, native.ErrNotStarted):
		// Answered elsewhere, withdrawn or expired; nothing was sent.
		result.Outcome = "superseded"
	case err != nil:
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", r.ID, "error", err)
		result.Outcome = "uncertain"
	}
	s.handoff.record(pending.ID, replyID, result)
	return result
}
