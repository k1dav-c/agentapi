package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/transcript"
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
	storeImages func(*transcript.RichMessage)
}

var _ native.Observer = (*nativeObserver)(nil)

func (o *nativeObserver) Started(caps native.Capabilities) { o.emitter.SetCapabilities(caps) }

func (o *nativeObserver) LoginRequired(login *native.LoginRequired) {
	o.emitter.SetLoginRequired(login)
}

func (o *nativeObserver) RichMessage(msg transcript.RichMessage) {
	if o.storeImages != nil {
		o.storeImages(&msg)
	}
	o.emitter.EmitRichMessageSnapshot(msg)
}

func (o *nativeObserver) SessionEvents(events []transcript.SessionEvent) {
	o.emitter.EmitSessionEvents(events)
}

func (o *nativeObserver) SessionName(name string) { o.emitter.SetSessionName(name) }

// SessionRef is not published; the session ID shown to clients is
// AgentAPI's own. It is stored to resume the session after a restart.
func (o *nativeObserver) SessionRef(ref string) {
	if o.persister != nil {
		o.persister.SetSessionRef(ref)
	}
}

func (o *nativeObserver) RequestUpdated(state native.RequestState) { o.emitter.EmitRequest(state) }

func (o *nativeObserver) Settings(settings native.Settings) { o.emitter.SetSettings(settings) }

func (o *nativeObserver) Usage(usage native.AgentUsage) { o.emitter.SetUsage(usage) }

// SettingsChosen is stored so that the agent gets the settings again after
// AgentAPI restarts.
func (o *nativeObserver) SettingsChosen(settings native.Settings) {
	if o.persister != nil {
		o.persister.SetSettings(settings)
	}
}

// requireNative returns an error for transports other than native.
func (s *Server) requireNative() error {
	if s.native == nil {
		return huma.Error400BadRequest("this endpoint needs the native transport (--transport native)")
	}
	return nil
}

// createNativeMessageLocked delivers a user message over the native
// transport according to mode. Steer and interrupt only apply to a running
// turn; without one the message is sent as usual. Caller holds s.mu.
func (s *Server) createNativeMessageLocked(content string, mode DispatchMode, resp *MessageResponse) error {
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	input := native.InputFromText(content)

	switch mode {
	case DispatchSteer:
		if !s.native.TurnActive() {
			break
		}
		switch err := s.native.Steer(ctx, input); {
		case err == nil:
			resp.Body.Ok = true
			resp.Body.Steered = true
			return nil
		case errors.Is(err, native.ErrUnsupported):
			return huma.Error400BadRequest("this agent can't add a message to a running turn")
		case errors.Is(err, native.ErrNoActiveTurn), errors.Is(err, native.ErrNotStarted):
			// The turn, or its agent, ended in the meantime; send the
			// message as usual.
		default:
			return fmt.Errorf("failed to steer the agent: %w", err)
		}
	case DispatchInterrupt:
		if !s.native.TurnActive() {
			break
		}
		switch err := s.native.Interrupt(ctx); {
		case errors.Is(err, native.ErrUnsupported):
			return huma.Error400BadRequest("this agent's turns can't be interrupted")
		case err != nil && !errors.Is(err, native.ErrNoActiveTurn) && !errors.Is(err, native.ErrNotStarted):
			return fmt.Errorf("failed to interrupt the agent: %w", err)
		}
		// The message goes first once the interrupted turn has ended.
		s.queueMessageLocked(resp, content, true)
		return nil
	case DispatchQueue:
	}

	if s.queue.Len() > 0 || !s.native.Idle() {
		s.queueMessageLocked(resp, content, false)
		return nil
	}
	if err := s.native.StartTurn(ctx, input); err != nil {
		if errors.Is(err, st.ErrMessageValidationChanging) {
			s.queueMessageLocked(resp, content, false)
			return nil
		}
		return fmt.Errorf("failed to send message: %w", err)
	}
	resp.Body.Ok = true
	return nil
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
	if r, ok := s.emitter.Request(input.ID); ok {
		return &RequestResponse{Body: r}, nil
	}
	return nil, huma.Error404NotFound("request not found")
}

func (s *Server) respondRequest(ctx context.Context, input *RespondRequest) (*RequestResponse, error) {
	// Serialize with message delivery, restarts and handoff replies.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.respondRequestLocked(input.ID, input.Body)
}

