package native

import (
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Policy is what a conversation does by itself when a turn or the agent
// stops: continue a turn a usage limit stopped, try a failed turn again,
// start an agent that exited again. The zero value is the defaults, with
// AutoContinue off.
type Policy struct {
	// AutoContinue schedules a message that continues a turn a usage limit
	// stopped for when the limit resets, if the agent says when
	// (--auto-continue).
	AutoContinue bool
	// RetryDelays are the waits before a turn that failed on a transient
	// upstream error (StopReasonTransient) is tried again: one per retry in
	// a row, after which the failure is an error (--retry-delays). Nil
	// means DefaultRetryDelays; empty turns retries off (RetriesOff).
	RetryDelays []time.Duration
	// RestartDelays are the waits before the conversation starts the agent
	// again after it exited on its own, or failed a start the conversation
	// made by itself: one per attempt, after which it gives up and no agent
	// runs until a restart. Once an agent ran five minutes, its exit starts
	// with the first again. Nil means 2, 10 and 30 seconds; empty means
	// none.
	RestartDelays []time.Duration
}

// defaultRetryDelays are DefaultRetryDelays: 15 retries, about an hour in
// all.
var defaultRetryDelays = append([]time.Duration{30 * time.Second, time.Minute, 2 * time.Minute}, slices.Repeat([]time.Duration{5 * time.Minute}, 12)...)

// defaultRestartDelays are Policy.RestartDelays' default.
var defaultRestartDelays = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

// DefaultRetryDelays returns Policy.RetryDelays' default: 30 seconds, a
// minute, 2 minutes, then 5 minutes twelve times (--retry-delays
// 30s,1m,2m,5m*12).
func DefaultRetryDelays() []time.Duration {
	return slices.Clone(defaultRetryDelays)
}

// RetriesOff reports whether p turns retries off: its RetryDelays are
// empty rather than nil. A failed turn that waited to be tried again when
// AgentAPI stopped doesn't wait any more then.
func (p Policy) RetriesOff() bool {
	return p.RetryDelays != nil && len(p.RetryDelays) == 0
}

// withDefaults returns p with the defaults for the delays it leaves nil.
func (p Policy) withDefaults() Policy {
	if p.RetryDelays == nil {
		p.RetryDelays = defaultRetryDelays
	}
	if p.RestartDelays == nil {
		p.RestartDelays = defaultRestartDelays
	}
	return p
}

// FormatDelays writes delays the way --retry-delays takes them, a run of
// one delay as delay*n: DefaultRetryDelays is 30s, 1m, 2m, 5m*12.
func FormatDelays(delays []time.Duration) []string {
	var out []string
	for i := 0; i < len(delays); {
		n := 1
		for i+n < len(delays) && delays[i+n] == delays[i] {
			n++
		}
		field := formatDelay(delays[i])
		if n > 1 {
			field += "*" + strconv.Itoa(n)
		}
		out = append(out, field)
		i += n
	}
	return out
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
