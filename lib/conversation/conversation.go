// Package conversation holds the conversation's messages and status, and
// the interface the agent's conversation (lib/native) reports its changes
// to; the state database (lib/statedb) and the session logs (lib/sessions)
// use its messages.
package conversation

import (
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
	ErrMessageValidationEmpty    = errors.New("message must not be empty")
	ErrMessageValidationChanging = errors.New("message can only be sent when the agent is waiting for user input")
)

// Message is a message of the conversation. Its ID is its position.
type Message struct {
	Id      int       `json:"id"`
	Message string    `json:"message"`
	Role    Role      `json:"role"`
	Time    time.Time `json:"time"`
}

// Emitter receives conversation state updates.
type Emitter interface {
	EmitMessages([]Message)
	EmitStatus(Status)
	EmitError(message string, level ErrorLevel)
}

// NopEmitter is an Emitter that discards every update, for conversations
// created without one.
type NopEmitter struct{}

func (NopEmitter) EmitMessages([]Message)       {}
func (NopEmitter) EmitStatus(Status)            {}
func (NopEmitter) EmitError(string, ErrorLevel) {}
