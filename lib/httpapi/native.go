package httpapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/statedb"
)

// nativeCallTimeout bounds a call into a native adapter made while the
// server lock is held.
const nativeCallTimeout = 30 * time.Second

// nativeObserver passes a native conversation's structured state to the
// event emitter.
type nativeObserver struct {
	emitter *EventEmitter
	// persister stores the session ref, if state is persisted.
	persister Persister
	// storeImages saves tool result images and replaces their data with
	// URLs. Set once the server exists.
	storeImages func(*jsonlwatcher.RichMessage)
}

var _ native.Observer = (*nativeObserver)(nil)

func (o *nativeObserver) Started(caps native.Capabilities) { o.emitter.SetCapabilities(caps) }

func (o *nativeObserver) LoginRequired(login *native.LoginRequired) {
	o.emitter.SetLoginRequired(login)
}

func (o *nativeObserver) RichMessage(msg jsonlwatcher.RichMessage) {
	if o.storeImages != nil {
		o.storeImages(&msg)
	}
	o.emitter.EmitRichMessageSnapshot(msg)
}

func (o *nativeObserver) SessionEvents(events []jsonlwatcher.SessionEvent) {
	o.emitter.EmitSessionEvents(events)
}

func (o *nativeObserver) SessionName(name string) { o.emitter.SetSessionName(name) }

// SessionRef is not published; the session ID shown to clients is
// AgentAPI's own. It is stored to resume the session after a restart.
func (o *nativeObserver) SessionRef(ref string) {
	if o.persister != nil {
		o.persister.SetMeta(statedb.MetaSessionRef, ref)
	}
}

func (o *nativeObserver) RequestUpdated(state native.RequestState) { o.emitter.EmitRequest(state) }

// requireNative returns an error for transports other than native.
func (s *Server) requireNative() error {
	if s.native == nil {
		return huma.Error400BadRequest("this endpoint needs the native transport (--transport native)")
	}
	return nil
}

// createNativeMessageLocked delivers a user message over the native
// transport according to mode. Caller holds s.mu.
func (s *Server) createNativeMessageLocked(content string, mode DispatchMode, resp *MessageResponse) error {
	ctx, cancel := context.WithTimeout(s.shutdownCtx, nativeCallTimeout)
	defer cancel()
	input := native.InputFromText(content)
	caps := s.native.Capabilities()
	busy := !s.native.Idle()

	switch mode {
	case DispatchSteer:
		if !caps.Steer {
			return huma.Error400BadRequest("this agent can't add a message to a running turn")
		}
		if s.native.TurnActive() {
			if err := s.native.Steer(ctx, input); err != nil {
				if !errors.Is(err, native.ErrNoActiveTurn) {
					return fmt.Errorf("failed to steer the agent: %w", err)
				}
				// The turn ended in the meantime; send a new turn below.
			} else {
				resp.Body.Ok = true
				resp.Body.Steered = true
				return nil
			}
		}
	case DispatchInterrupt:
		if !caps.Interrupt {
			return huma.Error400BadRequest("this agent's turns can't be interrupted")
		}
		if s.native.TurnActive() {
			if err := s.native.Interrupt(ctx); err != nil && !errors.Is(err, native.ErrNoActiveTurn) {
				return fmt.Errorf("failed to interrupt the agent: %w", err)
			}
			// The message goes first once the interrupted turn has ended.
			s.enqueueMessageLocked(content, true)
			resp.Body.Ok = true
			resp.Body.Queued = true
			return nil
		}
	case DispatchQueue:
	}

	if len(s.messageQueue) > 0 || busy {
		s.enqueueMessageLocked(content, false)
		resp.Body.Ok = true
		resp.Body.Queued = true
		return nil
	}
	if err := s.native.StartTurn(ctx, input); err != nil {
		if errors.Is(err, st.ErrMessageValidationChanging) {
			s.enqueueMessageLocked(content, false)
			resp.Body.Ok = true
			resp.Body.Queued = true
			return nil
		}
		return fmt.Errorf("failed to send message: %w", err)
	}
	resp.Body.Ok = true
	return nil
}

// dispatchNativeQueuedLocked sends the next queued message once the agent
// is idle. Caller holds s.mu.
func (s *Server) dispatchNativeQueuedLocked() {
	if len(s.messageQueue) == 0 || !s.native.Idle() {
		return
	}
	next := s.messageQueue[0]
	ctx, cancel := context.WithTimeout(s.shutdownCtx, nativeCallTimeout)
	defer cancel()
	err := s.native.StartTurn(ctx, native.InputFromText(next.Content))
	if errors.Is(err, st.ErrMessageValidationChanging) {
		return
	}
	s.messageQueue = s.messageQueue[1:]
	if err != nil {
		// The adapter refused the turn outright; resending would fail the
		// same way.
		s.logger.Error("Failed to send queued message", "queueId", next.ID, "error", err)
		s.emitter.EmitError(fmt.Sprintf("Dropped queued message: %v", err), st.ErrorLevelError)
	}
}

