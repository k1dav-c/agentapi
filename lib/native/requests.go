package native

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

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
// clients that reconnect after a request was closed.
const MaxClosedRequests = 50

// requestClose is how a request closes: its new status, and how it can
// still be answered.
type requestClose struct {
	status     RequestStatus
	capability ResponseCapability
}

// Requests returns the pending requests and the most recently closed ones,
// oldest first.
func (c *Conversation) Requests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RequestState, 0, len(c.requests))
	for _, r := range c.requests {
		out = append(out, *r)
	}
	return out
}

// PendingRequests returns the requests the agent is waiting on.
func (c *Conversation) PendingRequests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []RequestState
	for _, r := range c.requests {
		if r.Status == RequestStatusPending {
			out = append(out, *r)
		}
	}
	return out
}

// Request returns one request by ID.
func (c *Conversation) Request(id string) (RequestState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.findRequestLocked(id); r != nil {
		return *r, true
	}
	return RequestState{}, false
}

// Respond answers a pending request. It returns ErrRequestNotFound if the
// request is not pending and ErrInvalidResponse if the response doesn't fit.
func (c *Conversation) Respond(ctx context.Context, id string, response Response) (RequestState, error) {
	c.mu.Lock()
	r := c.findRequestLocked(id)
	if r == nil {
		c.mu.Unlock()
		return RequestState{}, ErrRequestNotFound
	}
	adapter := c.adapter
	_, answering := c.responding[id]
	err := ErrRequestNotFound
	if !answering && r.Status == RequestStatusPending {
		err = validateResponse(r.Request, response)
		if err == nil && adapter == nil {
			err = ErrNotStarted
		}
	}
	if err != nil {
		state := *r
		c.mu.Unlock()
		return state, err
	}
	c.responding[id] = requestClose{}
	c.mu.Unlock()

	err = adapter.Respond(ctx, id, response)

	c.mu.Lock()
	defer c.mu.Unlock()
	closed := c.responding[id]
	delete(c.responding, id)
	switch {
	case err == nil:
		resp := redactSecrets(r.Request, response)
		r.Response = &resp
		c.closeRequestLocked(r, RequestStatusResolved, r.Capability)
	case closed.status != "":
		// The agent stopped waiting while the answer was on its way.
		c.closeRequestLocked(r, closed.status, closed.capability)
	}
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	return *r, err
}

// redactSecrets replaces the answers to secret questions with RedactedAnswer
// for the stored and published request state. The agent gets the real
// answers.
func redactSecrets(req Request, resp Response) Response {
	redacted := false
	for _, q := range req.Questions {
		if !q.Secret || len(resp.Answers[q.ID]) == 0 {
			continue
		}
		if !redacted {
			resp.Answers = maps.Clone(resp.Answers)
			redacted = true
		}
		resp.Answers[q.ID] = []string{RedactedAnswer}
	}
	return resp
}

// validateResponse checks that a response answers the request.
func validateResponse(req Request, resp Response) error {
	switch req.Kind {
	case RequestKindApproval, RequestKindPlan:
		return validateOption(req, resp.OptionID)
	case RequestKindQuestion:
		if resp.OptionID != "" {
			return validateOption(req, resp.OptionID)
		}
		for _, q := range req.Questions {
			answers := resp.Answers[q.ID]
			if len(answers) == 0 {
				return fmt.Errorf("%w: question %q has no answer", ErrInvalidResponse, q.ID)
			}
			if len(answers) > 1 && !q.MultiSelect {
				return fmt.Errorf("%w: question %q takes one answer", ErrInvalidResponse, q.ID)
			}
			if q.FreeText || len(q.Options) == 0 {
				continue
			}
			for _, a := range answers {
				if !slices.ContainsFunc(q.Options, func(o QuestionOption) bool { return o.Label == a }) {
					return fmt.Errorf("%w: %q is not an option of question %q", ErrInvalidResponse, a, q.ID)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown request kind %q", ErrInvalidResponse, req.Kind)
	}
}

// validateOption checks that id is one of the request's options.
func validateOption(req Request, id string) error {
	if slices.ContainsFunc(req.Options, func(o RequestOption) bool { return o.ID == id }) {
		return nil
	}
	return fmt.Errorf("%w: unknown option %q", ErrInvalidResponse, id)
}

func (c *Conversation) findRequestLocked(id string) *RequestState {
	for _, r := range c.requests {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// closeRequestLocked closes a pending request and publishes it. While an
// answer to the request is being delivered, Respond closes it instead: as
// resolved if the delivery works, otherwise as the first close asked for
// meanwhile.
func (c *Conversation) closeRequestLocked(r *RequestState, status RequestStatus, capability ResponseCapability) {
	if r.Status != RequestStatusPending {
		return
	}
	if pending, answering := c.responding[r.ID]; answering {
		if pending.status == "" {
			c.responding[r.ID] = requestClose{status: status, capability: capability}
		}
		return
	}
	now := c.cfg.Clock.Now()
	r.Status = status
	r.Capability = capability
	r.ClosedAt = &now
	c.trimRequestsLocked()
	c.cfg.Observer.RequestUpdated(*r)
}

// expireRequestsLocked closes the pending requests. If only their turn
// ended, questions can still be answered with a message; once the agent is
// gone, nothing can be answered.
func (c *Conversation) expireRequestsLocked(agentGone bool) {
	// Closing a request trims c.requests.
	for _, r := range slices.Clone(c.requests) {
		capability := ResponseNotResumable
		if !agentGone {
			capability = closedCapability(r.Kind)
		}
		c.closeRequestLocked(r, RequestStatusExpired, capability)
	}
}

// closedCapability is how a request the agent stopped waiting for can still
// be answered: a question with a new message, an approval or a plan not at
// all.
func closedCapability(kind RequestKind) ResponseCapability {
	if kind == RequestKindQuestion {
		return ResponseMessage
	}
	return ResponseNotResumable
}

func (c *Conversation) trimRequestsLocked() {
	closed := 0
	for _, r := range c.requests {
		if r.Status != RequestStatusPending {
			closed++
		}
	}
	if closed <= MaxClosedRequests {
		return
	}
	drop := closed - MaxClosedRequests
	kept := c.requests[:0]
	for _, r := range c.requests {
		if drop > 0 && r.Status != RequestStatusPending {
			drop--
			continue
		}
		kept = append(kept, r)
	}
	c.requests = kept
}
