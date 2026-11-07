package httpapi

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/native"
)

// retryWaitError maps the conversation's errors about a retry wait.
func retryWaitError(err error) error {
	if errors.Is(err, native.ErrNoRetryWait) {
		return huma.Error409Conflict("no failed turn waits to be tried again")
	}
	return limitStopError(err)
}

// retryNow tries the failed turn that waits now, instead of at retry_at.
func (s *Server) retryNow(_ context.Context, _ *struct{}) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	// The retry goes before the queued messages, which waited for it.
	if err := s.native.RetryNow(ctx); err != nil {
		return nil, retryWaitError(err)
	}
	return okResponse(), nil
}

// cancelRetry forgets the failed turn that waits to be tried again, which
// lets the queued messages go out.
func (s *Server) cancelRetry(_ context.Context, _ *struct{}) (*InterruptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return okResponse(), retryWaitError(s.native.CancelRetry())
}

func (s *Server) registerRetryRoutes() {
	huma.Post(s.api, "/retry", s.retryNow, func(o *huma.Operation) {
		o.Summary = "Retry a failed turn now"
		o.Description = "Tries the turn that failed on an upstream error (`retry_wait` in GET /status) again now rather than at `retry_at`: AgentAPI sends a message asking the agent to go on, before the queued messages. If it fails the same way, the next retry waits the next delay of --retry-delays. Returns 409 when no failed turn waits."
		o.Errors = []int{400, 409}
	})
	huma.Delete(s.api, "/retry", s.cancelRetry, func(o *huma.Operation) {
		o.Summary = "Cancel a retry"
		o.Description = "Forgets the turn that failed on an upstream error without trying it again, so the queued messages go out as soon as the agent can take them. Returns 409 when no failed turn waits."
		o.Errors = []int{400, 409}
	})
}
