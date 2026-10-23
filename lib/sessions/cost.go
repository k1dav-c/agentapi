package sessions

import (
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// ModelCall is one call a session made to a model, as its log has it.
type ModelCall struct {
	Time   time.Time      `json:"time" doc:"When the call was made."`
	Model  string         `json:"model" doc:"The model called."`
	Tokens pricing.Tokens `json:"tokens" doc:"The tokens it used."`
	// USD is the API-equivalent cost, and Priced false when no price of the
	// model is known (then USD is 0).
	USD    float64 `json:"usd" doc:"What the call costs at the model provider's list prices, in USD: the API-equivalent cost, which a subscription doesn't charge. 0 when the model's price isn't known."`
	Priced bool    `json:"priced" doc:"The model's price is known."`
	// SubagentID is set on a subagent's calls.
	SubagentID string `json:"subagent_id,omitempty" doc:"The subagent that made the call; missing for the agent's own."`
	// Unitemized marks what Claude Code counted that its log doesn't list
	// call by call (compacting the context, among others).
	Unitemized bool `json:"unitemized,omitempty" doc:"Calls Claude Code counted in its own total but didn't log one by one, such as compacting the context: the difference up to this point."`
	// report is a Claude Code cost-state line: what it counted so far.
	report map[string]agentlog.ModelReport
	// key identifies the call across logs: a fork's log copies the calls of
	// the session it forked.
	key string
	// cost is what the agent itself said the call cost (Pi), or -1.
	cost float64
}

// ModelCost is what a session spent on one model.
type ModelCost struct {
	Model  string         `json:"model" doc:"The model."`
	USD    float64        `json:"usd" doc:"API-equivalent cost in USD."`
	Tokens pricing.Tokens `json:"tokens" doc:"The tokens used."`
	Calls  int            `json:"calls" doc:"How many calls were made."`
	Priced bool           `json:"priced" doc:"The model's price is known; when it isn't, its calls count no cost."`
}

// HourCost is what was spent in one hour.
type HourCost struct {
	Hour time.Time `json:"hour" doc:"The start of the hour, in UTC."`
	USD  float64   `json:"usd" doc:"API-equivalent cost in USD."`
}

// SessionCost is what a session (or several) spent, its subagents
// included.
type SessionCost struct {
	USD    float64        `json:"usd" doc:"API-equivalent cost in USD: what the tokens cost at the model providers' list prices, which a subscription doesn't charge."`
	Tokens pricing.Tokens `json:"tokens" doc:"The tokens used."`
	Calls  int            `json:"calls" doc:"How many model calls were made."`
	// Models are the models called, the most expensive first.
	Models []ModelCost `json:"models" nullable:"false" doc:"What was spent on each model, the most expensive first."`
	// Hourly are the hours with calls, the earliest first.
	Hourly []HourCost `json:"hourly" nullable:"false" doc:"What was spent in each hour (UTC) with calls, the earliest first, for totals over days, weeks or months in the reader's time zone."`
}

// costSum adds calls up into a SessionCost, finding a call's model and
// hour by key: a long session makes tens of thousands of calls over
// hundreds of hours.
type costSum struct {
	c      SessionCost
	models map[string]int
	hours  map[int64]int
}

// add counts call.
func (s *costSum) add(call ModelCall) {
	c := &s.c
	c.USD += call.USD
	c.Tokens = c.Tokens.Add(call.Tokens)
	c.Calls++
	i, ok := s.models[call.Model]
	if !ok {
		c.Models = append(c.Models, ModelCost{Model: call.Model, Priced: call.Priced})
		i = len(c.Models) - 1
		s.models[call.Model] = i
	}
	m := &c.Models[i]
	m.USD += call.USD
	m.Tokens = m.Tokens.Add(call.Tokens)
	m.Calls++
	hour := call.Time.UTC().Truncate(time.Hour)
	j, ok := s.hours[hour.Unix()]
	if !ok {
		c.Hourly = append(c.Hourly, HourCost{Hour: hour})
		j = len(c.Hourly) - 1
		s.hours[hour.Unix()] = j
	}
	c.Hourly[j].USD += call.USD
}

// sorted puts the models and hours in order.
func (c SessionCost) sorted() SessionCost {
	if c.Models == nil {
		c.Models = []ModelCost{}
	}
	if c.Hourly == nil {
		c.Hourly = []HourCost{}
	}
	slices.SortStableFunc(c.Models, func(a, b ModelCost) int {
		switch {
		case a.USD > b.USD:
			return -1
		case a.USD < b.USD:
			return 1
		}
		return strings.Compare(a.Model, b.Model)
	})
	slices.SortFunc(c.Hourly, func(a, b HourCost) int { return a.Hour.Compare(b.Hour) })
	return c
}

// CostOf sums calls.
func CostOf(calls []ModelCall) SessionCost {
	sum := costSum{models: make(map[string]int), hours: make(map[int64]int)}
	for _, call := range calls {
		sum.add(call)
	}
	return sum.c.sorted()
}

// DirectoryCost is what each session of a directory spent, and all of them.
type DirectoryCost struct {
	// Sessions maps a session's ID to its calls, its subagents' included.
	// A call a fork copied from an earlier session counts in that one.
	Sessions map[string][]ModelCall
	Total    SessionCost
}

// Cost is what session id spent.
func (d DirectoryCost) Cost(id string) SessionCost { return CostOf(d.Sessions[id]) }

// Costs reads what every session spent: each session's model calls and
// its subagents', from their logs. A fork's log copies the calls of the
// session it continues, which count once, in the earlier session.
// Concurrent calls share one reading, and its result: the caller must not
// change it.
func (s *Store) Costs() (DirectoryCost, error) {
	d, err, _ := s.costs.Do("", func() (any, error) { return s.readCosts() })
	if err != nil {
		return DirectoryCost{}, err
	}
	return d.(DirectoryCost), nil
}

// readCosts reads what every session spent, for Costs.
func (s *Store) readCosts() (DirectoryCost, error) {
	s.costPasses.Add(1)
	if s.costPass != nil {
		s.costPass()
	}
	files, err := s.src.files(s)
	if err != nil {
		return DirectoryCost{}, err
	}
	type session struct {
		id string
		// born is when the log was created, if known; started its first
		// line's time; changed when it last changed.
		born, started, changed time.Time
		calls                  []ModelCall
	}
	var sessions []session
	subagents := s.subagentIndex()
	// read are the logs this pass read: what the Store keeps of any other
	// is of a log that is gone.
	read, readSubagents := make(map[string]bool), make(map[string]bool)
	for _, path := range files {
		read[path] = true
		snap, err := s.readLog(path, true)
		if err != nil || !snap.session {
			continue
		}
		calls := modelCalls(snap.calls, "")
		for _, sub := range subagents(snap.summary.ID) {
			readSubagents[sub.path] = true
			if subSnap, err := s.readSubagentLog(sub.path, true); err == nil {
				calls = append(calls, modelCalls(subSnap.calls, sub.ID)...)
			}
		}
		for i := range calls {
			calls[i] = priced(calls[i], s.prices)
		}
		calls = reconcile(calls)
		sessions = append(sessions, session{id: snap.summary.ID, born: snap.info.Birth, started: snap.summary.CreatedAt, changed: snap.info.ModTime, calls: calls})
	}
	s.logs.Retain(func(path string) bool { return read[path] })
	s.subs.Retain(func(path string) bool { return readSubagents[path] })
	// A call two logs have counts in the session whose log came first. A
	// fork's log starts with the lines it copied, and their times, so the
	// file's creation tells best; the file system may not record it, or
	// record it coarsely.
	slices.SortStableFunc(sessions, func(a, b session) int {
		if !a.born.IsZero() && !b.born.IsZero() && !a.born.Equal(b.born) {
			return a.born.Compare(b.born)
		}
		if c := a.started.Compare(b.started); c != 0 {
			return c
		}
		return a.changed.Compare(b.changed)
	})

	d := DirectoryCost{Sessions: make(map[string][]ModelCall, len(sessions))}
	seen := make(map[string]bool)
	var all []ModelCall
	for _, s := range sessions {
		var own []ModelCall
		for _, call := range s.calls {
			if call.key != "" {
				if seen[call.key] {
					continue
				}
				seen[call.key] = true
			}
			own = append(own, call)
		}
		d.Sessions[s.id] = own
		all = append(all, own...)
	}
	d.Total = CostOf(all)
	return d, nil
}

// modelCalls are the calls a log records, of the subagent subagentID if
// set.
func modelCalls(calls []agentlog.Call, subagentID string) []ModelCall {
	out := make([]ModelCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, ModelCall{
			Time: c.Time, Model: c.Model, Tokens: c.Tokens, SubagentID: subagentID,
			report: c.Report, key: c.Key, cost: c.Cost,
		})
	}
	return out
}

