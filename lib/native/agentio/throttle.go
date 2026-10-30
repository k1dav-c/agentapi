package agentio

import (
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/transcript"
)

// Defaults of the adapters' timing.
const (
	// SnapshotInterval is the least time between two snapshots of a
	// message that streams.
	SnapshotInterval = 100 * time.Millisecond
	// InterruptTimeout is how long an adapter waits for the agent to end
	// an interrupted turn before it ends the turn itself.
	InterruptTimeout = 10 * time.Second
)

// Throttle limits how often a message that keeps changing is sent: its
// first snapshot goes out at once, then at most one per interval, and the
// latest change held back goes out once the interval is over (Flush, at
// Next). Keys name the messages. Only the adapter's loop uses a Throttle.
type Throttle[K comparable] struct {
	clock    quartz.Clock
	interval time.Duration
	entries  map[K]*throttled
	// held lists the keys with a change held back, in the order they were
	// first held back.
	held []K
}

type throttled struct {
	sent bool
	last time.Time
	// held builds the change held back, nil without one.
	held func() (transcript.RichMessage, bool)
}

// NewThrottle returns a Throttle that sends a message at most once per
// interval.
func NewThrottle[K comparable](clock quartz.Clock, interval time.Duration) *Throttle[K] {
	return &Throttle[K]{clock: clock, interval: interval, entries: make(map[K]*throttled)}
}

func (t *Throttle[K]) entry(key K) *throttled {
	e := t.entries[key]
	if e == nil {
		e = &throttled{}
		t.entries[key] = e
	}
	return e
}

// Changed reports a change to message key, which build snapshots when it
// is sent; build reports false while there is nothing to show. Changed
// returns the snapshot to send now: the first one, or one an interval
// after the last. A change that comes sooner is held back, and only its
// latest build is kept, so a fast stream isn't copied for every change.
func (t *Throttle[K]) Changed(key K, build func() (transcript.RichMessage, bool)) (transcript.RichMessage, bool) {
	e := t.entry(key)
	if e.sent && t.clock.Since(e.last) < t.interval {
		if e.held == nil {
			t.held = append(t.held, key)
		}
		e.held = build
		return transcript.RichMessage{}, false
	}
	return t.send(e, build)
}

// Send returns the snapshot of message key to send now, interval or not,
// in place of a change held back.
func (t *Throttle[K]) Send(key K, build func() (transcript.RichMessage, bool)) (transcript.RichMessage, bool) {
	return t.send(t.entry(key), build)
}

func (t *Throttle[K]) send(e *throttled, build func() (transcript.RichMessage, bool)) (transcript.RichMessage, bool) {
	e.held = nil
	msg, ok := build()
	if ok {
		e.sent, e.last = true, t.clock.Now()
	}
	return msg, ok
}

// Flush returns the snapshots of the changes held back whose interval is
// over, all of them with force (the agent's output ended), in the order
// they were held back.
func (t *Throttle[K]) Flush(force bool) []transcript.RichMessage {
	var out []transcript.RichMessage
	now := t.clock.Now()
	kept := t.held[:0]
	for _, key := range t.held {
		e := t.entries[key]
		switch {
		case e == nil || e.held == nil:
			// Sent or forgotten since.
		case !force && now.Sub(e.last) < t.interval:
			kept = append(kept, key)
		default:
			if msg, ok := t.send(e, e.held); ok {
				out = append(out, msg)
			}
		}
	}
	t.held = kept
	return out
}

// Next returns when the first change held back may be sent, or the zero
// time if none is.
func (t *Throttle[K]) Next() time.Time {
	var next time.Time
	for _, key := range t.held {
		if e := t.entries[key]; e != nil && e.held != nil {
			next = Earliest(next, e.last.Add(t.interval))
		}
	}
	return next
}

// Sent reports whether a snapshot of message key was sent since it was
// last forgotten.
func (t *Throttle[K]) Sent(key K) bool {
	e := t.entries[key]
	return e != nil && e.sent
}

// Held reports whether a change to message key is held back.
func (t *Throttle[K]) Held(key K) bool {
	e := t.entries[key]
	return e != nil && e.held != nil
}

// Forget forgets message key, whose final state was sent, with a change
// held back.
func (t *Throttle[K]) Forget(key K) {
	delete(t.entries, key)
}

// Reset forgets every message.
func (t *Throttle[K]) Reset() {
	clear(t.entries)
	t.held = nil
}

// InterruptDeadline is when an adapter stops waiting for the agent to end
// an interrupted turn and ends the turn itself: an agent may never confirm
// an interrupt. Only the adapter's loop uses it.
type InterruptDeadline struct {
	clock   quartz.Clock
	timeout time.Duration
	// since is when the wait began, zero while none runs.
	since time.Time
}

// NewInterruptDeadline returns a deadline timeout after the interrupt.
func NewInterruptDeadline(clock quartz.Clock, timeout time.Duration) InterruptDeadline {
	return InterruptDeadline{clock: clock, timeout: timeout}
}

// Start begins the wait for the interrupted turn to end, unless one runs.
func (d *InterruptDeadline) Start() {
	if d.since.IsZero() {
		d.since = d.clock.Now()
	}
}

// Stop ends the wait: the turn ended.
func (d *InterruptDeadline) Stop() {
	d.since = time.Time{}
}

// Next returns when the wait is over, or the zero time while none runs.
func (d *InterruptDeadline) Next() time.Time {
	if d.since.IsZero() {
		return time.Time{}
	}
	return d.since.Add(d.timeout)
}

// Due reports whether the wait is over: the adapter ends the turn.
func (d *InterruptDeadline) Due() bool {
	return !d.since.IsZero() && d.clock.Since(d.since) >= d.timeout
}

// Earliest returns the earliest of times that isn't zero, or the zero time.
func Earliest(times ...time.Time) time.Time {
	var first time.Time
	for _, t := range times {
		if !t.IsZero() && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}
