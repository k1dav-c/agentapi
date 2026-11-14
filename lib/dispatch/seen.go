package dispatch

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

// A run's spend and work time as the hub reads them of its AgentAPI, which
// a task event that doesn't reach the hub doesn't lower: the summary's
// spend lists each hour of the last 25 that had calls (the agent's
// sessions in the run's worktree, subagents included), and the hub keeps
// a week of the spans it saw an instance working. The run keeps the hours
// that may still change and adds up those before them; it adds each span
// of work once, from when it started or the run did.

// listedHours is how far back the summary's spend lists the hours: an
// hour that started before the hour under way minus this is final.
const listedHours = 24 * time.Hour

// Seen is a run's spend and work time as the hub read them of its
// AgentAPI.
type Seen struct {
	// Scope is the spend's scope: the agent's sessions in the run's
	// worktree. Empty until the hub read the spend.
	Scope string `json:"scope,omitempty"`
	// Hours are the hours of the scope since the run started that may
	// still change, the earliest first.
	Hours []SpendHour `json:"hours,omitempty"`
	// Earlier adds up the hours before them.
	Earlier Spend `json:"earlier"`
	// WorkSeconds is how long the hub saw the run's AgentAPI working, in
	// the spans that ended; WorkedUntil is when the last one ended.
	WorkSeconds float64   `json:"work_seconds"`
	WorkedUntil time.Time `json:"worked_until,omitzero"`
}

// Spend is what calls cost.
type Spend struct {
	USD    float64 `json:"usd"`
	Tokens Tokens  `json:"tokens"`
}

func (s *Spend) add(o Spend) {
	s.USD += o.USD
	s.Tokens.Input += o.Tokens.Input
	s.Tokens.Output += o.Tokens.Output
	s.Tokens.CacheWrite += o.Tokens.CacheWrite
	s.Tokens.CacheRead += o.Tokens.CacheRead
}

// SpendHour is what an hour's calls cost.
type SpendHour struct {
	Hour time.Time `json:"hour"`
	Spend
}

// spend adds up the hours.
func (s *Seen) spend() Spend {
	total := s.Earlier
	for _, h := range s.Hours {
		total.add(h.Spend)
	}
	return total
}

// Sighting is what the hub read of an instance's summary stream at URL:
// its spend (nil when the summary has none, or no hours: AgentAPIs before
// 4.2), and the spans of work it saw that ended, the oldest first.
type Sighting struct {
	InstanceID string
	ServerID   string
	URL        string
	Spend      *instanceapi.SummarySpend
	Work       []WorkSpan
}

// WorkSpan is a stretch of time the hub saw an instance working.
type WorkSpan struct {
	Start, End time.Time
}

// Saw takes what the hub read of an instance: the job of the current run
// it is takes its spend and work time. The same as for a task event
// (TaskEvent), only the exact AgentAPI process of the run counts, read at
// an address of the run's machine.
func (s *Service) Saw(ctx context.Context, seen Sighting) {
	if seen.URL == "" || (seen.Spend == nil && len(seen.Work) == 0) {
		return
	}
	s.mu.Lock()
	key, launcherURL := s.runOfLocked(seen.InstanceID, seen.ServerID)
	s.mu.Unlock()
	if key == "" || !sameMachine(ctx, launcherURL, seen.URL) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[key]
	if job == nil {
		return
	}
	run := job.current()
	if !isRun(run, seen.InstanceID, seen.ServerID) {
		return
	}
	var before *Seen
	if run.Seen != nil {
		c := *run.Seen
		c.Hours = slices.Clone(run.Seen.Hours)
		before = &c
	}
	now := s.config.Now()
	if run.see(seen, now) {
		// An hour this run's AgentAPI reports is its own: a run before it
		// of the same scope (the same agent in the worktree) may have
		// counted it when it ended within it.
		for i := range job.Runs {
			if other := &job.Runs[i]; other != run && other.Seen != nil && other.Seen.Scope == run.Seen.Scope {
				other.Seen.Hours = slices.DeleteFunc(other.Seen.Hours, func(h SpendHour) bool {
					return slices.ContainsFunc(run.Seen.Hours, func(o SpendHour) bool { return o.Hour.Equal(h.Hour) })
				})
				other.Cost = other.shown()
			}
		}
	}
	run.Cost = run.shown()
	if !reflect.DeepEqual(before, run.Seen) {
		s.saveLocked()
	}
}