// priced returns call with its cost at prices, or at what the agent said.
func priced(call ModelCall, prices *pricing.Table) ModelCall {
	if call.report != nil {
		return call
	}
	if call.cost >= 0 {
		call.USD, call.Priced = call.cost, true
		return call
	}
	if p, ok := prices.Lookup(call.Model); ok {
		call.USD, call.Priced = p.Cost(call.Tokens), true
	}
	return call
}

// unitemizedMin is the smallest difference to Claude Code's own count that
// is added as unitemized: below it, rounding.
const unitemizedMin = 0.01

// reconcile turns Claude Code's cost-state lines among calls into calls
// for what it counted but didn't log call by call: at each line, what it
// counted for a model so far less the calls of that model up to then. The
// session's calls, its subagents' included, then add up to Claude Code's
// own total. The lines themselves are dropped.
func reconcile(calls []ModelCall) []ModelCall {
	var reports []ModelCall
	calls = slices.DeleteFunc(calls, func(c ModelCall) bool {
		if c.report != nil {
			reports = append(reports, c)
			return true
		}
		return false
	})
	if len(reports) == 0 {
		return calls
	}
	slices.SortStableFunc(calls, func(a, b ModelCall) int { return a.Time.Compare(b.Time) })
	counted := make(map[string]agentlog.ModelReport)
	next := 0
	var added []ModelCall
	for _, r := range reports {
		if r.Time.IsZero() {
			// Before any line with a time: nothing to compare it with.
			continue
		}
		for ; next < len(calls) && !calls[next].Time.After(r.Time); next++ {
			c := counted[calls[next].Model]
			c.USD += calls[next].USD
			c.Tokens = c.Tokens.Add(calls[next].Tokens)
			counted[calls[next].Model] = c
		}
		for model, reported := range r.report {
			c := counted[model]
			missing := reported.USD - c.USD
			if missing < unitemizedMin {
				continue
			}
			tokens := pricing.Tokens{
				Input:      max(0, reported.Tokens.Input-c.Tokens.Input),
				CacheRead:  max(0, reported.Tokens.CacheRead-c.Tokens.CacheRead),
				CacheWrite: max(0, reported.Tokens.CacheWrite-c.Tokens.CacheWrite-c.Tokens.CacheWrite1h),
				Output:     max(0, reported.Tokens.Output-c.Tokens.Output),
			}
			// Priced by Claude Code itself.
			call := ModelCall{Time: r.Time, Model: model, Tokens: tokens, USD: missing, Priced: true, Unitemized: true, cost: -1}
			added = append(added, call)
			c.USD += missing
			c.Tokens = c.Tokens.Add(tokens)
			counted[model] = c
		}
	}
	calls = append(calls, added...)
	slices.SortStableFunc(calls, func(a, b ModelCall) int { return a.Time.Compare(b.Time) })
	return calls
}

// subagentIndex returns, for a session ID, the subagents whose logs say
// they are its. Codex keeps every thread in one tree, so its subagents are
// found in one pass over its logs.
func (s *Store) subagentIndex() func(id string) []Subagent {
	if s.agent != "codex" {
		return func(id string) []Subagent {
			subs, _ := s.Subagents(id)
			return subs
		}
	}
	threads, _ := s.codexThreads()
	byParent := make(map[string][]Subagent)
	for _, t := range threads {
		if !t.meta.IsSubAgent() {
			continue
		}
		// As Subagents finds them: those of the thread, and those of the
		// subagents its session ran.
		sub := Subagent{ID: t.meta.Payload.ID, path: t.path, kind: "codex", from: t.meta.Payload.HistoryStart}
		for _, parent := range slices.Compact([]string{t.meta.Payload.ParentThreadID, t.meta.Payload.SessionID}) {
			if parent != "" && parent != sub.ID {
				byParent[parent] = append(byParent[parent], sub)
			}
		}
	}
	return func(id string) []Subagent { return byParent[id] }
}
