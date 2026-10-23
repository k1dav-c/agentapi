// Package conversation holds what the transports share: the conversation's
// messages and status, the interface the server drives a transport
// through, and the one a transport reports its changes to. The PTY engine
// (lib/screentracker), the native transport (lib/native) and ACP (x/acpio)
// implement it; the state database (lib/statedb) and the session logs
// (lib/sessions) use its messages.
package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

// Status is whether the agent works on something.
type Status string

const (
	StatusChanging     Status = "changing"
	StatusStable       Status = "stable"
	StatusInitializing Status = "initializing"
)

// Role is who wrote a message.
type Role string

func (r Role) Schema(registry huma.Registry) *huma.Schema {
	return util.OpenAPISchema(registry, "ConversationRole", RoleValues)
}

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

var RoleValues = []Role{
	RoleUser,
	RoleAgent,
}

// ErrorLevel is how bad an error the agent reported is.
type ErrorLevel string

func (e ErrorLevel) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "ErrorLevel", ErrorLevelValues)
}

const (
	ErrorLevelWarning ErrorLevel = "warning"
	ErrorLevelError   ErrorLevel = "error"
)

var ErrorLevelValues = []ErrorLevel{
	ErrorLevelWarning,
	ErrorLevelError,
}

var (
	ErrMessageValidationWhitespace = errors.New("message must be trimmed of leading and trailing whitespace")
	ErrMessageValidationEmpty      = errors.New("message must not be empty")
	ErrMessageValidationChanging   = errors.New("message can only be sent when the agent is waiting for user input")
	// ErrMessageNotSubmitted means the message was typed into the agent but
	// the agent never reacted to the carriage return. The text may still be
	// sitting in the agent's input box, so resending it would duplicate it.
	ErrMessageNotSubmitted = errors.New("agent did not react to the submitted message")
	// ErrConversationReset means the conversation was reset before the
	// message reached the agent; it isn't part of the new conversation.
	ErrConversationReset = errors.New("the conversation was reset before the message was sent")
)

// Message is a message of the conversation. Its ID is its position.
type Message struct {
	Id      int       `json:"id"`
	Message string    `json:"message"`
	Role    Role      `json:"role"`
	Time    time.Time `json:"time"`
}

// MessagePart is a part of a message the user sends, as the agent reads
// it. The PTY transport types parts that know how to (screentracker's
// TerminalPart) its own way; the others send the text.
type MessagePart interface {
	String() string
}

// MessagePartText is text. A hidden part is typed into the agent's terminal
// (bracketed paste markers, say) but isn't part of the message's text.
type MessagePartText struct {
	Content string
	Hidden  bool
}

var _ MessagePart = MessagePartText{}

func (p MessagePartText) String() string {
	if p.Hidden {
		return ""
	}
	return p.Content
}

// Conversation is a conversation between a user and an agent, as the
// server drives it: it lists the messages, sends one, runs whatever keeps
// the state up to date, and reports the agent's status. What the agent's
// screen shows goes to the Emitter (EmitScreen).
type Conversation interface {
	Messages() []Message
	Send(...MessagePart) error
	Start(context.Context)
	Status() Status
	// Reset clears all conversation state back to the initial empty state.
	Reset()
}

// Emitter receives conversation state updates.
type Emitter interface {
	EmitMessages([]Message)
	EmitStatus(Status)
	EmitScreen(string)
	EmitError(message string, level ErrorLevel)
}

// NopEmitter is an Emitter that discards every update, for conversations
// created without one.
type NopEmitter struct{}

func (NopEmitter) EmitMessages([]Message)       {}
func (NopEmitter) EmitStatus(Status)            {}
func (NopEmitter) EmitScreen(string)            {}
func (NopEmitter) EmitError(string, ErrorLevel) {}
