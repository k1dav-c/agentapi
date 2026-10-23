package native

import (
	"slices"
	"strings"

	"github.com/coder/quartz"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

// messageLog is a conversation as conv.Conversation messages: the user's
// messages, and agent messages made of the text of the agent's rich
// messages.
type messageLog struct {
	clock    quartz.Clock
	messages []conv.Message
	nextID   int
	// agent is the agent message being built, or nil until the agent says
	// something after the last user message.
	agent *agentText
	// rich maps the assistant rich messages to their part of an agent
	// message. A rich message keeps its agent message even if it is still
	// updated after a later user message (a steer), so its text isn't
	// repeated in the next agent message.
	rich map[string]*richPart
}

// agentText is an agent message made of the text of rich messages.
type agentText struct {
	// id is the agent message's Id.
	id int
	// parts holds the rich messages' text in the order they appeared.
	parts []*richPart
}

// richPart is the text a rich message adds to an agent message.
type richPart struct {
	msg  *agentText
	text string
}

// text joins the text of the agent message's rich messages.
func (a *agentText) text() string {
	texts := make([]string, 0, len(a.parts))
	for _, p := range a.parts {
		if p.text != "" {
			texts = append(texts, p.text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// newMessageLog returns a messageLog that continues messages.
func newMessageLog(clock quartz.Clock, messages []conv.Message) messageLog {
	t := messageLog{clock: clock, messages: slices.Clone(messages), rich: make(map[string]*richPart)}
	for _, m := range t.messages {
		t.nextID = max(t.nextID, m.Id+1)
	}
	return t
}

// snapshot returns a copy of the messages.
func (t *messageLog) snapshot() []conv.Message {
	return slices.Clone(t.messages)
}

// reset clears the messages.
func (t *messageLog) reset() {
	t.messages = nil
	t.nextID = 0
	t.forgetRich()
}

// forgetRich forgets the rich messages, whose IDs the next adapter may use
// again; the agent's next text starts a new agent message.
func (t *messageLog) forgetRich() {
	t.agent = nil
	clear(t.rich)
}

// endAgentMessage ends the agent message being built: the agent's next
// text starts a new one.
func (t *messageLog) endAgentMessage() {
	t.agent = nil
}

// appendUser appends a user message and returns its Id.
func (t *messageLog) appendUser(text string) int {
	id := t.nextID
	t.messages = append(t.messages, conv.Message{
		Id: id, Message: text, Role: conv.RoleUser, Time: t.clock.Now(),
	})
	t.nextID++
	t.agent = nil
	return id
}

// remove removes the user message with Id id, whose turn failed to start.
// Its Id is given out again if no message came after it.
func (t *messageLog) remove(id int) {
	if i := t.index(id); i >= 0 {
		t.messages = slices.Delete(t.messages, i, i+1)
		if id == t.nextID-1 {
			t.nextID--
		}
	}
}

// index returns the index of the message with Id id, or -1.
func (t *messageLog) index(id int) int {
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].Id == id {
			return i
		}
	}
	return -1
}

// addRich puts the text of an assistant rich message into the agent
// message it belongs to, and reports whether the messages changed.
func (t *messageLog) addRich(msg transcript.RichMessage) bool {
	text := assistantText(msg)
	part := t.rich[msg.MessageID]
	if part == nil {
		if text == "" {
			// Wait for text before tying the message to an agent message,
			// so a message made only of tool calls doesn't start one.
			return false
		}
		if t.agent == nil {
			t.agent = &agentText{id: t.nextID}
			t.messages = append(t.messages, conv.Message{
				Id: t.nextID, Role: conv.RoleAgent, Time: t.clock.Now(),
			})
			t.nextID++
		}
		part = &richPart{msg: t.agent}
		t.agent.parts = append(t.agent.parts, part)
		t.rich[msg.MessageID] = part
	}
	part.text = text
	i := t.index(part.msg.id)
	if joined := part.msg.text(); i >= 0 && t.messages[i].Message != joined {
		t.messages[i].Message = joined
		return true
	}
	return false
}

// assistantText joins the text blocks of a rich message.
func assistantText(msg transcript.RichMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}
