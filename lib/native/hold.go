package native

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
)

// A turn that stops on a usage limit (agentstate.LimitStop) or fails on a
// transient upstream error (agentstate.RetryWait) holds the queue: the
// queued messages wait until a message that continues the turn
// (ContinuePrompt) went out, or for a limit without one scheduled until it
// resets. A conversation has at most one hold, which the next turn ends,
// and a turn whose message doesn't reach the agent gives back.

var (
	// ErrNoLimitStop is returned when no turn stopped on a usage limit.
	ErrNoLimitStop = errors.New("no turn stopped on a usage limit")
	// ErrNoRetryWait is returned when no failed turn waits to be tried
	// again.
	ErrNoRetryWait = errors.New("no failed turn waits to be tried again")
)

// ContinuePrompt is the message that continues a turn a usage limit
// stopped, or tries a failed turn again.
const ContinuePrompt = "Continue from where you left off."

// continueMargin is how long after a limit resets a continuation is sent:
// the reset times agents report are rounded, and the limit may lift a
// moment after. A continuation the agent refused is tried again after it
// too.
const continueMargin = time.Minute

// defaultRetryDelays are Config.RetryDelays' default (--retry-delays
// 30s,1m,2m,5m*12): 15 retries, about an hour in all.
var defaultRetryDelays = append([]time.Duration{30 * time.Second, time.Minute, 2 * time.Minute}, slices.Repeat([]time.Duration{5 * time.Minute}, 12)...)

// clone returns a copy of *p, or nil for nil.
func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	copied := *p
	return &copied
}

// ContinueAtReset is when a continuation scheduled for the reset of l's
// limit goes out, or nil when the agent didn't say when it resets.
func ContinueAtReset(l *agentstate.LimitStop) *time.Time {
	if l == nil || l.ResetsAt == nil {
		return nil
	}
	at := l.ResetsAt.Add(continueMargin)
	return &at
}

// HoldKind is what holds the queue.
type HoldKind int

const (
	// HoldNone: nothing holds the queue.
	HoldNone HoldKind = iota
	// HoldLimit: a turn a usage limit stopped (LimitStop).
	HoldLimit
	// HoldRetry: a failed turn that waits to be tried again (RetryWait).
	HoldRetry
)

// hold is what holds the queue: a limit stop, a retry wait, or with
// neither nothing. Its values are never changed, only replaced.
type hold struct {
	limit *agentstate.LimitStop
	retry *agentstate.RetryWait
}

// restoredHold is the hold restored from before AgentAPI restarted. Only
// one of limit and retry holds at a time; if both are given, the later
// one does.
func restoredHold(limit *agentstate.LimitStop, retry *agentstate.RetryWait) hold {
	switch {
	case limit != nil && retry != nil && retry.StoppedAt.After(limit.StoppedAt):
		return hold{retry: clone(retry)}
	case limit != nil:
		return hold{limit: clone(limit)}
	}
	return hold{retry: clone(retry)}
}

func (h hold) kind() HoldKind {
	switch {
	case h.limit != nil:
		return HoldLimit
	case h.retry != nil:
		return HoldRetry
	}
	return HoldNone
}

// dueAt is when the message that continues the turn goes out; false when
// none is scheduled.
func (h hold) dueAt() (time.Time, bool) {
	switch {
	case h.limit != nil && h.limit.ContinueAt != nil:
		return *h.limit.ContinueAt, true
	case h.retry != nil:
		return h.retry.RetryAt, true
	}
	return time.Time{}, false
}

// due reports whether the message that continues the turn is due at now.
func (h hold) due(now time.Time) bool {
	at, ok := h.dueAt()
	return ok && !now.Before(at)
}

// later is h with the message that continues the turn scheduled for at.
func (h hold) later(at time.Time) hold {
	switch {
	case h.limit != nil:
		ls := *h.limit
		ls.ContinueAt = &at
		return hold{limit: &ls}
	case h.retry != nil:
		rw := *h.retry
		rw.RetryAt = at
		return hold{retry: &rw}
	}
	return h
}

// holdsQueue reports whether queued messages wait at now: for a failed
// turn until it was tried again, for a usage limit until a scheduled
// continuation went out, or else until it resets (one that doesn't say
// when until a turn starts or the stop is dismissed).
func (h hold) holdsQueue(now time.Time) bool {
	switch {
	case h.retry != nil:
		return true
	case h.limit == nil:
		return false
	case h.limit.ContinueAt != nil, h.limit.ResetsAt == nil:
		return true
	}
	return now.Before(*h.limit.ResetsAt)
}

// retried is the retry, from 1, the turn that continues h is; 0 for any
// other.
func (h hold) retried() int {
	if h.retry == nil {
		return 0
	}
	return h.retry.Attempt
}

// setHoldLocked replaces the hold with h, and reports what changed.
func (c *Conversation) setHoldLocked(h hold) {
	old := c.hold
	c.hold = h
	// What ended is reported before what began.
	if old.limit != h.limit && h.limit == nil {
		c.cfg.Observer.LimitStop(nil)
	}
	if old.retry != h.retry && h.retry == nil {
		c.cfg.Observer.RetryWait(nil)
	}
	if old.limit != h.limit && h.limit != nil {
		c.cfg.Observer.LimitStop(clone(h.limit))
	}
	if old.retry != h.retry && h.retry != nil {
		c.cfg.Observer.RetryWait(clone(h.retry))
	}
}

