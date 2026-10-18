package httpapi

import (
	"context"
	"errors"

	st "github.com/coder/agentapi/lib/screentracker"
)

// ptyBackend drives an agent through a screentracker conversation: a
// terminal (PTY) or ACP one.
type ptyBackend struct{ s *Server }

// readyForQueued: the conversation is stable, and a terminal agent's screen
// shows no dialog, which the message's Enter would answer.
func (b ptyBackend) readyForQueued() bool {
	s := b.s
	if s.conversation.Status() != st.ConversationStatusStable {
		return false
	}
	if s.emitter == nil || s.transport != TransportPTY {
		return true
	}
	s.emitter.mu.Lock()
	screen := s.emitter.screen
	s.emitter.mu.Unlock()
	if !s.terminalQuestionValid || screen != s.terminalQuestionScreen {
		s.terminalQuestionResult = isTerminalQuestionScreen(s.agentType, screen)
		s.terminalQuestionScreen = screen
		s.terminalQuestionValid = true
	}
	return !s.terminalQuestionResult
}

func (b ptyBackend) sendQueued(content string) error {
	return b.s.conversation.Send(FormatMessage(b.s.agentType, content)...)
}

// notResent: a message the agent didn't submit is still in its input box,
// and sent again it would be typed a second time.
func (b ptyBackend) notResent(err error) string {
	if errors.Is(err, st.ErrMessageNotSubmitted) {
		return "was typed but the agent didn't submit it; check the agent's input box"
	}
	return ""
}

func (b ptyBackend) canRestart() bool {
	return b.s.restartAgent != nil
}

// restart starts the agent's command again (restartAgent updates the agent
// first). Without clear the conversation goes on with the new process.
func (b ptyBackend) restart(ctx context.Context, clear bool) error {
	s := b.s
	// The old session's log stays out of the conversation.
	s.stopJSONLWatcher()
	if clear {
		s.resetConversationLocked()
	}
	pid, err := s.restartAgent(ctx)
	if err != nil {
		return err
	}
	s.startJSONLWatcher(pid)
	s.emitter.SetLifecycle(LifecycleStarting)
	return nil
}

// reload: a terminal agent reads its configuration when it starts.
func (b ptyBackend) reload(context.Context) (bool, error) {
	return false, nil
}

// close: the terminal agent's process is stopped by whoever started it.
func (b ptyBackend) close() error {
	return nil
}
