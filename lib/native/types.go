// Package native drives coding agents through their own structured protocols
// (Claude Code's stream-json, Codex's app-server, Pi's RPC mode) instead of a
// terminal. An Adapter speaks one agent's protocol; Conversation turns its
// events into the conversation state the HTTP API serves.
package native

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

var (
	// ErrUnsupported is returned for an operation the agent's protocol
	// doesn't offer (see agentstate.Capabilities).
	ErrUnsupported = errors.New("not supported by this agent")
	// ErrNoActiveTurn is returned by Steer and Interrupt when the agent is
	// not working on a turn.
	ErrNoActiveTurn = errors.New("the agent is not working on a turn")
	// ErrTurnActive is returned by StartTurn and Compact while a turn is
	// running, and by the conversation's Compact and BeginSwitch.
	ErrTurnActive = errors.New("the agent is already working on a turn")
	// ErrRequestNotFound is returned by Respond for a request that is not
	// pending, for example one the agent already withdrew.
	ErrRequestNotFound = errors.New("request is not pending")
	// ErrInvalidResponse is returned by Respond when the response doesn't
	// answer the request (unknown option, missing answers).
	ErrInvalidResponse = errors.New("response does not answer the request")
	// ErrNotStarted is returned before Start has completed.
	ErrNotStarted = errors.New("agent session has not started")
)

// ErrNoLoginRequired is returned by RetryLogin when the agent is not
// waiting for a login.
var ErrNoLoginRequired = errors.New("the agent is not waiting for a login")

// ErrUnconfirmed matches, with errors.Is, the error of a call whose request
// was written to the agent but never confirmed: the call timed out or was
// cancelled, or the agent exited first. The agent may have taken the
// request, so sending it again could carry it out twice. Adapters mark such
// errors with Unconfirmed.
var ErrUnconfirmed = errors.New("the agent didn't confirm the request")

// Unconfirmed marks err, why a request written to the agent wasn't
// confirmed, as ErrUnconfirmed. The message stays err's, and errors.Is
// still finds err.
func Unconfirmed(err error) error {
	if err == nil {
		return nil
	}
	return unconfirmedError{err}
}

type unconfirmedError struct{ err error }

func (e unconfirmedError) Error() string        { return e.err.Error() }
func (e unconfirmedError) Unwrap() error        { return e.err }
func (e unconfirmedError) Is(target error) bool { return target == ErrUnconfirmed }

// RefusedError is an agent's refusal of a request, in its own words: the
// request reached it, and it won't do it (Pi's "Nothing to compact").
type RefusedError struct {
	Reason string
}

func (e *RefusedError) Error() string { return e.Reason }

// LoginRequiredError is returned by Adapter.Start when the agent can't work
// until the user logs in. The conversation waits and starts the agent again
// instead of giving up.
type LoginRequiredError struct {
	Login agentstate.LoginRequired
}

func (e *LoginRequiredError) Error() string {
	return fmt.Sprintf("%s Log in with `%s`.", e.Login.Message, e.Login.Command)
}

// Compacter is implemented by adapters whose agent compacts its context on
// request: it summarizes the conversation so far to free the context
// window. agentstate.Capabilities.Compact reports it.
type Compacter interface {
	// Compact compacts the context. It returns once the agent took the
	// request; an agent may report the compaction as a turn.
	Compact(ctx context.Context) error
}

// Image is an image attached to a message, read from a local file.
type Image struct {
	Path      string
	MediaType string
}

// Input is a user message.
type Input struct {
	Text   string
	Images []Image
}

// StopReason is why a turn ended, as an adapter reports it in TurnResult.
type StopReason string

const (
	StopReasonEndTurn     StopReason = "end_turn"
	StopReasonInterrupted StopReason = "interrupted"
	StopReasonError       StopReason = "error"
	StopReasonMaxTokens   StopReason = "max_tokens"
	// StopReasonHandled: the agent handled the message without a model
	// turn, e.g. a Pi extension command.
	StopReasonHandled StopReason = "handled"
	// StopReasonLimited: the account reached a usage limit; TurnResult.Error
	// is what the agent said.
	StopReasonLimited StopReason = "limited"
	// StopReasonTransient: the turn failed on an upstream error that may
	// pass (an HTTP 5xx or 429, an overloaded API, a dropped connection)
	// after the agent's own retries; TurnResult.Error is what the agent
	// said. The conversation tries the turn again later (Policy.RetryDelays).
	StopReasonTransient StopReason = "transient"
)

