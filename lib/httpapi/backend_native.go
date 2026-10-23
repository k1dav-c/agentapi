package httpapi

import (
	"context"
	"errors"

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

// notResent: a call that timed out may have reached the agent, and sent
// again the message could run its task twice.
func (b nativeBackend) notResent(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "may not have reached the agent, which didn't answer in time"
	}
	return ""
}

func (b nativeBackend) canRestart() bool {
	return true
}

// restart starts a new agent in a new session, after updating the agent
// CLI when the server can. The old agent is retired before the
// conversation is cleared, so nothing it still reports lands in the new
// one, which starts empty however the restart was asked for.
func (b nativeBackend) restart(ctx context.Context, _ bool) error {
	s := b.s
	replacement := s.native.BeginRestart(s.forgetConversationLocked)
	if s.updateAgent != nil {
		// The update can take minutes: the old agent stops first, so it
		// doesn't go on working meanwhile.
		replacement.CloseOld()
		// The new agent is the latest release; a failed update (e.g.
		// offline) still restarts the installed one.
		s.updateAgent(ctx)
	}
	// The conversation reports the new agent's status itself.
	return replacement.Start(ctx)
}

// reload restarts the agent on its session, which it resumes. One whose
// session can't be resumed needs a restart, into a new session.
func (b nativeBackend) reload(ctx context.Context) (bool, error) {
	replacement, err := b.s.native.BeginReload()
	switch {
	case errors.Is(err, native.ErrUnsupported):
		return false, nil
	case err != nil:
		return false, err
	}
	if err := replacement.Start(ctx); err != nil {
		return false, err
	}
	return true, nil
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

func (b nativeBackend) pendingRequest() (native.RequestState, string, bool) {
	return b.s.pendingNativeHandoff()
}