// runOfLocked is the job whose current run is the AgentAPI process
// instanceID and serverID, and the launcher URL of the run's machine;
// empty when none is. s.mu is held.
func (s *Service) runOfLocked(instanceID, serverID string) (key, launcherURL string) {
	for _, job := range s.jobs {
		if run := job.current(); isRun(run, instanceID, serverID) {
			if h := s.hosts[cmp.Or(run.Host, job.Host)]; h != nil {
				launcherURL = h.URL
			}
			return job.Key, launcherURL
		}
	}
	return "", ""
}

// see takes what the hub read of the run's AgentAPI at now, and reports
// whether it took spend hours.
func (r *Run) see(seen Sighting, now time.Time) bool {
	if r.Seen == nil {
		r.Seen = &Seen{}
	}
	tookHours := false
	if sp := seen.Spend; sp != nil && len(sp.Hours) > 0 {
		r.Seen.Scope = sp.Scope
		// The hours that may still change, of the run.
		final := now.UTC().Truncate(time.Hour).Add(-listedHours)
		first := r.StartedAt.UTC().Truncate(time.Hour)
		for _, h := range sp.Hours {
			hour := h.Hour.UTC()
			if hour.Before(first) || hour.Before(final) {
				continue
			}
			spend := Spend{USD: h.USD, Tokens: Tokens{
				Input: int64(h.Tokens.Input), Output: int64(h.Tokens.Output),
				CacheWrite: int64(h.Tokens.CacheWrite), CacheRead: int64(h.Tokens.CacheRead),
			}}
			i, found := slices.BinarySearchFunc(r.Seen.Hours, hour, func(e SpendHour, t time.Time) int { return e.Hour.Compare(t) })
			if found {
				r.Seen.Hours[i].Spend = spend
			} else {
				r.Seen.Hours = slices.Insert(r.Seen.Hours, i, SpendHour{Hour: hour, Spend: spend})
			}
			tookHours = true
		}
		// Those that left the list are final.
		n := 0
		for n < len(r.Seen.Hours) && r.Seen.Hours[n].Hour.Before(final) {
			r.Seen.Earlier.add(r.Seen.Hours[n].Spend)
			n++
		}
		r.Seen.Hours = slices.Delete(r.Seen.Hours, 0, n)
	}
	for _, w := range seen.Work {
		start := w.Start
		if start.Before(r.StartedAt) {
			start = r.StartedAt
		}
		if start.Before(r.Seen.WorkedUntil) {
			start = r.Seen.WorkedUntil
		}
		if w.End.After(start) {
			r.Seen.WorkSeconds += w.End.Sub(start).Seconds()
		}
		if w.End.After(r.Seen.WorkedUntil) {
			r.Seen.WorkedUntil = w.End
		}
	}
	return tookHours
}

// shown is the run's cost as the hub shows it: its tasks and calls from
// its events; its spend as the hub read it unless its events add up to
// more (the hub read it too late for the hours that left the summary);
// and the longer of the work times.
func (r *Run) shown() Cost {
	c := r.Events
	if r.Seen == nil {
		return c
	}
	if r.Seen.Scope != "" {
		if sp := r.Seen.spend(); sp.USD >= c.USD {
			c.USD, c.Tokens = sp.USD, sp.Tokens
		}
	}
	c.WorkSeconds = max(c.WorkSeconds, r.Seen.WorkSeconds)
	return c
}

// spendSeen reports whether the run's spend is the one the hub read of its
// AgentAPI: a missed task event doesn't lower it.
func (r *Run) spendSeen() bool {
	return r.Seen != nil && r.Seen.Scope != "" && r.Seen.spend().USD >= r.Events.USD
}