func (s *Server) respondRequestLocked(id string, response native.Response) (*RequestResponse, error) {
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	state, err := s.native.Respond(ctx, id, response)
	switch {
	case err == nil:
		return &RequestResponse{Body: state}, nil
	case errors.Is(err, native.ErrRequestNotFound):
		if state.ID == "" {
			// Requests restored from the state database, and old ones the
			// conversation no longer keeps, are only stored here.
			stored, ok := s.emitter.Request(id)
			if !ok {
				return nil, huma.Error404NotFound("request not found")
			}
			state = stored
		}
		return nil, huma.Error409Conflict(fmt.Sprintf("request is %s, not pending", state.Status))
	case errors.Is(err, native.ErrInvalidResponse):
		return nil, huma.Error400BadRequest(err.Error())
	default:
		return nil, fmt.Errorf("failed to deliver the response: %w", err)
	}
}

func (s *Server) retryLogin(ctx context.Context, _ *struct{}) (*InterruptResponse, error) {
	ctx, cancel := s.agentCtx(restartTimeout)
	defer cancel()
	if err := s.native.RetryLogin(ctx); err != nil {
		if errors.Is(err, native.ErrNoLoginRequired) {
			return nil, huma.Error409Conflict("the agent is not waiting for a login")
		}
		return nil, fmt.Errorf("failed to check the login: %w", err)
	}
	return okResponse(), nil
}

func (s *Server) interrupt(ctx context.Context, _ *struct{}) (*InterruptResponse, error) {
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	switch err := s.native.Interrupt(ctx); {
	case errors.Is(err, native.ErrUnsupported):
		return nil, huma.Error400BadRequest("this agent's turns can't be interrupted")
	case errors.Is(err, native.ErrNoActiveTurn), errors.Is(err, native.ErrNotStarted):
		// No agent runs while it restarts or waits for a login.
		return nil, huma.Error409Conflict("the agent is not working on a turn")
	case err != nil:
		return nil, fmt.Errorf("failed to interrupt the agent: %w", err)
	}
	return okResponse(), nil
}

func (s *Server) getSettings(ctx context.Context, _ *struct{}) (*SettingsResponse, error) {
	return s.settingsResponse(ctx)
}

// updateSettings doesn't take s.mu: a change doesn't touch the queue, and
// the conversation sorts out a restart that happens meanwhile.
func (s *Server) updateSettings(ctx context.Context, input *UpdateSettingsRequest) (*SettingsResponse, error) {
	applyCtx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	if err := settingsError(s.native.ApplySettings(applyCtx, input.Body)); err != nil {
		return nil, err
	}
	return s.settingsResponse(ctx)
}

func (s *Server) settingsResponse(ctx context.Context) (*SettingsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, nativeCallTimeout)
	defer cancel()
	options, err := s.native.SettingOptions(ctx)
	if err := settingsError(err); err != nil {
		return nil, err
	}
	resp := &SettingsResponse{}
	resp.Body.Settings = s.native.Settings()
	resp.Body.Options = options
	if resp.Body.Options.PermissionModes == nil {
		resp.Body.Options.PermissionModes = []native.PermissionMode{}
	}
	if resp.Body.Options.Models == nil {
		resp.Body.Options.Models = []native.ModelOption{}
	}
	return resp, nil
}

// settingsError turns an error about the settings into an HTTP error.
func settingsError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, native.ErrUnsupported):
		return huma.Error400BadRequest("this agent's settings can't be changed")
	case errors.Is(err, native.ErrInvalidSetting):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, native.ErrNotStarted):
		// It restarts, or waits for a login.
		return huma.Error409Conflict("the agent isn't running")
	default:
		return fmt.Errorf("failed to change the agent's settings: %w", err)
	}
}

// compactTimeout bounds a compaction: Pi answers once it summarized the
// session.
const compactTimeout = 5 * time.Minute

// compact has the agent compact its context. Messages sent meanwhile wait
// in the queue: the conversation starts no turn until it is done.
func (s *Server) compact(ctx context.Context, _ *struct{}) (*InterruptResponse, error) {
	ctx, cancel := s.agentCtx(compactTimeout)
	defer cancel()
	var refused *native.RefusedError
	switch err := s.native.Compact(ctx); {
	case errors.Is(err, native.ErrUnsupported):
		return nil, huma.Error400BadRequest("this agent doesn't compact on request; Claude Code compacts with the /compact message")
	case errors.Is(err, native.ErrTurnActive):
		return nil, huma.Error409Conflict("the agent is working on a turn or compacting; try again once it is done")
	case errors.Is(err, native.ErrNotStarted):
		return nil, huma.Error409Conflict("the agent isn't running")
	case errors.As(err, &refused):
		return nil, huma.Error409Conflict("the agent didn't compact: " + refused.Reason)
	case err != nil:
		return nil, fmt.Errorf("failed to compact: %w", err)
	}
	return okResponse(), nil
}

