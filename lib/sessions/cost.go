package sessions

import (
	"hash/fnv"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
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

// ModelCost is what a session spent on one model. Priced is false when
// any of its calls had no price.
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
// hundreds of hours. Calls follow each other in time, mostly of one model:
// the last model and hour are kept at hand.
type costSum struct {
	c      SessionCost
	models map[string]int
	hours  map[int64]int
	// model is the index of the last call's model in c.Models, hour of
	// its hour in c.Hourly, which starts at hourStart (Unix seconds).
	model, hour int
	hourStart   int64
}

// add counts call.
func (s *costSum) add(call ModelCall) {
	c := &s.c
	c.USD += call.USD
	c.Tokens = c.Tokens.Add(call.Tokens)
	c.Calls++
	if s.model < 0 || c.Models[s.model].Model != call.Model {
		i, ok := s.models[call.Model]
		if !ok {
			c.Models = append(c.Models, ModelCost{Model: call.Model, Priced: call.Priced})
			i = len(c.Models) - 1
			s.models[call.Model] = i
		}
		s.model = i
	}
	m := &c.Models[s.model]
	m.Priced = m.Priced && call.Priced
	m.USD += call.USD
	m.Tokens = m.Tokens.Add(call.Tokens)
	m.Calls++
	if at := call.Time.Unix(); s.hour < 0 || at < s.hourStart || at >= s.hourStart+3600 {
		hour := call.Time.UTC().Truncate(time.Hour)
		j, ok := s.hours[hour.Unix()]
		if !ok {
			c.Hourly = append(c.Hourly, HourCost{Hour: hour})
			j = len(c.Hourly) - 1
			s.hours[hour.Unix()] = j
		}
		s.hour, s.hourStart = j, hour.Unix()
	}
	c.Hourly[s.hour].USD += call.USD
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

// merge counts what c, a sum of calls, counts.
func (s *costSum) merge(c SessionCost) {
	s.c.USD += c.USD
	s.c.Tokens = s.c.Tokens.Add(c.Tokens)
	s.c.Calls += c.Calls
	for _, m := range c.Models {
		i, ok := s.models[m.Model]
		if !ok {
			s.c.Models = append(s.c.Models, ModelCost{Model: m.Model, Priced: m.Priced})
			i = len(s.c.Models) - 1
			s.models[m.Model] = i
		}
		sm := &s.c.Models[i]
		sm.Priced = sm.Priced && m.Priced
		sm.USD += m.USD
		sm.Tokens = sm.Tokens.Add(m.Tokens)
		sm.Calls += m.Calls
	}
	for _, h := range c.Hourly {
		j, ok := s.hours[h.Hour.Unix()]
		if !ok {
			s.c.Hourly = append(s.c.Hourly, HourCost{Hour: h.Hour})
			j = len(s.c.Hourly) - 1
			s.hours[h.Hour.Unix()] = j
		}
		s.c.Hourly[j].USD += h.USD
	}
}

func newCostSum() costSum {
	return costSum{models: make(map[string]int), hours: make(map[int64]int), model: -1, hour: -1}
}

// CostOf sums calls.
func CostOf(calls []ModelCall) SessionCost {
	sum := newCostSum()
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
	// Total is what the sessions' costs add up to.
	Total SessionCost
	// costs are what each session spent.
	costs map[string]SessionCost
}

// Cost is what session id spent.
func (d DirectoryCost) Cost(id string) SessionCost {
	if c, ok := d.costs[id]; ok {
		return c
	}
	return CostOf(d.Sessions[id])
}

// Costs reads what every session spent: each session's model calls and
// its subagents', from their logs. A fork's log copies the calls of the
// session it continues, which count once, in the earlier session.
// Concurrent calls share one reading, and its result: the caller must not
// change it. A reading returns the last one's result when no log's calls
// changed, and otherwise works out again the sessions whose logs did.
func (s *Store) Costs() (DirectoryCost, error) {
	d, err, _ := s.costs.Do("", func() (any, error) { return s.readCosts() })
	if err != nil {
		return DirectoryCost{}, err
	}
	return d.(DirectoryCost), nil
}

// costCache is what a reading of the costs leaves the next one.
type costCache struct {
	// sessions are the sessions read, by the path of their log, and
	// order the same in the order a call two logs have counts in the
	// first.
	sessions map[string]*sessionCost
	order    []*sessionCost
	// owner is the session whose log has a call first, by the call's key.
	owner  map[string]*sessionCost
	result DirectoryCost
}

// sessionCost is what the readings of the costs found of a session.
type sessionCost struct {
	id string
	// born is when the log was created, if known; started its first
	// line's time; changed when it last changed.
	born, started, changed time.Time
	// reads are the readings of the session's log and its subagents' that
	// calls were made of, in that order.
	reads []logRead
	// calls are the session's calls, its subagents' included, priced and
	// reconciled; own those that count in it (not those of an earlier
	// session's log), and cost what they add up to.
	calls []ModelCall
	own   []ModelCall
	cost  SessionCost
	// fresh says this reading made calls, of the logs' calls logCalls;
	// grown that they have every call they had before, of the readings
	// before.
	fresh, grown bool
	logCalls     [][]ModelCall
	before       []logRead
	// twice says calls has a key twice, which counts once; allOwn that
	// own is calls.
	twice, allOwn bool
	pos           int
}

// subagentCalls are the calls of a subagent's log.
type subagentCalls struct {
	firstAt time.Time
	calls   []ModelCall
	read    logRead
}

// readCosts reads what every session spent, for Costs. It reads only what
// the logs had appended since the last reading, prices only the calls
// read or changed since, and works out again only the sessions whose
// calls changed. Its readings of the logs are passes, which the indexes
// don't forget for being past their bounds.
func (s *Store) readCosts() (DirectoryCost, error) {
	s.costPasses.Add(1)
	if s.costPass != nil {
		s.costPass()
	}
	files, subagents, err := s.costLogs()
	if err != nil {
		return DirectoryCost{}, err
	}
	logs, subs := s.logs.Pass(), s.subs.Pass()
	// What a pass didn't read is of a log that is gone.
	defer logs.End()
	defer subs.End()

	c := &s.cost
	sessions := make([]*sessionCost, 0, len(c.order))
	byPath := make(map[string]*sessionCost, len(c.order))
	changed := false
	for _, path := range files {
		snap, err := s.readLog(logs, path, true)
		if err != nil || !snap.session {
			continue
		}
		var subCalls []subagentCalls
		for _, sub := range subagents(snap.summary.ID, path) {
			subSnap, err := s.readSubagentLog(subs, sub.path, true)
			if err != nil {
				continue
			}
			subSnap.read.subagent = sub.ID
			subCalls = append(subCalls, subagentCalls{firstAt: subSnap.firstAt, calls: subSnap.calls, read: subSnap.read})
		}
		if s.agent == "claude" {
			// In the order they started, as Subagents has them.
			slices.SortStableFunc(subCalls, func(a, b subagentCalls) int { return a.firstAt.Compare(b.firstAt) })
		}
		reads := make([]logRead, 0, 1+len(subCalls))
		reads = append(reads, snap.read)
		for _, sub := range subCalls {
			reads = append(reads, sub.read)
		}
		sc := c.sessions[path]
		if sc == nil || sc.id != snap.summary.ID {
			sc = &sessionCost{id: snap.summary.ID}
		}
		sc.fresh = sc.reads == nil || !slices.Equal(sc.reads, reads)
		if sc.fresh {
			changed = true
			sc.grown = grown(sc.reads, reads)
			sc.before, sc.reads = sc.reads, reads
			sc.calls = assemble(snap.calls, subCalls)
			sc.logCalls = append(make([][]ModelCall, 0, len(reads)), snap.calls)
			for _, sub := range subCalls {
				sc.logCalls = append(sc.logCalls, sub.calls)
			}
		}
		sc.born, sc.started, sc.changed = snap.info.Birth, snap.summary.CreatedAt, snap.info.ModTime
		byPath[path] = sc
		sessions = append(sessions, sc)
	}
	// A call two logs have counts in the session whose log came first. A
	// fork's log starts with the lines it copied, and their times, so the
	// file's creation tells best; the file system may not record it, or
	// record it coarsely.
	slices.SortStableFunc(sessions, func(a, b *sessionCost) int {
		if !a.born.IsZero() && !b.born.IsZero() && !a.born.Equal(b.born) {
			return a.born.Compare(b.born)
		}
		if c := a.started.Compare(b.started); c != 0 {
			return c
		}
		return a.changed.Compare(b.changed)
	})
	same := c.owner != nil && slices.Equal(sessions, c.order)
	c.sessions, c.order = byPath, sessions
	if same && !changed {
		return c.result, nil
	}
	for i, sc := range sessions {
		sc.pos = i
	}
	if same && !slices.ContainsFunc(sessions, func(sc *sessionCost) bool { return sc.fresh && !sc.grown }) {
		// The same sessions, whose calls only grew: what changed is the
		// fresh ones' calls, and those of the sessions that had a call a
		// fresh one has before them.
		dirty := make(map[*sessionCost]bool)
		for _, sc := range sessions {
			if sc.fresh {
				dirty[sc] = true
				c.claimAdded(sc, dirty)
			}
		}
		for sc := range dirty {
			if sc.fresh && sc.allOwn && !sc.twice {
				sc.own = sc.calls
			} else {
				sc.own = sc.ownCalls(c.owner)
			}
			sc.cost = CostOf(sc.own)
		}
	} else {
		c.owner = make(map[string]*sessionCost, len(c.owner))
		for _, sc := range sessions {
			c.claim(sc)
		}
		for _, sc := range sessions {
			sc.own = sc.ownCalls(c.owner)
			sc.cost = CostOf(sc.own)
		}
	}
	d := DirectoryCost{
		Sessions: make(map[string][]ModelCall, len(sessions)),
		costs:    make(map[string]SessionCost, len(sessions)),
	}
	total := newCostSum()
	for _, sc := range sessions {
		d.Sessions[sc.id] = sc.own
		d.costs[sc.id] = sc.cost
		total.merge(sc.cost)
	}
	d.Total = total.c.sorted()
	c.result = d
	for _, sc := range sessions {
		sc.logCalls, sc.before = nil, nil
	}
	return d, nil
}

// grown reports whether reads read on from where old stopped: the same
// readers of the same logs, and maybe more logs after them. Their calls
// then have every key they had: a reader's calls only grow, or change
// their tokens.
func grown(old, reads []logRead) bool {
	if old == nil || len(reads) < len(old) {
		return false
	}
	for i, r := range old {
		if reads[i].reader != r.reader || reads[i].subagent != r.subagent {
			return false
		}
	}
	return true
}

// claim makes sc the owner of its calls that no session before it has,
// the sessions taken in order.
func (c *costCache) claim(sc *sessionCost) {
	sc.twice = false
	for _, call := range sc.calls {
		if call.key == "" {
			continue
		}
		switch o, ok := c.owner[call.key]; {
		case !ok:
			c.owner[call.key] = sc
		case o == sc:
			sc.twice = true
		}
	}
}

// claimAdded is claim for a session whose calls grew, of the sessions of
// the reading before, in their order: only the calls its logs added can
// have keys it didn't have. It takes those of a session after it, which it
// adds to dirty.
func (c *costCache) claimAdded(sc *sessionCost, dirty map[*sessionCost]bool) {
	for i, calls := range sc.logCalls {
		if i < len(sc.before) {
			calls = calls[sc.before[i].calls:]
		}
		for _, call := range calls {
			if call.key == "" {
				continue
			}
			o, ok := c.owner[call.key]
			switch {
			case !ok || o.pos > sc.pos:
				if ok {
					dirty[o] = true
				}
				c.owner[call.key] = sc
			case o != sc:
				// An earlier session's.
				sc.allOwn = false
			default:
				sc.twice = true
			}
		}
	}
}

// ownCalls are the calls that count in sc: those without a key, and the
// first of those whose key it owns.
func (sc *sessionCost) ownCalls(owner map[string]*sessionCost) []ModelCall {
	all := !sc.twice
	for i := 0; all && i < len(sc.calls); i++ {
		all = sc.calls[i].key == "" || owner[sc.calls[i].key] == sc
	}
	sc.allOwn = all
	if all {
		return sc.calls
	}
	var own []ModelCall
	counted := make(map[string]bool)
	for _, call := range sc.calls {
		if call.key != "" {
			if owner[call.key] != sc || counted[call.key] {
				continue
			}
			counted[call.key] = true
		}
		own = append(own, call)
	}
	return own
}

// assemble returns a session's calls: its log's, and its subagents',
// reconciled with Claude Code's own count. Without either, they are its
// log's calls as they are.
func assemble(own []ModelCall, subs []subagentCalls) []ModelCall {
	if len(subs) == 0 && !slices.ContainsFunc(own, func(c ModelCall) bool { return c.report != nil }) {
		return own
	}
	n := len(own)
	for _, sub := range subs {
		n += len(sub.calls)
	}
	calls := make([]ModelCall, 0, n)
	calls = append(calls, own...)
	for _, sub := range subs {
		for _, call := range sub.calls {
			call.SubagentID = sub.read.subagent
			calls = append(calls, call)
		}
	}
	calls = reconcile(calls)
	if len(calls) == 0 {
		return nil
	}
	return calls
}

// costLogs lists the logs a reading of the costs reads: the sessions', and
// a function that lists the subagents of the session id whose log is at
// path. Codex keeps every thread in one tree, so one walk finds both.
func (s *Store) costLogs() ([]string, func(id, path string) []Subagent, error) {
	switch s.agent {
	case "codex":
		threads, err := s.codexThreads()
		if err != nil {
			return nil, nil, err
		}
		byParent := make(map[string][]Subagent)
		for _, t := range threads {
			if !t.meta.IsSubAgent() {
				continue
			}
			// As Subagents finds them: those of the thread, and those of
			// the subagents its session ran.
			sub := Subagent{ID: t.meta.Payload.ID, path: t.path, kind: "codex", from: t.meta.Payload.HistoryStart}
			for _, parent := range slices.Compact([]string{t.meta.Payload.ParentThreadID, t.meta.Payload.SessionID}) {
				if parent != "" && parent != sub.ID {
					byParent[parent] = append(byParent[parent], sub)
				}
			}
		}
		return codexSessionLogs(s, threads), func(id, _ string) []Subagent { return byParent[id] }, nil
	case "claude":
		files, dirs, err := claudeLogs(s)
		if err != nil {
			return nil, nil, err
		}
		sessionDirs := make(map[string]bool, len(dirs))
		for _, dir := range dirs {
			sessionDirs[dir] = true
		}
		return files, func(id, path string) []Subagent {
			// Most sessions ran none, and have no directory.
			if !sessionDirs[filepath.Join(filepath.Dir(path), id)] {
				return nil
			}
			return claudeSubagentLogs(id, path)
		}, nil
	}
	files, err := s.src.files(s)
	return files, func(string, string) []Subagent { return nil }, err
}

// price returns call at the store's prices, or at what the agent said. A
// model without a price is logged once, the first time a call of it is.
func (s *Store) price(c agentlog.Call) ModelCall {
	call := priced(ModelCall{
		Time: c.Time, Model: c.Model, Tokens: c.Tokens,
		report: c.Report, key: c.Key, cost: c.Cost,
	}, s.prices)
	if !call.Priced && call.report == nil && call.Model != "" {
		if _, warned := s.unpriced.LoadOrStore(call.Model, true); !warned {
			s.logger.Warn("Session log has calls to a model without a known price; they count no cost (--prices adds one)",
				"agent", s.agent, "model", call.Model)
		}
	}
	return call
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
	if !slices.IsSortedFunc(calls, byTime) {
		slices.SortStableFunc(calls, byTime)
	}
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
		// By model, in order: the calls added at one time keep it.
		for _, model := range slices.Sorted(maps.Keys(r.report)) {
			reported := r.report[model]
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
	if len(added) == 0 {
		return calls
	}
	if !slices.IsSortedFunc(added, byTime) {
		calls = append(calls, added...)
		slices.SortStableFunc(calls, byTime)
		return calls
	}
	// Both in order: merged, the calls first at one time, as sorting them
	// one after the other would.
	merged := make([]ModelCall, 0, len(calls)+len(added))
	for len(calls) > 0 && len(added) > 0 {
		if added[0].Time.Before(calls[0].Time) {
			merged, added = append(merged, added[0]), added[1:]
		} else {
			merged, calls = append(merged, calls[0]), calls[1:]
		}
	}
	merged = append(merged, calls...)
	return append(merged, added...)
}

// byTime orders calls by their time.
func byTime(a, b ModelCall) int { return a.Time.Compare(b.Time) }

// CallsDigest identifies calls, in order, so a client that has them can
// ask for the ones after them only: FNV-1a over each call's time, model,
// tokens, cost and subagent, a line each ("%d|%s|%d|%d|%d|%d|%d|%x|%s|%t\n"
// of them). Empty for none. A chat asks for it on every turn, of sessions
// of tens of thousands of calls: the lines are written into one buffer.
func CallsDigest(calls []ModelCall) string {
	if len(calls) == 0 {
		return ""
	}
	h := fnv.New64a()
	var line []byte
	for _, call := range calls {
		t := call.Tokens
		line = strconv.AppendInt(line[:0], call.Time.UnixNano(), 10)
		line = append(append(line, '|'), call.Model...)
		for _, n := range [...]int{t.Input, t.CacheRead, t.CacheWrite, t.CacheWrite1h, t.Output} {
			line = strconv.AppendInt(append(line, '|'), int64(n), 10)
		}
		line = strconv.AppendUint(append(line, '|'), math.Float64bits(call.USD), 16)
		line = append(append(line, '|'), call.SubagentID...)
		line = strconv.AppendBool(append(line, '|'), call.Unitemized)
		_, _ = h.Write(append(line, '\n'))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}
