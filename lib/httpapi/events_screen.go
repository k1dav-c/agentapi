package httpapi

import (
	"strings"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
)

func (e *EventEmitter) EmitScreen(newScreen string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.screen == newScreen {
		return
	}

	e.notifyChannels(EventTypeScreenUpdate, ScreenUpdateBody{Screen: strings.TrimRight(newScreen, mf.WhiteSpaceChars)})
	e.screen = newScreen
	// What an ACP agent streams is no terminal.
	e.screenQuestion = e.transport != TransportACP && isTerminalQuestionScreen(e.agentType, newScreen)

	// Clients decide whether to offer prompt options from this rather than
	// guessing from message text, where a numbered list in an answer looks
	// just like a selection menu.
	prompt := ""
	if e.screenQuestion {
		prompt = screenTail(newScreen, terminalQuestionTailLines)
	}
	e.updateTerminalPromptLocked(prompt)
}

// Screen returns the agent's terminal screen as last emitted, and whether
// it shows a dialog that waits for an answer.
func (e *EventEmitter) Screen() (screen string, question bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.screen, e.screenQuestion
}

// terminalPromptSettle is how long a prompt must stay on screen before it is
// published. Claude Code draws its dialogs before it accepts keys, so an
// option picked the moment the prompt appeared could be ignored.
const terminalPromptSettle = time.Second

// updateTerminalPromptLocked publishes a new prompt once it has settled, and
// clears a prompt that went away or changed immediately. Caller holds e.mu.
func (e *EventEmitter) updateTerminalPromptLocked(prompt string) {
	if prompt == e.pendingPrompt && e.promptTimer != nil {
		return
	}
	if e.promptTimer != nil {
		e.promptTimer.Stop()
		e.promptTimer = nil
	}
	e.pendingPrompt = ""
	if prompt == e.terminalPrompt {
		return
	}
	// Clear a published prompt as soon as it changes: its options would
	// answer a dialog that is no longer showing.
	if e.terminalPrompt != "" {
		e.terminalPrompt = ""
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}
	if prompt == "" {
		return
	}
	e.pendingPrompt = prompt
	e.promptTimer = e.clock.AfterFunc(terminalPromptSettle, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.pendingPrompt != prompt {
			return
		}
		e.pendingPrompt = ""
		e.promptTimer = nil
		e.terminalPrompt = prompt
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}, "EventEmitter", "terminalPrompt")
}
