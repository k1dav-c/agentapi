package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/util"
)

// ContinueAt is when to continue a turn a usage limit stopped.
type ContinueAt string

const (
	// ContinueNow sends the continuation now.
	ContinueNow ContinueAt = "now"
	// ContinueAtReset sends it once the limit resets.
	ContinueAtReset ContinueAt = "reset"
)

var ContinueAtValues = []ContinueAt{ContinueNow, ContinueAtReset}

func (c ContinueAt) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "ContinueAt", ContinueAtValues)
}

// ContinueRequest asks to continue a turn a usage limit stopped.
type ContinueRequest struct {
	Body struct {
		At ContinueAt `json:"at" doc:"now: send the continuation now; reset: send it once the limit resets (409 if the agent didn't say when)."`
	}
}

// limitStopError maps the conversation's errors about a limit stop.
func limitStopError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, native.ErrNoLimitStop):
		return huma.Error409Conflict("no turn stopped on a usage limit")
	case errors.Is(err, conv.ErrMessageValidationChanging):
		return huma.Error409Conflict("the agent is busy; try again once it is done")
	case errors.Is(err, native.ErrNotStarted):
		return huma.Error409Conflict("the agent isn't running")
	}
	return fmt.Errorf("failed to continue: %w", err)
}

// continueLimited continues the turn a usage limit stopped: now, or once the
// limit resets.
func (s *Server) continueLimited(_ context.Context, input *ContinueRequest) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls := s.native.LimitStop()
	if ls == nil {
		return nil, limitStopError(native.ErrNoLimitStop)
	}
	if input.Body.At == ContinueAtReset {
		at := ls.ContinueAtReset()
		if at == nil {
			return nil, huma.Error409Conflict("the agent didn't say when the limit resets")
		}
		return okResponse(), limitStopError(s.native.ScheduleContinue(at))
	}
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	// The continuation goes before the queued messages, which waited for
	// it.
	if err := s.native.StartTurn(ctx, native.InputFromText(native.ContinuePrompt)); err != nil {
		return nil, limitStopError(err)
	}
	return okResponse(), nil
}

// cancelContinue cancels a scheduled continuation. The queue still waits
// for the limit to reset.
func (s *Server) cancelContinue(_ context.Context, _ *struct{}) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return okResponse(), limitStopError(s.native.ScheduleContinue(nil))
}

// dismissLimitStop forgets the turn a usage limit stopped, which lets the
// queued messages go out.
func (s *Server) dismissLimitStop(_ context.Context, _ *struct{}) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return okResponse(), limitStopError(s.native.DismissLimitStop())
}

// continueIfDueLocked sends a scheduled continuation once it is due. Called
// on the queue's tick, before the queue: the queued messages go after it.
// Caller holds s.mu.
func (s *Server) continueIfDueLocked() {
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	if _, err := s.native.ContinueIfDue(ctx, s.clock.Now()); err != nil && !errors.Is(err, conv.ErrMessageValidationChanging) {
		s.logger.Error("Failed to continue the turn a usage limit stopped", "error", err)
		s.emitter.EmitError(fmt.Sprintf("Failed to continue after the usage limit: %v", err), conv.ErrorLevelError)
	}
}

func (s *Server) registerLimitRoutes() {
	huma.Post(s.api, "/limit/continue", guard(s.requireNative, s.continueLimited), func(o *huma.Operation) {
		o.Summary = "Continue after a usage limit"
		o.Description = "Continues the turn a usage limit stopped (`limit_stop` in GET /status) with a message asking the agent to go on: now, or once the limit resets (`at: reset`, which needs the reset time). While a continuation is scheduled the queued messages wait for it; sending a message yourself cancels it. Native transport only; returns 409 when no turn stopped on a limit."
		o.Errors = []int{400, 409}
	})
	huma.Delete(s.api, "/limit/continue", guard(s.requireNative, s.cancelContinue), func(o *huma.Operation) {
		o.Summary = "Cancel a scheduled continuation"
		o.Description = "Cancels the continuation POST /limit/continue or --auto-continue scheduled. The queued messages still wait until the limit resets. Native transport only; returns 409 when no turn stopped on a limit."
		o.Errors = []int{400, 409}
	})
	huma.Delete(s.api, "/limit", guard(s.requireNative, s.dismissLimitStop), func(o *huma.Operation) {
		o.Summary = "Dismiss a usage limit stop"
		o.Description = "Forgets the turn a usage limit stopped, with its scheduled continuation, so the queued messages go out as soon as the agent can take them. Native transport only; returns 409 when no turn stopped on a limit."
		o.Errors = []int{400, 409}
	})
}