func (s *Server) getRequests(_ context.Context, _ *struct{}) (*RequestsResponse, error) {
	resp := &RequestsResponse{}
	resp.Body.Requests = s.emitter.Requests()
	if resp.Body.Requests == nil {
		resp.Body.Requests = []native.RequestState{}
	}
	return resp, nil
}

func (s *Server) getRequest(_ context.Context, input *RequestPath) (*RequestResponse, error) {
	for _, r := range s.emitter.Requests() {
		if r.ID == input.ID {
			return &RequestResponse{Body: r}, nil
		}
	}
	return nil, huma.Error404NotFound("request not found")
}

func (s *Server) respondRequest(ctx context.Context, input *RespondRequest) (*RequestResponse, error) {
	if err := s.requireNative(); err != nil {
		return nil, err
	}
	// Serialize with message delivery, restarts and handoff replies.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.respondRequestLocked(ctx, input.ID, input.Body)
}

func (s *Server) respondRequestLocked(ctx context.Context, id string, response native.Response) (*RequestResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, nativeCallTimeout)
	defer cancel()
	state, err := s.native.Respond(ctx, id, response)
	switch {
	case err == nil:
		return &RequestResponse{Body: state}, nil
	case errors.Is(err, native.ErrRequestNotFound):
		if state.ID == "" {
			return nil, huma.Error404NotFound("request not found")
		}
		return nil, huma.Error409Conflict(fmt.Sprintf("request is %s, not pending", state.Status))
	case errors.Is(err, native.ErrInvalidResponse):
		return nil, huma.Error400BadRequest(err.Error())
	default:
		return nil, fmt.Errorf("failed to deliver the response: %w", err)
	}
}

func (s *Server) retryLogin(ctx context.Context, _ *struct{}) (*InterruptResponse, error) {
	if err := s.requireNative(); err != nil {
		return nil, err
	}
	if err := s.native.RetryLogin(ctx); err != nil {
		if errors.Is(err, native.ErrNoLoginRequired) {
			return nil, huma.Error409Conflict("the agent is not waiting for a login")
		}
		return nil, fmt.Errorf("failed to check the login: %w", err)
	}
	resp := &InterruptResponse{}
	resp.Body.Ok = true
	return resp, nil
}

func (s *Server) interrupt(ctx context.Context, _ *struct{}) (*InterruptResponse, error) {
	if err := s.requireNative(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, nativeCallTimeout)
	defer cancel()
	switch err := s.native.Interrupt(ctx); {
	case errors.Is(err, native.ErrUnsupported):
		return nil, huma.Error400BadRequest("this agent's turns can't be interrupted")
	case errors.Is(err, native.ErrNoActiveTurn):
		return nil, huma.Error409Conflict("the agent is not working on a turn")
	case err != nil:
		return nil, fmt.Errorf("failed to interrupt the agent: %w", err)
	}
	resp := &InterruptResponse{}
	resp.Body.Ok = true
	return resp, nil
}