// LimitStop reports the turn a usage limit stopped, if no turn started
// since; nil otherwise.
func (c *Conversation) LimitStop() *agentstate.LimitStop {
	c.mu.Lock()
	defer c.mu.Unlock()
	return clone(c.hold.limit)
}

// RetryWait reports the failed turn that waits to be tried again, if no
// turn started since; nil otherwise.
func (c *Conversation) RetryWait() *agentstate.RetryWait {
	c.mu.Lock()
	defer c.mu.Unlock()
	return clone(c.hold.retry)
}

// HoldsQueue reports whether queued messages wait at now: for a usage
// limit, until it resets or until a scheduled continuation went out
// (without a reset time until a turn starts or the stop is dismissed), and
// for a failed turn until it was tried again (RetryWait).
func (c *Conversation) HoldsQueue(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hold.holdsQueue(now)
}

// ContinueIfDue sends the continuation of a turn a usage limit stopped, or
// the retry of a failed turn, once it is due and the agent is idle. It
// reports what it sent, HoldNone if nothing; with an error, the agent's
// refusal, what it tried to send. A refused one is tried again a minute
// later, and the hold stays meanwhile.
func (c *Conversation) ContinueIfDue(ctx context.Context, now time.Time) (HoldKind, error) {
	c.mu.Lock()
	h := c.hold
	if !c.readyLocked() || !h.due(now) {
		c.mu.Unlock()
		return HoldNone, nil
	}
	// Decided and begun in one step: no other turn starts in between.
	turn, err := c.beginTurnLocked(InputFromText(ContinuePrompt), h)
	turn.restore = h.later(now.Add(continueMargin))
	c.mu.Unlock()
	if err == nil {
		err = c.deliverTurn(ctx, turn)
	}
	return h.kind(), err
}

// limitStoppedLocked records that a usage limit stopped the turn that ended
// with result; again: that turn was a continuation, so it is scheduled
// again.
func (c *Conversation) limitStoppedLocked(result TurnResult, again bool) {
	ls := &agentstate.LimitStop{Message: result.Error, StoppedAt: c.cfg.Clock.Now(), ResetsAt: result.ResetsAt}
	if ls.ResetsAt == nil {
		ls.ResetsAt = reachedLimitReset(c.usage)
	}
	if c.cfg.AutoContinue || again {
		ls.ContinueAt = ContinueAtReset(ls)
	}
	c.setHoldLocked(hold{limit: ls})
}

// reachedLimitReset returns when the limits the account reached in u
// reset: the latest reset among them, as the account can't work before
// then. It is nil when the agent reported no reached limit with a reset
// time.
func reachedLimitReset(u agentstate.AgentUsage) *time.Time {
	var latest *time.Time
	for _, l := range u.RateLimits {
		reached := l.Status == "rejected" || (l.UsedPercent != nil && *l.UsedPercent >= 100)
		if reached && l.ResetsAt != nil && (latest == nil || l.ResetsAt.After(*latest)) {
			at := *l.ResetsAt
			latest = &at
		}
	}
	return latest
}

// ScheduleContinue schedules a message that continues the turn a usage
// limit stopped: at the time given, or with nil not at all. It returns
// ErrNoLimitStop when no turn stopped on a limit.
func (c *Conversation) ScheduleContinue(at *time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hold.limit == nil {
		return ErrNoLimitStop
	}
	ls := *c.hold.limit
	ls.ContinueAt = nil
	if at != nil {
		when := *at
		ls.ContinueAt = &when
	}
	c.setHoldLocked(hold{limit: &ls})
	return nil
}

// DismissLimitStop forgets the turn a usage limit stopped, which releases
// the queue. It returns ErrNoLimitStop when there is none.
func (c *Conversation) DismissLimitStop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hold.limit == nil {
		return ErrNoLimitStop
	}
	c.setHoldLocked(hold{})
	return nil
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
	c.setHoldLocked(hold{retry: &agentstate.RetryWait{
		Message: result.Error, APIStatus: status, StoppedAt: now,
		Attempt: attempt, Attempts: len(delays), RetryAt: now.Add(delay),
	}})
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
// for. It returns ErrNoRetryWait when none waits, and StartTurn's errors;
// a retry that doesn't reach the agent still waits.
func (c *Conversation) RetryNow(ctx context.Context) error {
	c.mu.Lock()
	h := c.hold
	if h.retry == nil {
		c.mu.Unlock()
		return ErrNoRetryWait
	}
	turn, err := c.beginTurnLocked(InputFromText(ContinuePrompt), h)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.deliverTurn(ctx, turn)
}

// CancelRetry forgets the failed turn that waits to be tried again, which
// releases the queue. It returns ErrNoRetryWait when none waits.
func (c *Conversation) CancelRetry() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hold.retry == nil {
		return ErrNoRetryWait
	}
	c.setHoldLocked(hold{})
	return nil
}
