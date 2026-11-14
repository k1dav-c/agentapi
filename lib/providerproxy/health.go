package providerproxy

import (
	"math"
	"slices"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/agentstate"
)

// healthWindow is how far back health goes, by the minute.
const healthWindow = time.Hour

// healthMinutes is how many minutes of counts health keeps: those of the
// window, the minute under way included.
const healthMinutes = int(healthWindow / time.Minute)

// healthSamples is how many of the latest first-byte times health keeps
// for its percentiles.
const healthSamples = 100

// healthBook keeps each provider's health.
type healthBook struct {
	mu        sync.Mutex
	providers map[string]*providerHealth
}

// providerHealth is what a provider's health is made of: the counts of
// each minute of the window, the latest first-byte times, and its latest
// success and failure.
type providerHealth struct {
	minutes     [healthMinutes]minuteCounts
	samples     [healthSamples]sample
	next, kept  int
	lastSuccess time.Time
	lastFailure *agentstate.ProviderFailure
}

// minuteCounts are the calls that ended in one minute (since the Unix
// epoch).
type minuteCounts struct {
	minute                           int64
	requests, failures, clientErrors int
}

// sample is a call's time to its first response byte.
type sample struct {
	at        time.Time
	firstByte time.Duration
}

func newHealthBook() *healthBook {
	return &healthBook{providers: map[string]*providerHealth{}}
}

// record counts a call to the provider id that ended at now.
func (b *healthBook) record(id string, now time.Time, o outcome, firstByte time.Duration, failure *agentstate.ProviderFailure) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.providers[id]
	if h == nil {
		h = &providerHealth{}
		b.providers[id] = h
	}
	minute := now.Unix() / 60
	counts := &h.minutes[minute%int64(healthMinutes)]
	if counts.minute != minute {
		*counts = minuteCounts{minute: minute}
	}
	counts.requests++
	switch o {
	case outcomeFailure:
		counts.failures++
		h.lastFailure = failure
		return
	case outcomeClientError:
		counts.clientErrors++
	case outcomeSuccess:
	}
	h.lastSuccess = now
	h.samples[h.next] = sample{at: now, firstByte: firstByte}
	h.next = (h.next + 1) % healthSamples
	h.kept = min(h.kept+1, healthSamples)
}

// of is the provider's health over the window before now; nil without a
// call in it.
func (b *healthBook) of(id string, now time.Time) *agentstate.ProviderHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.providers[id]
	if h == nil {
		return nil
	}
	health, ok := h.over(now)
	if !ok {
		return nil
	}
	return &health
}

// all is the health of every provider with calls in the window before
// now. It forgets the others.
func (b *healthBook) all(now time.Time) map[string]agentstate.ProviderHealth {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]agentstate.ProviderHealth{}
	for id, h := range b.providers {
		health, ok := h.over(now)
		if !ok {
			delete(b.providers, id)
			continue
		}
		out[id] = health
	}
	return out
}

// over is the health of the window before now, and whether it had calls.
func (h *providerHealth) over(now time.Time) (agentstate.ProviderHealth, bool) {
	var health agentstate.ProviderHealth
	minute := now.Unix() / 60
	for _, counts := range h.minutes {
		if counts.minute > minute-int64(healthMinutes) && counts.minute <= minute {
			health.Requests += counts.requests
			health.Failures += counts.failures
			health.ClientErrors += counts.clientErrors
		}
	}
	if health.Requests == 0 {
		return health, false
	}
	since := now.Add(-healthWindow)
	if h.lastSuccess.After(since) {
		at := h.lastSuccess.UTC()
		health.LastSuccessAt = &at
	}
	if h.lastFailure != nil && h.lastFailure.At.After(since) {
		failure := *h.lastFailure
		health.LastFailure = &failure
	}
	var times []time.Duration
	for _, s := range h.samples[:h.kept] {
		if s.at.After(since) {
			times = append(times, s.firstByte)
		}
	}
	health.FirstByteP50MS = percentile(times, 0.5).Milliseconds()
	health.FirstByteP95MS = percentile(times, 0.95).Milliseconds()
	return health, true
}

// percentile is the nearest-rank percentile q of times; 0 for none.
func percentile(times []time.Duration, q float64) time.Duration {
	if len(times) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(times))
	rank := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(rank, 0)]
}