// steerQueuedMessage adds a queued message to the running turn instead of
// waiting for the turn to end.
func (s *Server) steerQueuedMessage(_ context.Context, input *SteerQueuedMessageRequest) (*QueueMutationResponse, error) {
	if err := s.requireNative(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := slices.IndexFunc(s.messageQueue, func(m QueuedMessage) bool { return m.ID == input.ID })
	if idx < 0 {
		return nil, huma.Error404NotFound("queued message not found")
	}
	ctx, cancel := context.WithTimeout(s.shutdownCtx, nativeCallTimeout)
	defer cancel()
	switch err := s.native.Steer(ctx, native.InputFromText(s.messageQueue[idx].Content)); {
	case errors.Is(err, native.ErrUnsupported):
		return nil, huma.Error400BadRequest("this agent can't add a message to a running turn")
	case errors.Is(err, native.ErrNoActiveTurn):
		return nil, huma.Error409Conflict("the agent is not working on a turn; the message will be sent next")
	case err != nil:
		return nil, fmt.Errorf("failed to steer the agent: %w", err)
	}
	s.messageQueue = slices.Delete(s.messageQueue, idx, idx+1)
	resp := &QueueMutationResponse{}
	resp.Body.Ok = true
	return resp, nil
}

// reorderQueue puts the queued messages in the given order.
func (s *Server) reorderQueue(_ context.Context, input *ReorderQueueRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := input.Body.IDs
	if len(ids) != len(s.messageQueue) {
		return nil, huma.Error409Conflict("the queue changed; list every queued message once")
	}
	byID := make(map[int]QueuedMessage, len(s.messageQueue))
	for _, m := range s.messageQueue {
		byID[m.ID] = m
	}
	reordered := make([]QueuedMessage, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok {
			return nil, huma.Error409Conflict("the queue changed; list every queued message once")
		}
		delete(byID, id)
		reordered = append(reordered, m)
	}
	s.messageQueue = reordered
	resp := &QueueMutationResponse{}
	resp.Body.Ok = true
	return resp, nil
}

// pendingNativeHandoffLocked describes the oldest pending native request for
// the human reply protocol. Caller holds s.mu.
func (s *Server) pendingNativeHandoffLocked() (native.RequestState, string, bool) {
	pending := s.native.PendingRequests()
	if len(pending) == 0 {
		return native.RequestState{}, "", false
	}
	r := pending[0]
	var sb strings.Builder
	sb.WriteString(r.Title)
	if r.Detail != "" {
		sb.WriteString("\n\n" + r.Detail)
	}
	for i, opt := range r.Options {
		fmt.Fprintf(&sb, "\n%d. %s", i+1, opt.Label)
	}
	for _, q := range r.Questions {
		sb.WriteString("\n\n" + q.Question)
		for i, opt := range q.Options {
			fmt.Fprintf(&sb, "\n%d. %s", i+1, opt.Label)
		}
	}
	return r, sb.String(), true
}

// nativeHandoffResponse turns a human reply into a response to r: a number
// picks an option, other text answers a single question or is feedback for
// the deny option.
func nativeHandoffResponse(r native.RequestState, reply string) (native.Response, bool) {
	reply = strings.TrimSpace(reply)
	if n, err := strconv.Atoi(reply); err == nil {
		if len(r.Options) > 0 && n >= 1 && n <= len(r.Options) {
			return native.Response{OptionID: r.Options[n-1].ID}, true
		}
		if len(r.Questions) == 1 && n >= 1 && n <= len(r.Questions[0].Options) {
			q := r.Questions[0]
			return native.Response{Answers: map[string][]string{q.ID: {q.Options[n-1].Label}}}, true
		}
		return native.Response{}, false
	}
	switch {
	case len(r.Questions) == 1 && (r.Questions[0].FreeText || len(r.Questions[0].Options) == 0):
		return native.Response{Answers: map[string][]string{r.Questions[0].ID: {reply}}}, true
	case r.Feedback:
		for _, opt := range r.Options {
			if opt.Kind == native.OptionKindDeny {
				return native.Response{OptionID: opt.ID, Text: reply}, true
			}
		}
	}
	return native.Response{}, false
}

func (s *Server) registerNativeRoutes() {
	huma.Get(s.api, "/requests", s.getRequests, func(o *huma.Operation) {
		o.Tags = []string{"Requests"}
		o.Summary = "List requests"
		o.Description = "Returns the requests the agent is waiting on (tool approvals, questions, plans) and the most recently closed ones. Native transport only; other transports return an empty list. Changes are also sent as `request_update` events on /events."
	})
	huma.Get(s.api, "/requests/{id}", s.getRequest, func(o *huma.Operation) {
		o.Tags = []string{"Requests"}
		o.Summary = "Get a request"
		o.Errors = []int{404}
	})
	huma.Post(s.api, "/requests/{id}/respond", s.respondRequest, func(o *huma.Operation) {
		o.Tags = []string{"Requests"}
		o.Summary = "Answer a request"
		o.Description = "Answers a pending request. Approvals and plans take `option_id` (one of the request's options, optionally with `text` as feedback); questions take `answers` keyed by question id. Returns 409 if the request is no longer pending (already answered, withdrawn or expired) and 400 if the answer doesn't fit the request. A request is answered at most once."
		o.Errors = []int{400, 404, 409}
	})
	huma.Post(s.api, "/login/retry", s.retryLogin, func(o *huma.Operation) {
		o.Summary = "Check the agent's login again"
		o.Description = "While `login_required` is reported in GET /status, checks again now whether the user logged in (AgentAPI also checks every few seconds while the agent waits at startup). If the agent lost its login while running, it is restarted and resumes its session. Native transport only; returns 409 if no login is needed."
		o.Errors = []int{400, 409}
	})
	huma.Post(s.api, "/interrupt", s.interrupt, func(o *huma.Operation) {
		o.Summary = "Interrupt the agent"
		o.Description = "Stops the turn the agent is working on. Native transport only, for agents with the interrupt capability."
		o.Errors = []int{400, 409}
	})
	huma.Post(s.api, "/queue/{id}/steer", s.steerQueuedMessage, func(o *huma.Operation) {
		o.Description = "Adds a queued message to the turn the agent is working on instead of waiting for it to end. Native transport only, for agents with the steer capability."
		o.Errors = []int{400, 404, 409}
	})
	huma.Put(s.api, "/queue", s.reorderQueue, func(o *huma.Operation) {
		o.Description = "Reorders the queued messages. The body must list every queued message id exactly once; otherwise 409 is returned."
		o.Errors = []int{409}
	})
}
