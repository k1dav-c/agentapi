package screentracker

import (
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
)

func (c *PTYConversation) lastMessage(role conv.Role) conv.Message {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == role {
			return c.messages[i]
		}
	}
	return conv.Message{}
}

// previousTurnAgentMessageLocked returns the last agent message from before
// the last user message, i.e. the finalized message of the previous turn.
// Returns a zero value if there is no such message. Caller MUST hold c.lock.
func (c *PTYConversation) previousTurnAgentMessageLocked() conv.Message {
	lastUserIdx := -1
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == conv.RoleUser {
			lastUserIdx = i
			break
		}
	}
	for i := lastUserIdx - 1; i >= 0; i-- {
		if c.messages[i].Role == conv.RoleAgent {
			return c.messages[i]
		}
	}
	return conv.Message{}
}

// caller MUST hold c.lock
func (c *PTYConversation) updateLastAgentMessageLocked(screen string, timestamp time.Time) {
	if c.writingMessage {
		return
	}
	// The result only depends on the screen and on conversation state that
	// invalidates fmtCache when mutated, so an unchanged screen means the
	// whole diff+format pass would produce the same outcome as last time.
	if c.fmtCacheValid && screen == c.fmtCacheScreen {
		return
	}
	agentMessage := screenDiff(c.screenBeforeLastUserMessage, screen, c.cfg.AgentType)
	lastUserMessage := c.lastMessage(conv.RoleUser)
	var toolCalls []string
	if c.cfg.FormatMessage != nil {
		agentMessage = c.cfg.FormatMessage(agentMessage, lastUserMessage.Message)
	}
	restoredFromState := false
	if c.restored && len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == conv.RoleAgent {
		agentMessage = c.messages[len(c.messages)-1].Message
		restoredFromState = true
	}
	if c.cfg.FormatToolCall != nil {
		agentMessage, toolCalls = c.cfg.FormatToolCall(agentMessage)
	}
	// Guard against TUI re-renders leaking the previous turn's output into
	// the current turn's message (see trimPreviousMessageOverlap). Skip for
	// messages restored verbatim from persisted state.
	if !restoredFromState {
		if prev := c.previousTurnAgentMessageLocked(); prev.Message != "" {
			agentMessage = trimPreviousMessageOverlap(prev.Message, agentMessage)
		}
	}
	for _, toolCall := range toolCalls {
		if c.toolCallMessageSet[toolCall] == false {
			c.toolCallMessageSet[toolCall] = true
			c.cfg.Logger.Info("Tool call detected", "toolCall", toolCall)
		}
	}
	// The computation for this screen is complete past this point; record it
	// so identical screens can skip the pass entirely on subsequent ticks.
	c.fmtCacheValid = true
	c.fmtCacheScreen = screen

	// A new turn always gets its message, even one like the previous
	// turn's (two empty replies in a row): until it has one, the user's
	// message is the last one and the status stays changing.
	shouldCreateNewMessage := len(c.messages) == 0 || c.messages[len(c.messages)-1].Role == conv.RoleUser
	if !shouldCreateNewMessage && c.messages[len(c.messages)-1].Message == agentMessage {
		return
	}
	conversationMessage := conv.Message{
		Message: agentMessage,
		Role:    conv.RoleAgent,
		Time:    timestamp,
	}
	if shouldCreateNewMessage {
		c.messages = append(c.messages, conversationMessage)

		// Cleanup
		c.toolCallMessageSet = make(map[string]bool)

	} else {
		c.messages[len(c.messages)-1] = conversationMessage
	}
	c.messages[len(c.messages)-1].Id = len(c.messages) - 1
}
