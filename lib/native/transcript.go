package native

import (
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	st "github.com/coder/agentapi/lib/screentracker"
)

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

func (c *Conversation) resetMessagesLocked() {
	c.messages = nil
	c.nextID = 0
	c.agent = nil
	clear(c.rich)
}

// appendUserMessageLocked appends a user message and returns its Id.
func (c *Conversation) appendUserMessageLocked(text string) int {
	id := c.nextID
	c.messages = append(c.messages, st.ConversationMessage{
		Id: id, Message: text, Role: st.ConversationRoleUser, Time: c.cfg.Clock.Now(),
	})
	c.nextID++
	c.agent = nil
	return id
}

// removeMessageLocked removes the user message with Id id, whose turn failed
// to start. Its Id is given out again if no message came after it.
func (c *Conversation) removeMessageLocked(id int) {
	if i := c.messageIndexLocked(id); i >= 0 {
		c.messages = slices.Delete(c.messages, i, i+1)
		if id == c.nextID-1 {
			c.nextID--
		}
	}
}

// messageIndexLocked returns the index of the message with Id id, or -1.
func (c *Conversation) messageIndexLocked(id int) int {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Id == id {
			return i
		}
	}
	return -1
}

// addRichMessageLocked puts the text of an assistant rich message into the
// agent message it belongs to.
func (c *Conversation) addRichMessageLocked(msg jsonlwatcher.RichMessage) {
	text := assistantText(msg)
	part := c.rich[msg.MessageID]
	if part == nil {
		if text == "" {
			// Wait for text before tying the message to an agent message,
			// so a message made only of tool calls doesn't start one.
			return
		}
		if c.agent == nil {
			c.agent = &agentText{id: c.nextID}
			c.messages = append(c.messages, st.ConversationMessage{
				Id: c.nextID, Role: st.ConversationRoleAgent, Time: c.cfg.Clock.Now(),
			})
			c.nextID++
		}
		part = &richPart{msg: c.agent}
		c.agent.parts = append(c.agent.parts, part)
		c.rich[msg.MessageID] = part
	}
	part.text = text
	i := c.messageIndexLocked(part.msg.id)
	if joined := part.msg.text(); i >= 0 && c.messages[i].Message != joined {
		c.messages[i].Message = joined
		c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
	}
}

// assistantText joins the text blocks of a rich message.
func assistantText(msg jsonlwatcher.RichMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}
