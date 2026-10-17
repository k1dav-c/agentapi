// Package native drives coding agents through their own structured protocols
// (Claude Code's stream-json, Codex's app-server, Pi's RPC mode) instead of a
// terminal. An Adapter speaks one agent's protocol; Conversation turns its
// events into the conversation state the HTTP API serves.
package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/util"
)

var (
	// ErrUnsupported is returned for an operation the agent's protocol
	// doesn't offer (see Capabilities).
	ErrUnsupported = errors.New("not supported by this agent")
	// ErrNoActiveTurn is returned by Steer and Interrupt when the agent is
	// not working on a turn.
	ErrNoActiveTurn = errors.New("the agent is not working on a turn")
	// ErrTurnActive is returned by StartTurn while a turn is running.
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

// LoginRequired tells the user how to log the agent in. Native mode has no
// terminal, so the login happens in one the user opens themselves.
type LoginRequired struct {
	Agent   string `json:"agent" doc:"Agent that needs a login, e.g. Claude Code."`
	Message string `json:"message" doc:"What is wrong, e.g. Claude Code is not logged in."`
	Command string `json:"command" doc:"Command to run in a terminal to log in, e.g. claude auth login."`
	Detail  string `json:"detail,omitempty" doc:"More help, or the agent's own error message."`
	Host    string `json:"host,omitempty" doc:"Machine to run the command on: the one AgentAPI runs on."`
	User    string `json:"user,omitempty" doc:"User to run the command as: the one AgentAPI runs as."`
}

// LoginRequiredError is returned by Adapter.Start when the agent can't work
// until the user logs in. The conversation waits and starts the agent again
// instead of giving up.
type LoginRequiredError struct {
	Login LoginRequired
}

func (e *LoginRequiredError) Error() string {
	return fmt.Sprintf("%s Log in with `%s`.", e.Login.Message, e.Login.Command)
}

// Capabilities says which optional operations an adapter supports. Callers
// decide what to offer from these, never from the agent type.
type Capabilities struct {
	Steer     bool `json:"steer" doc:"A message can be added to the turn the agent is working on."`
	Interrupt bool `json:"interrupt" doc:"The turn the agent is working on can be stopped."`
	Resume    bool `json:"resume" doc:"The agent's session can be resumed after AgentAPI restarts."`
	Images    bool `json:"images" doc:"Messages can carry images."`
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

// RequestKind is what the agent is asking for.
type RequestKind string

const (
	// RequestKindApproval asks to allow a tool call (run a command, edit a
	// file, call an MCP tool).
	RequestKindApproval RequestKind = "approval"
	// RequestKindQuestion asks the user one or more questions (Claude's
	// AskUserQuestion, Codex's request_user_input, a Pi extension dialog).
	RequestKindQuestion RequestKind = "question"
	// RequestKindPlan asks to approve a plan before the agent carries it out
	// (Claude's ExitPlanMode).
	RequestKindPlan RequestKind = "plan"
)

var RequestKindValues = []RequestKind{RequestKindApproval, RequestKindQuestion, RequestKindPlan}

func (k RequestKind) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "RequestKind", RequestKindValues)
}

// OptionKind classifies an option so clients can style it and so a generic
// reply ("yes", "no") can be mapped onto it.
type OptionKind string

const (
	OptionKindAllow       OptionKind = "allow"
	OptionKindAllowAlways OptionKind = "allow_always"
	OptionKindDeny        OptionKind = "deny"
	OptionKindCancel      OptionKind = "cancel"
	OptionKindOther       OptionKind = "other"
)

var OptionKindValues = []OptionKind{OptionKindAllow, OptionKindAllowAlways, OptionKindDeny, OptionKindCancel, OptionKindOther}

func (k OptionKind) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "OptionKind", OptionKindValues)
}

// ResponseCapability says how a request can still be answered.
type ResponseCapability string

const (
	// ResponseLive: the agent is waiting for the answer right now.
	ResponseLive ResponseCapability = "live"
	// ResponseMessage: the agent stopped waiting; answer it with a new
	// message instead.
	ResponseMessage ResponseCapability = "message"
	// ResponseNotResumable: the request can't be answered any more: the
	// agent that asked is gone (it exited or was restarted), or it stopped
	// waiting for an approval or a plan.
	ResponseNotResumable ResponseCapability = "not_resumable"
)

var ResponseCapabilityValues = []ResponseCapability{ResponseLive, ResponseMessage, ResponseNotResumable}

func (c ResponseCapability) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "ResponseCapability", ResponseCapabilityValues)
}

// RequestOption is one way to answer an approval or plan request. ID is the
// agent's own identifier for the decision, passed back unchanged.
type RequestOption struct {
	ID          string     `json:"id" doc:"Identifier to send back in the response."`
	Label       string     `json:"label" doc:"Text to show for the option."`
	Description string     `json:"description,omitempty" doc:"Longer explanation of the option."`
	Kind        OptionKind `json:"kind" doc:"What choosing the option does: allow, allow_always, deny, cancel or other."`
}