// steerQueuedMessage adds a queued message to the running turn instead of
// waiting for the turn to end.
func (s *Server) steerQueuedMessage(_ context.Context, input *SteerQueuedMessageRequest) (*QueueMutationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued, ok := s.queue.Get(input.ID)
	if !ok {
		return nil, huma.Error404NotFound("queued message not found")
	}
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	switch err := s.native.Steer(ctx, native.InputFromText(queued.Content)); {
	case errors.Is(err, native.ErrUnsupported):
		return nil, huma.Error400BadRequest("this agent can't add a message to a running turn")
	case errors.Is(err, native.ErrNoActiveTurn), errors.Is(err, native.ErrNotStarted):
		return nil, huma.Error409Conflict("the agent is not working on a turn; the message will be sent next")
	case err != nil:
		return nil, fmt.Errorf("failed to steer the agent: %w", err)
	}
	s.queue.Remove(input.ID)
	return queueMutated(), nil
}

// pendingNativeHandoff describes the oldest pending native request for the
// human reply protocol, or reports false when none is pending.
func (s *Server) pendingNativeHandoff() (native.RequestState, string, bool) {
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
	huma.Post(s.api, "/requests/{id}/respond", guard(s.requireNative, s.respondRequest), func(o *huma.Operation) {
		o.Tags = []string{"Requests"}
		o.Summary = "Answer a request"
		o.Description = "Answers a pending request. Approvals and plans take `option_id` (one of the request's options, optionally with `text` as feedback); questions take `answers` keyed by question id. Returns 409 if the request is no longer pending (already answered, withdrawn or expired) and 400 if the answer doesn't fit the request. A request is answered at most once."
		o.Errors = []int{400, 404, 409}
	})
	huma.Post(s.api, "/login/retry", guard(s.requireNative, s.retryLogin), func(o *huma.Operation) {
		o.Summary = "Check the agent's login again"
		o.Description = "While `login_required` is reported in GET /status, checks again now whether the user logged in (AgentAPI also checks every few seconds while the agent waits at startup). If the agent lost its login while running, it is restarted and resumes its session. Native transport only; returns 409 if no login is needed."
		o.Errors = []int{400, 409}
	})
	huma.Post(s.api, "/interrupt", guard(s.requireNative, s.interrupt), func(o *huma.Operation) {
		o.Summary = "Interrupt the agent"
		o.Description = "Stops the turn the agent is working on. Native transport only, for agents with the interrupt capability."
		o.Errors = []int{400, 409}
	})
	huma.Get(s.api, "/settings", guard(s.requireNative, s.getSettings), func(o *huma.Operation) {
		o.Summary = "Get the agent's settings"
		o.Description = "Returns the agent's permission mode, model and reasoning effort, and the values they can take. The current settings are also part of GET /status and `status_change` events. Native transport only, for agents with the settings capability; returns 409 while the agent restarts or waits for a login."
		o.Errors = []int{400, 409}
	})
	huma.Put(s.api, "/settings", guard(s.requireNative, s.updateSettings), func(o *huma.Operation) {
		o.Summary = "Change the agent's settings"
		o.Description = "Changes the settings that are set in the body and leaves the others: `permission_mode` (bypass, auto, ask or plan, as the agent offers), `model` (one of the model ids GET /settings lists) and `effort` (one of the model's efforts). A change made during a turn may apply from the next one (Codex). The agent that replaces this one after a restart, a new login or an AgentAPI restart (with --state-db) gets the chosen settings too. Native transport only; returns 400 for a value a setting can't take and 409 while the agent restarts or waits for a login."
		o.Errors = []int{400, 409}
	})
	huma.Post(s.api, "/compact", guard(s.requireNative, s.compact), func(o *huma.Operation) {
		o.Summary = "Compact the agent's context"
		o.Description = "Has the agent summarize the conversation so far to free its context window (Codex: in a turn of its own; Pi: answered once done). Native transport only, for agents with the compact capability. Messages sent meanwhile wait in the queue. Returns 409 while the agent works on a turn or compacts, and when it refuses (Pi with nothing to compact yet)."
		o.Errors = []int{400, 409}
	})
	huma.Post(s.api, "/queue/{id}/steer", guard(s.requireNative, s.steerQueuedMessage), func(o *huma.Operation) {
		o.Description = "Adds a queued message to the turn the agent is working on instead of waiting for it to end. Native transport only, for agents with the steer capability."
		o.Errors = []int{400, 404, 409}
	})
}