// RedactedAnswer replaces the answers to secret questions everywhere but
// in what the agent gets.
const RedactedAnswer = "[redacted]"

// TurnResult describes how a turn ended.
type TurnResult struct {
	// StopReason is the agent's reason, one of the StopReason constants.
	StopReason StopReason
	// Error is set when the turn failed.
	Error string
	// ResetsAt is when the limit that stopped a limited turn resets, if
	// the agent says. Without it the conversation takes the reset of the
	// limits the agent reported as reached.
	ResetsAt *time.Time
	// APIStatus is the HTTP status of the upstream error that failed a
	// transient turn, if the agent says; 0 otherwise.
	APIStatus int
}

// Sink receives an adapter's events. Its methods may be called from any
// goroutine but must not be called concurrently for one adapter; adapters
// call them from their read loop.
//
// Neither side holds a lock while it calls the other: an adapter calls the
// Sink with its own locks released, and the conversation calls its adapter
// with the conversation's lock released (a Sink method takes it). Either
// rule alone rules out a deadlock between the two; with both, each side can
// be changed without looking at the other, and an adapter's methods don't
// wait while the conversation handles an event. nativetest.Sink's
// CheckUnlocked tests an adapter's side.
//
// A Sink copies what it keeps: once a call returned, the adapter may
// change what it passed (the Content of a RichMessage, say).
type Sink interface {
	// TurnStarted reports that the agent began working on a turn.
	TurnStarted()
	// TurnCompleted reports that the turn ended. Requests still open are
	// closed by the conversation.
	TurnCompleted(TurnResult)
	// RichMessage reports the current full state of a message. Sending the
	// same MessageID and Role again replaces the earlier state, so adapters
	// send the whole message as it grows, not deltas. A conversation
	// outlives its adapters, so a MessageID must not repeat one that an
	// earlier adapter of the same Factory used.
	RichMessage(transcript.RichMessage)
	// SessionEvents reports timeline events.
	SessionEvents([]transcript.SessionEvent)
	// RequestOpened reports a new request the agent waits on. Opening a
	// request with a known ID replaces it, so like MessageIDs, request IDs
	// must not repeat those of an earlier adapter of the same Factory.
	RequestOpened(agentstate.Request)
	// RequestClosed reports that the agent no longer waits on a request it
	// opened (it was answered elsewhere, timed out or was cancelled).
	// Requests still open when the turn ends needn't be reported:
	// TurnCompleted closes them.
	RequestClosed(id string)
	// SessionName reports the name the agent gave the session.
	SessionName(string)
	// SessionRef reports the identifier needed to resume the session.
	SessionRef(string)
	// LoginRequired reports that the agent lost its login (e.g. an expired
	// token) while running.
	LoginRequired(agentstate.LoginRequired)
	// Error reports a problem the user should know about.
	Error(message string, level conv.ErrorLevel)
}

// Adapter drives one agent process through its structured protocol.
type Adapter interface {
	// Start launches the agent and opens its session (or resumes the one
	// the adapter was created with). It returns once the agent accepts
	// turns. ctx bounds only the startup: the agent keeps running after it
	// ends, until Close. Events are delivered to sink until the process
	// exits.
	Start(ctx context.Context, sink Sink) error
	// StartTurn sends a user message as a new turn.
	StartTurn(ctx context.Context, input Input) error
	// Steer adds a user message to the turn that is running.
	Steer(ctx context.Context, input Input) error
	// Interrupt stops the turn that is running. The turn ends with
	// TurnCompleted once the agent confirms.
	Interrupt(ctx context.Context) error
	// Respond answers a request reported with Sink.RequestOpened.
	Respond(ctx context.Context, requestID string, response agentstate.Response) error
	// Capabilities reports the optional operations the adapter supports.
	// The conversation reads it once Start returned.
	Capabilities() agentstate.Capabilities
	// Wait blocks until the agent process exits.
	Wait() error
	// Close stops the agent process. An agent that keeps a session log is
	// interrupted first if it works on a turn, and given a bounded time to
	// end it, so the log doesn't end in a tool call without result.
	Close() error
}

// Resume says which session an adapter opens.
type Resume struct {
	// Ref is a value an earlier adapter reported with Sink.SessionRef, or
	// empty for a new session.
	Ref string
	// Fork opens a new session that starts as a copy of Ref's, which goes
	// on unchanged. The adapter reports the new session's ref.
	Fork bool
}

// Factory creates an adapter that opens the session resume says.
type Factory func(resume Resume) (Adapter, error)