// QuestionOption is a suggested answer to a question.
type QuestionOption struct {
	Label       string `json:"label" doc:"The answer."`
	Description string `json:"description,omitempty" doc:"Explanation of the answer."`
}

// Question is one question of a question request.
type Question struct {
	ID          string           `json:"id" doc:"Identifier of the question; key of its answer in the response."`
	Header      string           `json:"header,omitempty" doc:"Short label for the question."`
	Question    string           `json:"question" doc:"The question."`
	Options     []QuestionOption `json:"options,omitempty" doc:"Suggested answers."`
	MultiSelect bool             `json:"multi_select,omitempty" doc:"More than one option may be chosen."`
	// FreeText allows an answer that is not one of the options.
	FreeText bool `json:"free_text,omitempty" doc:"An answer other than the options is allowed."`
	Secret   bool `json:"secret,omitempty" doc:"The answer is a secret and should not be shown."`
}

// Request is something the agent is waiting for the user to decide.
type Request struct {
	ID        string          `json:"id" doc:"Identifier of the request."`
	Kind      RequestKind     `json:"kind" doc:"approval, question or plan."`
	Title     string          `json:"title" doc:"One-line summary, e.g. the command to run."`
	Detail    string          `json:"detail,omitempty" doc:"More context: the plan, the diff, the reason given by the agent."`
	ToolName  string          `json:"tool_name,omitempty" doc:"Tool the request is about."`
	ToolUseID string          `json:"tool_use_id,omitempty" doc:"Tool call the request is about, matching tool_use_id in rich messages."`
	Input     json.RawMessage `json:"input,omitempty" doc:"The tool call's input."`
	Options   []RequestOption `json:"options,omitempty" doc:"Ways to answer an approval or plan request."`
	Questions []Question      `json:"questions,omitempty" doc:"Questions of a question request."`
	// Feedback means a deny/reject answer can carry text for the agent
	// (e.g. what to change in a plan).
	Feedback   bool               `json:"feedback,omitempty" doc:"A text explanation can accompany the answer."`
	Capability ResponseCapability `json:"response_capability" doc:"live: the agent is waiting; message: answer with a new message; not_resumable: can no longer be answered."`
	CreatedAt  time.Time          `json:"created_at" doc:"When the agent asked."`
}

// Response answers a Request.
type Response struct {
	// OptionID picks one of Request.Options.
	OptionID string `json:"option_id,omitempty" doc:"Chosen option of an approval or plan request."`
	// Answers holds the answers of a question request, keyed by question
	// ID. Each answer is a list of chosen option labels or free text.
	Answers map[string][]string `json:"answers,omitempty" doc:"Answers of a question request, keyed by question id. Each answer lists the chosen option labels, or holds free text."`
	// Text is feedback sent with the answer (e.g. why a plan was rejected).
	Text string `json:"text,omitempty" doc:"Feedback for the agent, e.g. what to change in a plan."`
}

// Stop reasons adapters report in TurnResult.StopReason.
const (
	StopReasonEndTurn     = "end_turn"
	StopReasonInterrupted = "interrupted"
	StopReasonError       = "error"
	StopReasonMaxTokens   = "max_tokens"
	// StopReasonHandled: the agent handled the message without a model
	// turn, e.g. a Pi extension command.
	StopReasonHandled = "handled"
)

// RedactedAnswer replaces the answers to secret questions everywhere but
// in what the agent gets.
const RedactedAnswer = "[redacted]"

// TurnResult describes how a turn ended.
type TurnResult struct {
	// StopReason is the agent's reason, one of the StopReason constants.
	StopReason string
	// Error is set when the turn failed.
	Error string
}

// Sink receives an adapter's events. Its methods may be called from any
// goroutine but must not be called concurrently for one adapter; adapters
// call them from their read loop.
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
	RichMessage(jsonlwatcher.RichMessage)
	// SessionEvents reports timeline events.
	SessionEvents([]jsonlwatcher.SessionEvent)
	// RequestOpened reports a new request the agent waits on. Opening a
	// request with a known ID replaces it, so like MessageIDs, request IDs
	// must not repeat those of an earlier adapter of the same Factory.
	RequestOpened(Request)
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
	LoginRequired(LoginRequired)
	// Error reports a problem the user should know about.
	Error(message string, level st.ErrorLevel)
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
	Respond(ctx context.Context, requestID string, response Response) error
	// Capabilities reports the optional operations the adapter supports.
	// The conversation reads it once Start returned.
	Capabilities() Capabilities
	// Wait blocks until the agent process exits.
	Wait() error
	// Close stops the agent process.
	Close() error
}

// Factory creates an adapter. resumeRef is a value an earlier adapter
// reported with Sink.SessionRef, or empty for a new session.
type Factory func(resumeRef string) (Adapter, error)
