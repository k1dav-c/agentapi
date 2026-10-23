package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/handoff"
	"github.com/coder/agentapi/lib/native"
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
	_, question := s.emitter.Screen()
	return !question
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

// beginRestart begins starting the agent's command again (restartAgent
// updates the agent first). Without clear the conversation goes on with
// the new process.
func (b ptyBackend) beginRestart(clear bool) agentRestart {
	s := b.s
	s.emitter.SetLifecycle(LifecycleRestarting)
	// The old session's log stays out of the conversation.
	s.stopJSONLWatcher()
	if clear {
		s.resetConversationLocked()
	}
	var pid int
	return agentRestart{
		run: func(ctx context.Context) error {
			var err error
			pid, err = s.restartAgent(ctx)
			return err
		},
		done: func() {
			s.startJSONLWatcher(pid)
			s.emitter.SetLifecycle(LifecycleStarting)
		},
	}
}

// beginReload: a terminal agent reads its configuration when it starts.
func (b ptyBackend) beginReload() (agentRestart, bool, error) {
	return agentRestart{}, false, nil
}

// close: the terminal agent's process is stopped by whoever started it.
func (b ptyBackend) close() error {
	return nil
}

func (b ptyBackend) dispatchModes() bool {
	return false
}

// deliver answers a dialog that has a text field with the message, or
// types the message. It waits in the queue behind earlier messages, while
// the agent works, and while the terminal shows a dialog: the dialog would
// ignore the text, and its Enter would pick the highlighted option (e.g.
// approve a permission request).
func (b ptyBackend) deliver(content string, _ DispatchMode, resp *MessageResponse) error {
	s := b.s
	var screen string
	var question bool
	if s.transport == TransportPTY && s.emitter != nil {
		screen, question = s.emitter.Screen()
	}
	if keys, ok := freeTextReplyKeys(s.agentType, screen); question && ok {
		if err := s.conversation.Send(formatFreeTextReply(s.agentType, keys, content)...); err != nil {
			if errors.Is(err, st.ErrMessageValidationChanging) {
				return huma.Error409Conflict("the agent's prompt is still updating; try again in a moment")
			}
			return fmt.Errorf("failed to send reply to the agent's prompt: %w", err)
		}
		return nil
	}
	if s.queue.Len() > 0 || s.conversation.Status() != st.ConversationStatusStable || question {
		s.queueMessageLocked(resp, content, false)
		return nil
	}
	if err := s.conversation.Send(FormatMessage(s.agentType, content)...); err != nil {
		if errors.Is(err, st.ErrMessageValidationChanging) {
			s.queueMessageLocked(resp, content, false)
			return nil
		}
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}

// beforeQueue dismisses the update dialog Codex shows at startup, which
// would hold the queue.
func (b ptyBackend) beforeQueue() {
	b.s.dismissCodexUpdatePromptLocked()
}

// handoffOffer offers a dialog of the agent's terminal.
func (b ptyBackend) handoffOffer(view handoffView) (string, string, native.RequestState, bool) {
	if view.dialog && b.s.transport == TransportPTY {
		return "terminal", view.screen, native.RequestState{}, true
	}
	return "", "", native.RequestState{}, false
}

// answerHandoff answers a dialog of the agent's terminal. Free text
// answering a prompt with a text field (Claude's plan feedback, Codex's
// question notes) goes into that field; other answers become the keys
// that pick an option.
func (b ptyBackend) answerHandoff(pending *handoff.Request, _ native.RequestState, replyID, content string) handoff.Result {
	s := b.s
	freeTextKeys, freeText := "", false
	if !isTerminalKeyAnswer(content) {
		freeTextKeys, freeText = freeTextReplyKeys(s.agentType, pending.Content)
	}
	if !freeText {
		var ok bool
		content, ok = terminalReply(s.agentType, pending.Content, content)
		if !ok {
			return handoff.Result{Outcome: "invalid"}
		}
	}
	// Reserve before touching the PTY. A failed/partial write must never be
	// retried automatically; it may already have approved an operation.
	result := handoff.Result{Outcome: "uncertain"}
	s.handoff.record(pending.ID, replyID, result)
	var err error
	if freeText {
		err = s.conversation.Send(formatFreeTextReply(s.agentType, freeTextKeys, strings.TrimSpace(content))...)
	} else {
		var n int
		n, err = s.agentio.Write([]byte(content))
		if err == nil && n != len(content) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		result.Outcome = "applied"
	} else {
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", pending.ID, "error", err)
	}
	s.handoff.record(pending.ID, replyID, result)
	return result
}
