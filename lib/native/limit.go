package native

import (
	"context"
	"errors"
	"time"
)

// ErrNoLimitStop is returned when no turn stopped on a usage limit.
var ErrNoLimitStop = errors.New("no turn stopped on a usage limit")

// ContinuePrompt is the message that continues a turn a usage limit
// stopped.
const ContinuePrompt = "Continue from where you left off."

// continueMargin is how long after a limit resets a continuation is sent:
// the reset times agents report are rounded, and the limit may lift a
// moment after.
const continueMargin = time.Minute

// LimitStop is a turn that stopped because the account reached a usage
// limit. It holds the queue until the limit resets, or until a
// continuation that is due went out.
type LimitStop struct {
	Message   string    `json:"message" doc:"What the agent said, e.g. You've hit your limit · resets 3pm."`
	StoppedAt time.Time `json:"stopped_at" doc:"When the turn stopped."`
	// ResetsAt is nil when the agent doesn't say.
	ResetsAt *time.Time `json:"resets_at,omitempty" doc:"When the limit resets, if the agent says. Queued messages wait until then."`
	// ContinueAt is set while a continuation is scheduled.
	ContinueAt *time.Time `json:"continue_at,omitempty" doc:"When AgentAPI sends a message that continues the turn, if one is scheduled (POST /limit/continue). Queued messages go out after it."`
}

// LimitStop reports the turn a usage limit stopped, if no turn started
// since; nil otherwise.
func (c *Conversation) LimitStop() *LimitStop {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitStop.clone()
}

func (l *LimitStop) clone() *LimitStop {
	if l == nil {
		return nil
	}
	copied := *l
	return &copied
}

// setLimitStopLocked records ls (nil clears it) and reports it.
func (c *Conversation) setLimitStopLocked(ls *LimitStop) {
	if c.limitStop == nil && ls == nil {
		return
	}
	c.limitStop = ls
	c.cfg.Observer.LimitStop(ls.clone())
}

// limitStoppedLocked records that a usage limit stopped the turn that ended
// with result.
func (c *Conversation) limitStoppedLocked(result TurnResult) {
	ls := &LimitStop{Message: result.Error, StoppedAt: c.cfg.Clock.Now(), ResetsAt: result.ResetsAt}
	if ls.ResetsAt == nil {
		ls.ResetsAt = c.usage.reachedLimitReset()
	}
	if c.cfg.AutoContinue || c.continueAgain {
		ls.ContinueAt = ls.ContinueAtReset()
	}
	c.setLimitStopLocked(ls)
}

// reachedLimitReset returns when the limits the account reached reset: the
// latest reset among them, as the account can't work before then. It is
// nil when the agent reported no reached limit with a reset time.
func (u AgentUsage) reachedLimitReset() *time.Time {
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
	if c.limitStop == nil {
		return ErrNoLimitStop
	}
	ls := *c.limitStop
	ls.ContinueAt = nil
	if at != nil {
		when := *at
		ls.ContinueAt = &when
	}
	c.setLimitStopLocked(&ls)
	return nil
}

// ContinueAtReset is when a continuation scheduled for the limit's reset
// goes out, or nil when the agent didn't say when it resets.
func (l *LimitStop) ContinueAtReset() *time.Time {
	if l == nil || l.ResetsAt == nil {
		return nil
	}
	at := l.ResetsAt.Add(continueMargin)
	return &at
}

// DismissLimitStop forgets the turn a usage limit stopped, which releases
// the queue. It returns ErrNoLimitStop when there is none.
func (c *Conversation) DismissLimitStop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limitStop == nil {
		return ErrNoLimitStop
	}
	c.setLimitStopLocked(nil)
	return nil
}

// HoldsQueue reports whether queued messages wait because of a usage limit:
// until it resets, or until a scheduled continuation went out. Without a
// reset time they wait until a turn starts or the stop is dismissed.
func (c *Conversation) HoldsQueue(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ls := c.limitStop
	switch {
	case ls == nil:
		return false
	case ls.ContinueAt != nil, ls.ResetsAt == nil:
		return true
	}
	return now.Before(*ls.ResetsAt)
}

// ContinueIfDue sends the continuation once it is due and the agent is
// idle. It reports whether it sent one; an error is the agent's refusal.
func (c *Conversation) ContinueIfDue(ctx context.Context, now time.Time) (bool, error) {
	c.mu.Lock()
	ls := c.limitStop.clone()
	due := ls != nil && ls.ContinueAt != nil && !now.Before(*ls.ContinueAt) && c.started && !c.turnActive && !c.compacting
	if due {
		c.continueAgain = true
	}
	c.mu.Unlock()
	if !due {
		return false, nil
	}
	err := c.StartTurn(ctx, InputFromText(ContinuePrompt))
	if err == nil {
		return true, nil
	}
	// The stop stays, and the continuation is tried again in a while.
	c.mu.Lock()
	c.continueAgain = false
	if c.limitStop == nil && !c.turnActive {
		retry := now.Add(continueMargin)
		ls.ContinueAt = &retry
		c.setLimitStopLocked(ls)
	}
	c.mu.Unlock()
	return false, err
}
