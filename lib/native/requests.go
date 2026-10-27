package native

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/coder/quartz"
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

// requestBook holds a conversation's requests: the pending ones and the
// most recently closed, in the order they opened.
type requestBook struct {
	clock    quartz.Clock
	requests []*RequestState
	// answering holds the requests whose answer is being delivered, so a
	// second answer can't be sent at the same time, and those whose answer
	// went out unconfirmed.
	answering map[string]answer
}

// answer is the answer on its way to a request.
type answer struct {
	// closed is a close that came while it was being delivered, which
	// applies if the delivery fails.
	closed requestClose
	// unconfirmed: the delivery ended without the agent confirming it
	// (ErrUnconfirmed). The agent may have it, so no other answer may
	// follow; the request stays pending until the agent closes it or the
	// turn ends.
	unconfirmed bool
}

// requestClose is how a request closes: its new status, and how it can
// still be answered.
type requestClose struct {
	status     RequestStatus
	capability ResponseCapability
}

func newRequestBook(clock quartz.Clock) requestBook {
	return requestBook{clock: clock, answering: make(map[string]answer)}
}

// open adds a request the agent waits on, or replaces the one with its ID,
// and returns its state.
func (b *requestBook) open(req Request) RequestState {
	if req.CreatedAt.IsZero() {
		req.CreatedAt = b.clock.Now()
	}
	if req.Capability == "" {
		req.Capability = ResponseLive
	}
	state := &RequestState{Request: req, Status: RequestStatusPending}
	if existing := b.find(req.ID); existing != nil {
		*existing = *state
	} else {
		b.requests = append(b.requests, state)
	}
	return *state
}

func (b *requestBook) find(id string) *RequestState {
	for _, r := range b.requests {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// all returns the requests, oldest first.
func (b *requestBook) all() []RequestState {
	out := make([]RequestState, 0, len(b.requests))
	for _, r := range b.requests {
		out = append(out, *r)
	}
	return out
}

// pending returns the requests the agent waits on.
func (b *requestBook) pending() []RequestState {
	var out []RequestState
	for _, r := range b.requests {
		if r.Status == RequestStatusPending {
			out = append(out, *r)
		}
	}
	return out
}

// waitingOnUser reports whether a request waits for an answer that isn't
// on its way.
func (b *requestBook) waitingOnUser() bool {
	for _, r := range b.requests {
		if _, answering := b.answering[r.ID]; r.Status == RequestStatusPending && !answering {
			return true
		}
	}
	return false
}

// close closes a pending request and reports whether it did. While an
// answer to the request is being delivered, endAnswer closes it instead:
// as resolved if the delivery works, otherwise as the first close asked
// for meanwhile. A request whose answer went out unconfirmed closes now.
func (b *requestBook) close(r *RequestState, status RequestStatus, capability ResponseCapability) bool {
	if r.Status != RequestStatusPending {
		return false
	}
	if a, answering := b.answering[r.ID]; answering {
		if !a.unconfirmed {
			if a.closed.status == "" {
				a.closed = requestClose{status: status, capability: capability}
				b.answering[r.ID] = a
			}
			return false
		}
		delete(b.answering, r.ID)
	}
	now := b.clock.Now()
	r.Status = status
	r.Capability = capability
	r.ClosedAt = &now
	b.trim()
	return true
}

// expire closes the pending requests and returns those it closed. If only
// their turn ended, questions can still be answered with a message; once
// the agent is gone, nothing can be answered.
func (b *requestBook) expire(agentGone bool) []RequestState {
	var closed []RequestState
	// Closing a request trims b.requests.
	for _, r := range slices.Clone(b.requests) {
		capability := ResponseNotResumable
		if !agentGone {
			capability = closedCapability(r.Kind)
		}
		if b.close(r, RequestStatusExpired, capability) {
			closed = append(closed, *r)
		}
	}
	return closed
}

// answerable reports whether r waits for an answer and none is on its
// way.
func (b *requestBook) answerable(r *RequestState) bool {
	_, answering := b.answering[r.ID]
	return r.Status == RequestStatusPending && !answering
}

// beginAnswer marks the request with id as being answered.
func (b *requestBook) beginAnswer(id string) {
	b.answering[id] = answer{}
}

// endAnswer ends the delivery of an answer to r: it resolves r with
// response if the delivery worked (response isn't nil), otherwise applies
// a close that came meanwhile. Without one, an unconfirmed delivery keeps
// r from taking another answer until it closes. It reports whether r
// closed.
func (b *requestBook) endAnswer(r *RequestState, response *Response, unconfirmed bool) bool {
	a := b.answering[r.ID]
	delete(b.answering, r.ID)
	switch {
	case response != nil:
		r.Response = response
		return b.close(r, RequestStatusResolved, r.Capability)
	case a.closed.status != "":
		// The agent stopped waiting while the answer was on its way.
		return b.close(r, a.closed.status, a.closed.capability)
	case unconfirmed:
		b.answering[r.ID] = answer{unconfirmed: true}
	}
	return false
}

// trim drops the closed requests beyond MaxClosedRequests that closed
// first.
func (b *requestBook) trim() {
	drop := ExcessClosed(len(b.requests), func(i int) *RequestState { return b.requests[i] })
	for n, i := range drop {
		// The n dropped before were in front of it.
		b.requests = slices.Delete(b.requests, i-n, i-n+1)
	}
}

// Requests returns the pending requests and the most recently closed ones,
// oldest first.
func (c *Conversation) Requests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests.all()
}

// PendingRequests returns the requests the agent is waiting on.
func (c *Conversation) PendingRequests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests.pending()
}

// Request returns one request by ID.
func (c *Conversation) Request(id string) (RequestState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.requests.find(id); r != nil {
		return *r, true
	}
	return RequestState{}, false
}

// Respond answers a pending request. It returns ErrRequestNotFound if the
// request is not pending, or an answer to it is on its way, and
// ErrInvalidResponse if the response doesn't fit. An answer the adapter
// wrote but the agent didn't confirm (ErrUnconfirmed) may still reach the
// agent, so the request takes no other: it stays pending until the agent
// closes it or the turn ends.
func (c *Conversation) Respond(ctx context.Context, id string, response Response) (RequestState, error) {
	c.mu.Lock()
	r := c.requests.find(id)
	if r == nil {
		c.mu.Unlock()
		return RequestState{}, ErrRequestNotFound
	}
	adapter := c.adapter
	err := ErrRequestNotFound
	if c.requests.answerable(r) {
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
	c.requests.beginAnswer(id)
	c.mu.Unlock()

	err = adapter.Respond(ctx, id, response)

	c.mu.Lock()
	defer c.mu.Unlock()
	var resolved *Response
	if err == nil {
		redacted := redactSecrets(r.Request, response)
		resolved = &redacted
	}
	if c.requests.endAnswer(r, resolved, errors.Is(err, ErrUnconfirmed)) {
		c.cfg.Observer.RequestUpdated(*r)
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

// closeRequestLocked closes a pending request and publishes it.
func (c *Conversation) closeRequestLocked(r *RequestState, status RequestStatus, capability ResponseCapability) {
	if c.requests.close(r, status, capability) {
		c.cfg.Observer.RequestUpdated(*r)
	}
}

// expireRequestsLocked closes the pending requests and publishes them.
func (c *Conversation) expireRequestsLocked(agentGone bool) {
	for _, r := range c.requests.expire(agentGone) {
		c.cfg.Observer.RequestUpdated(r)
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
