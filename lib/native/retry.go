package native

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
)

// ErrNoRetryWait is returned when no failed turn waits to be tried again.
var ErrNoRetryWait = errors.New("no failed turn waits to be tried again")

// defaultRetryDelays are Config.RetryDelays' default (--retry-delays
// 30s,1m,2m,5m*12): 15 retries, about an hour in all.
var defaultRetryDelays = append([]time.Duration{30 * time.Second, time.Minute, 2 * time.Minute}, slices.Repeat([]time.Duration{5 * time.Minute}, 12)...)

// RetryWait is a turn that failed on a transient upstream error (an HTTP
// 5xx or 429, an overloaded API) after the agent's own retries. It holds
// the queue until AgentAPI tried the turn again: it sends ContinuePrompt
// at RetryAt, so the agent goes on without the user's message twice.
type RetryWait struct {
	Message string `json:"message" doc:"What the agent said, e.g. API Error: 502 Bad Gateway."`
	// APIStatus is 0 when the agent doesn't say.
	APIStatus int       `json:"api_status,omitempty" doc:"The HTTP status the API or gateway answered with, e.g. 502, if the agent says."`
	StoppedAt time.Time `json:"stopped_at" doc:"When the turn failed."`
	Attempt   int       `json:"attempt" doc:"Which retry comes next, from 1."`
	Attempts  int       `json:"attempts" doc:"How many times in a row AgentAPI tries a failed turn again (--retry-delays). After the last, a failure is an error."`
	RetryAt   time.Time `json:"retry_at" doc:"When AgentAPI sends a message that continues the turn. Queued messages go out after it."`
}

func (r *RetryWait) clone() *RetryWait {
	if r == nil {
		return nil
	}
	copied := *r
	return &copied
}

// RetryWait reports the failed turn that waits to be tried again, if no
// turn started since; nil otherwise.
func (c *Conversation) RetryWait() *RetryWait {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retryWait.clone()
}

// setRetryWaitLocked records rw (nil clears it) and reports it.
func (c *Conversation) setRetryWaitLocked(rw *RetryWait) {
	if c.retryWait == nil && rw == nil {
		return
	}
	c.retryWait = rw
	c.cfg.Observer.RetryWait(rw.clone())
}

// retryLocked schedules the turn that ended with result, a transient
// failure, to be tried again as the attempt-th retry in a row, and warns
// of it. It reports false, and schedules nothing, once the retries ran
// out.
func (c *Conversation) retryLocked(result TurnResult, attempt int) bool {
	delays := c.cfg.RetryDelays
	if attempt > len(delays) {
		return false
	}
	now := c.cfg.Clock.Now()
	delay := delays[attempt-1]
	status := result.APIStatus
	if status == 0 {
		status = TransientStatus(result.Error)
	}
	c.setRetryWaitLocked(&RetryWait{
		Message: result.Error, APIStatus: status, StoppedAt: now,
		Attempt: attempt, Attempts: len(delays), RetryAt: now.Add(delay),
	})
	c.cfg.Emitter.EmitError(fmt.Sprintf("%s AgentAPI tries again in %s (%d of %d).", result.Error, formatDelay(delay), attempt, len(delays)), conv.ErrorLevelWarning)
	return true
}

// retriesOutText is the error of a transient failure that isn't tried again:
// what the agent said, and how often AgentAPI tried before.
func retriesOutText(result TurnResult, retries int) string {
	if retries == 0 {
		return result.Error
	}
	times := "times"
	if retries == 1 {
		times = "time"
	}
	return fmt.Sprintf("%s AgentAPI tried again %d %s and gave up.", result.Error, retries, times)
}

// formatDelay writes a retry delay the way a flag gives it: 30s, 2m, 1m30s.
func formatDelay(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d < time.Minute:
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return fmt.Sprintf("%dm%ds", d/time.Minute, (d%time.Minute)/time.Second)
}

// RetryNow tries the failed turn that waits now, as the retry it waits
// for. It returns ErrNoRetryWait when none waits, and StartTurn's errors.
func (c *Conversation) RetryNow(ctx context.Context) error {
	c.mu.Lock()
	if c.retryWait == nil {
		c.mu.Unlock()
		return ErrNoRetryWait
	}
	c.retrying = c.retryWait.Attempt
	c.mu.Unlock()
	err := c.StartTurn(ctx, InputFromText(ContinuePrompt))
	if err != nil {
		c.mu.Lock()
		c.retrying = 0
		c.mu.Unlock()
	}
	return err
}

// CancelRetry forgets the failed turn that waits to be tried again, which
// releases the queue. It returns ErrNoRetryWait when none waits.
func (c *Conversation) CancelRetry() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.retryWait == nil {
		return ErrNoRetryWait
	}
	c.setRetryWaitLocked(nil)
	return nil
}
