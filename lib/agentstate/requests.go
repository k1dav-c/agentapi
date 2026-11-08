package agentstate

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

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
	Options     []QuestionOption `json:"options,omitempty" nullable:"false" doc:"Suggested answers."`
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
	Options   []RequestOption `json:"options,omitempty" nullable:"false" doc:"Ways to answer an approval or plan request."`
	Questions []Question      `json:"questions,omitempty" nullable:"false" doc:"Questions of a question request."`
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

// RequestStatus is where a request is in its life.
type RequestStatus string

const (
	RequestStatusPending  RequestStatus = "pending"
	RequestStatusResolved RequestStatus = "resolved"
	// RequestStatusWithdrawn: the agent stopped waiting before it was
	// answered here.
	RequestStatusWithdrawn RequestStatus = "withdrawn"
	// RequestStatusExpired: the turn ended or the agent was restarted while
	// the request was open.
	RequestStatusExpired RequestStatus = "expired"
)

var RequestStatusValues = []RequestStatus{RequestStatusPending, RequestStatusResolved, RequestStatusWithdrawn, RequestStatusExpired}

func (s RequestStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "RequestStatus", RequestStatusValues)
}

// RequestState is a request together with what became of it.
type RequestState struct {
	Request
	Status   RequestStatus `json:"status" doc:"pending, resolved, withdrawn (the agent stopped waiting) or expired (the turn ended or the agent restarted)."`
	Response *Response     `json:"response,omitempty" doc:"The answer, once resolved."`
	ClosedAt *time.Time    `json:"closed_at,omitempty" doc:"When the request stopped being pending."`
}

// MaxClosedRequests is how many answered or expired requests are kept for
// clients that reconnect after a request was closed: the most recently
// closed ones.
const MaxClosedRequests = 50

// ExcessClosed returns the indexes, ascending, of the closed requests to
// drop so that MaxClosedRequests remain: those that closed first. state
// returns request i of n.
func ExcessClosed(n int, state func(i int) *RequestState) []int {
	var closed []int
	for i := range n {
		if state(i).Status != RequestStatusPending {
			closed = append(closed, i)
		}
	}
	excess := len(closed) - MaxClosedRequests
	if excess <= 0 {
		return nil
	}
	closedAt := func(i int) time.Time {
		if at := state(i).ClosedAt; at != nil {
			return *at
		}
		return time.Time{}
	}
	slices.SortStableFunc(closed, func(a, b int) int { return closedAt(a).Compare(closedAt(b)) })
	drop := closed[:excess]
	slices.Sort(drop)
	return drop
}
