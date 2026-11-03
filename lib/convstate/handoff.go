package convstate

import (
	conv "github.com/coder/agentapi/lib/conversation"
)

// HandoffView is what the human reply protocol (lib/httpapi, GET /handoff)
// reads of the emitter, as of one moment.
type HandoffView struct {
	Status StatusChangeBody
	// Revision changes on every lifecycle transition and reset, which
	// invalidates the replies to what was offered before.
	Revision uint64
	// Question is the user's last message, and Reply the agent's last
	// message after it; both are empty without one.
	Question, Reply string
}

// HandoffView returns what the human reply protocol reads of the emitter.
func (e *EventEmitter) HandoffView() HandoffView {
	e.mu.Lock()
	defer e.mu.Unlock()
	view := HandoffView{
		Status: e.statusChangeBody(), Revision: e.handoffRevision,
	}
	reply, replied := "", false
	for i := len(e.messages) - 1; i >= 0; i-- {
		msg := e.messages[i]
		if msg.Role == conv.RoleUser {
			view.Question, view.Reply = msg.Message, reply
			break
		}
		if msg.Role == conv.RoleAgent && !replied {
			reply, replied = msg.Message, true
		}
	}
	return view
}
