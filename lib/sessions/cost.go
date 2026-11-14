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
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerlog"
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
	// Unitemized marks the tokens Claude Code counted that its log doesn't
	// list call by call (compacting the context, among others).
	Unitemized bool `json:"unitemized,omitempty" doc:"Tokens Claude Code counted in its own total but didn't log call by call, such as compacting the context: those up to this point, priced as any call."`
	// Provider is the AgentAPI model provider the call went to, by its ID;
	// empty for the agent's own (provider.Default).
	Provider string `json:"provider,omitempty" doc:"The AgentAPI model provider the call went to, by its ID (as in GET /settings/options); missing for the agent's own provider (default). Codex's log names it; a Claude Code call goes to the provider AgentAPI last recorded its session on before the call (its provider ledger)."`
	// report is a Claude Code cost-state line: what it counted so far.
	report map[string]agentlog.ModelReport
	// key identifies the call across logs: a fork's log copies the calls of
	// the session it forked.
	key string
	// cost is what the agent itself said the call cost (Pi), or -1.
	cost float64
	// providerPriced says USD is at its provider's price of the model
	// (atProviderPrices), not at the store's prices.
	providerPriced bool
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

// ProviderCost is what a session (or several) spent on one model provider.
// Priced is false when any of its calls had no price.
type ProviderCost struct {
	Provider string         `json:"provider" doc:"The AgentAPI model provider, by its ID; default for the agent's own."`
	USD      float64        `json:"usd" doc:"API-equivalent cost in USD, at the list prices of the models called, whichever provider served them."`
	Tokens   pricing.Tokens `json:"tokens" doc:"The tokens used."`
	Calls    int            `json:"calls" doc:"How many calls were made."`
	Priced   bool           `json:"priced" doc:"The price of every model called is known; when one isn't, its calls count no cost."`
	// hourly is what was spent on the provider in each hour with calls,
	// for the summary's spend by provider.
	hourly []HourCost
}

// Hourly is what was spent on the provider in each hour (UTC) with calls,
// the earliest first. The caller must not change it.
func (p ProviderCost) Hourly() []HourCost { return p.hourly }

// providerName is the provider a call with provider id went to, as
// ProviderCost names it.
func providerName(id string) string {
	if id == "" {
		return provider.Default
	}
	return id
}

// HourCost is what was spent in one hour.
type HourCost struct {
	Hour   time.Time      `json:"hour" doc:"The start of the hour, in UTC."`
	USD    float64        `json:"usd" doc:"API-equivalent cost in USD."`
	Tokens pricing.Tokens `json:"tokens" doc:"The tokens used, those of models without a known price included."`
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
	// Providers are the model providers called, the most expensive first.
	Providers []ProviderCost `json:"providers" nullable:"false" doc:"What was spent on each AgentAPI model provider (default for the agent's own), the most expensive first, then the one with the most tokens."`
}

// costSum adds calls up into a SessionCost, finding a call's model and
// hour by key: a long session makes tens of thousands of calls over
// hundreds of hours. Calls follow each other in time, mostly of one model:
// the last model and hour are kept at hand.
type costSum struct {
	c         SessionCost
	models    map[string]int
	hours     map[int64]int
	providers map[string]int
	// providerHours finds an hour in the hourly of a provider of
	// c.Providers, by the provider's index and the hour.
	providerHours map[providerHour]int
	// model is the index of the last call's model in c.Models, hour of
	// its hour in c.Hourly, which starts at hourStart (Unix seconds);
	// provider of its provider in c.Providers, and providerHour of its
	// hour in that provider's hourly, which starts at providerStart.
	model, hour            int
	hourStart              int64
	provider, providerHour int
	providerStart          int64
}

// providerHour is an hour of a provider's: its index in a sum's providers
// and the hour's start (Unix seconds).
type providerHour struct {
	provider int
	hour     int64
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
	h := &c.Hourly[s.hour]
	h.USD += call.USD
	h.Tokens = h.Tokens.Add(call.Tokens)
	name := providerName(call.Provider)
	if s.provider < 0 || c.Providers[s.provider].Provider != name {
		s.provider = s.providerIndex(name, call.Priced)
		s.providerHour = -1
	}
	p := &c.Providers[s.provider]
	p.Priced = p.Priced && call.Priced
	p.USD += call.USD
	p.Tokens = p.Tokens.Add(call.Tokens)
	p.Calls++
	if at := call.Time.Unix(); s.providerHour < 0 || at < s.providerStart || at >= s.providerStart+3600 {
		s.providerHour = s.providerHourIndex(s.provider, call.Time.UTC().Truncate(time.Hour))
		s.providerStart = p.hourly[s.providerHour].Hour.Unix()
	}
	ph := &p.hourly[s.providerHour]
	ph.USD += call.USD
	ph.Tokens = ph.Tokens.Add(call.Tokens)
}

// providerIndex is the index of provider name in s.c.Providers, which it
// adds to.
func (s *costSum) providerIndex(name string, priced bool) int {
	i, ok := s.providers[name]
	if !ok {
		s.c.Providers = append(s.c.Providers, ProviderCost{Provider: name, Priced: priced})
		i = len(s.c.Providers) - 1
		s.providers[name] = i
	}
	return i
}

// providerHourIndex is the index of hour in the hourly of the provider at
// index i of s.c.Providers, which it adds to.
func (s *costSum) providerHourIndex(i int, hour time.Time) int {
	key := providerHour{provider: i, hour: hour.Unix()}
	j, ok := s.providerHours[key]
	if !ok {
		p := &s.c.Providers[i]
		p.hourly = append(p.hourly, HourCost{Hour: hour})
		j = len(p.hourly) - 1
		s.providerHours[key] = j
	}
	return j
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
	slices.SortFunc(c.Hourly, byHour)
	if c.Providers == nil {
		c.Providers = []ProviderCost{}
	}
	slices.SortStableFunc(c.Providers, func(a, b ProviderCost) int {
		switch {
		case a.USD > b.USD:
			return -1
		case a.USD < b.USD:
			return 1
		}
		if ta, tb := tokenTotal(a.Tokens), tokenTotal(b.Tokens); ta != tb {
			return tb - ta
		}
		return strings.Compare(a.Provider, b.Provider)
	})
	for _, p := range c.Providers {
		slices.SortFunc(p.hourly, byHour)
	}
	return c
}

// byHour orders hours by their start.
func byHour(a, b HourCost) int { return a.Hour.Compare(b.Hour) }

// tokenTotal is how many tokens t counts, of every kind.
func tokenTotal(t pricing.Tokens) int {
	return t.Input + t.CacheRead + t.CacheWrite + t.CacheWrite1h + t.Output
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
		sh := &s.c.Hourly[j]
		sh.USD += h.USD
		sh.Tokens = sh.Tokens.Add(h.Tokens)
	}
	for _, p := range c.Providers {
		i := s.providerIndex(p.Provider, p.Priced)
		sp := &s.c.Providers[i]
		sp.Priced = sp.Priced && p.Priced
		sp.USD += p.USD
		sp.Tokens = sp.Tokens.Add(p.Tokens)
		sp.Calls += p.Calls
		for _, h := range p.hourly {
			j := s.providerHourIndex(i, h.Hour)
			sh := &s.c.Providers[i].hourly[j]
			sh.USD += h.USD
			sh.Tokens = sh.Tokens.Add(h.Tokens)
		}
	}
	s.provider = -1
}

func newCostSum() costSum {
	return costSum{
		models: make(map[string]int), hours: make(map[int64]int),
		providers: make(map[string]int), providerHours: make(map[providerHour]int),
		model: -1, hour: -1, provider: -1, providerHour: -1,
	}
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
	// marksVersion is the provider ledger's version the calls were
	// attributed at.
	marksVersion uint64
	// pricesKey identifies the providers' model prices the calls were
	// priced at (providerPricesKey).
	pricesKey string
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
	// recount says own changed, and cost is to be worked out again; marks
	// are the provider ledger's marks of the session own was attributed
	// with.
	recount bool
	marks   []providerlog.Mark
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
		sc := s.readSessionCost(logs, subs, path, subagents)
		if sc == nil {
			continue
		}
		changed = changed || sc.fresh
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
	marks, marksVersion := s.providerMarks()
	prices := s.providerPrices()
	pricesKey := providerPricesKey(prices)
	if same && !changed && marksVersion == c.marksVersion && pricesKey == c.pricesKey {
		return c.result, nil
	}
	repriced := pricesKey != c.pricesKey
	c.marksVersion, c.pricesKey = marksVersion, pricesKey
	c.count(sessions, same)
	for _, sc := range sessions {
		if m := marks[sc.id]; sc.recount || repriced || !slices.Equal(m, sc.marks) {
			if s.src.ledgered {
				sc.own = attribute(sc.own, m)
			}
			sc.own = s.atProviderPrices(sc.own, prices)
			sc.marks = m
			sc.cost = CostOf(sc.own)
			sc.recount = false
		}
	}
	d := directoryCost(sessions)
	c.result = d
	for _, sc := range sessions {
		sc.logCalls, sc.before = nil, nil
	}
	return d, nil
}

// readSessionCost reads the calls of the session whose log is at path,
// and its subagents' (those subagents lists), through logs and subs:
// what the last reading found of it, fresh if they changed. It returns
// nil for a log that isn't one of the directory's sessions.
func (s *Store) readSessionCost(logs, subs logReader, path string, subagents func(id, path string) []Subagent) *sessionCost {
	snap, err := s.readLog(logs, path, true)
	if err != nil || !snap.session {
		return nil
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
	if s.src.subagentsUnordered {
		// In the order they started, as Subagents has them.
		slices.SortStableFunc(subCalls, func(a, b subagentCalls) int { return a.firstAt.Compare(b.firstAt) })
	}
	reads := make([]logRead, 0, 1+len(subCalls))
	reads = append(reads, snap.read)
	for _, sub := range subCalls {
		reads = append(reads, sub.read)
	}
	sc := s.cost.sessions[path]
	if sc == nil || sc.id != snap.summary.ID {
		sc = &sessionCost{id: snap.summary.ID}
	}
	sc.fresh = sc.reads == nil || !slices.Equal(sc.reads, reads)
	if sc.fresh {
		sc.grown = grown(sc.reads, reads)
		sc.before, sc.reads = sc.reads, reads
		var drift map[string]costDrift
		sc.calls, drift = assemble(snap.calls, subCalls, s.prices)
		s.warnDrift(drift)
		sc.logCalls = append(make([][]ModelCall, 0, len(reads)), snap.calls)
		for _, sub := range subCalls {
			sc.logCalls = append(sc.logCalls, sub.calls)
		}
	}
	sc.born, sc.started, sc.changed = snap.info.Birth, snap.summary.CreatedAt, snap.info.ModTime
	return sc
}

// count works out which calls count in each of sessions, in order, and
// what they add up to. same says they are the sessions of the reading
// before, in its order.
func (c *costCache) count(sessions []*sessionCost, same bool) {
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
			sc.recount = true
		}
		return
	}
	c.owner = make(map[string]*sessionCost, len(c.owner))
	for _, sc := range sessions {
		c.claim(sc)
	}
	for _, sc := range sessions {
		sc.own = sc.ownCalls(c.owner)
		sc.recount = true
	}
}

// providerMarks are the provider ledger's marks of the sessions of an
// agent whose log doesn't name the provider of a call, by session, and the
// ledger's version; none without a ledger or for another agent.
func (s *Store) providerMarks() (map[string][]providerlog.Mark, uint64) {
	if !s.src.ledgered || s.config.Providers == nil {
		return nil, 0
	}
	return s.config.Providers.Sessions(s.agent)
}

// attribute returns the calls of a session with the provider marks say it
// ran on at each one's time (providerlog.At): the agent's own before the
// first, and with none. They are a copy when any provider changes, so
// what holds calls (a log's reading, an earlier result) keeps them as
// they were.
func attribute(calls []ModelCall, marks []providerlog.Mark) []ModelCall {
	first := -1
	for i, call := range calls {
		if providerlog.At(marks, call.Time) != call.Provider {
			first = i
			break
		}
	}
	if first < 0 {
		return calls
	}
	out := slices.Clone(calls)
	for i := first; i < len(out); i++ {
		out[i].Provider = providerlog.At(marks, out[i].Time)
	}
	return out
}

// providerPrices are the model prices of the providers, if the store has
// them (Config.ProviderPrices).
func (s *Store) providerPrices() provider.PriceTable {
	if s.config.ProviderPrices == nil {
		return nil
	}
	return s.config.ProviderPrices()
}

// providerPricesKey identifies prices: when it changes, the calls are
// priced again.
func providerPricesKey(prices provider.PriceTable) string {
	if len(prices) == 0 {
		return ""
	}
	h := fnv.New64a()
	var line []byte
	for _, id := range slices.Sorted(maps.Keys(prices)) {
		models := prices[id]
		for _, model := range slices.Sorted(maps.Keys(models)) {
			p := models[model]
			line = append(append(append(line[:0], id...), '|'), model...)
			for _, v := range [...]float64{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
				line = strconv.AppendUint(append(line, '|'), math.Float64bits(v), 16)
			}
			_, _ = h.Write(append(line, '\n'))
		}
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// atProviderPrices returns calls with those that went to a provider with a
// price of their model at that price, before the store's prices, and those
// priced so before at the store's prices again when their provider no
// longer has one (or the call is attributed to another). They are a copy
// when any cost changes, as attribute's.
func (s *Store) atProviderPrices(calls []ModelCall, prices provider.PriceTable) []ModelCall {
	var out []ModelCall
	for i, call := range calls {
		var next ModelCall
		price, ok := prices.Lookup(call.Provider, call.Model)
		switch {
		case ok && call.Provider != "":
			next = call
			next.USD, next.Priced, next.providerPriced = price.Pricing().Cost(call.Tokens), true, true
		case call.providerPriced:
			next = call
			next.USD, next.Priced, next.providerPriced = 0, false, false
			next = priced(next, s.prices)
		default:
			continue
		}
		if next.USD == call.USD && next.Priced == call.Priced && next.providerPriced == call.providerPriced {
			continue
		}
		if out == nil {
			out = slices.Clone(calls)
		}
		out[i] = next
	}
	if out == nil {
		return calls
	}
	return out
}

// directoryCost is what sessions spent, each and all of them.
func directoryCost(sessions []*sessionCost) DirectoryCost {
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
	return d
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
	// first and last are the calls own holds while they follow each other
	// in calls, as a fork's own calls follow those it copied: own is then
	// a part of calls rather than a copy.
	first, last := -1, -1
	counted := make(map[string]bool)
	for i, call := range sc.calls {
		if call.key != "" {
			if owner[call.key] != sc || counted[call.key] {
				continue
			}
			counted[call.key] = true
		}
		switch {
		case first < 0:
			first, last = i, i+1
		case own == nil && i == last:
			last++
		default:
			if own == nil {
				own = slices.Clone(sc.calls[first:last])
			}
			own = append(own, call)
		}
	}
	if own == nil && first >= 0 {
		return sc.calls[first:last:last]
	}
	return own
}

// assemble returns a session's calls: its log's, and its subagents',
// reconciled with Claude Code's own count at prices, and the models whose
// cost differs from Claude Code's (see reconcile). Without either, they
// are its log's calls as they are.
func assemble(own []ModelCall, subs []subagentCalls, prices *pricing.Table) ([]ModelCall, map[string]costDrift) {
	if len(subs) == 0 && !slices.ContainsFunc(own, func(c ModelCall) bool { return c.report != nil }) {
		return own, nil
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
	calls, drift := reconcile(calls, prices)
	if len(calls) == 0 {
		return nil, drift
	}
	return calls, drift
}

// warnDrift logs, once for each model, that Claude Code's own cost of the
// tokens differs from what the store's prices make of them.
func (s *Store) warnDrift(drift map[string]costDrift) {
	for _, model := range slices.Sorted(maps.Keys(drift)) {
		if _, warned := s.drifted.LoadOrStore(model, true); warned {
			continue
		}
		d := drift[model]
		s.logger.Warn("Claude Code's own cost of a model's tokens differs from AgentAPI's prices; costs use AgentAPI's (--prices changes them)",
			"agent", s.agent, "model", model, "claude_code_usd", d.reported, "agentapi_usd", d.priced)
	}
}

// costLogs lists the logs a reading of the costs reads: the sessions', and
// a function that lists the subagents of the session id whose log is at
// path.
func (s *Store) costLogs() ([]string, func(id, path string) []Subagent, error) {
	if s.src.costLogs != nil {
		return s.src.costLogs(s)
	}
	files, err := s.src.files(s)
	return files, func(string, string) []Subagent { return nil }, err
}

// codexCostLogs is costLogs for Codex, which keeps every thread in one
// tree: one walk finds the sessions and their subagents.
func (s *Store) codexCostLogs() ([]string, func(id, path string) []Subagent, error) {
	threads, err := s.codexThreads()
	if err != nil {
		return nil, nil, err
	}
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
	return codexSessionLogs(s, threads), func(id, _ string) []Subagent { return byParent[id] }, nil
}

// claudeCostLogs is costLogs for Claude Code, which keeps a session's
// subagents in a directory named after it next to its log.
func (s *Store) claudeCostLogs() ([]string, func(id, path string) []Subagent, error) {
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

// price returns call at the store's prices, or at what the agent said. A
// model without a price is logged once, the first time a call of it is.
func (s *Store) price(c agentlog.Call) ModelCall {
	call := priced(ModelCall{
		Time: c.Time, Model: s.intern(c.Model), Tokens: c.Tokens, Provider: s.intern(c.Provider),
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

// intern returns model, the one copy of the name the store keeps: a long
// session makes tens of thousands of calls, to a few models (and
// providers, which it keeps too).
func (s *Store) intern(model string) string {
	if model == "" {
		return ""
	}
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	if name, ok := s.models[model]; ok {
		return name
	}
	if s.models == nil {
		s.models = make(map[string]string)
	}
	s.models[model] = model
	return model
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

// unitemizedMin is the smallest cost of tokens Claude Code counted but
// didn't log that is added as unitemized, and the smallest difference
// between its cost and ours that is drift: below it, rounding.
const unitemizedMin = 0.01

// costDrift is Claude Code's own cost of a model's tokens and what the
// prices make of them, where they differ by more than rounding.
type costDrift struct{ reported, priced float64 }

// drifted reports whether Claude Code's cost reported differs from ours,
// priced: by more than unitemizedMin and 1%.
func drifted(reported, priced float64) bool {
	diff := math.Abs(reported - priced)
	return diff >= unitemizedMin && diff > 0.01*math.Abs(reported)
}

// reconcile turns Claude Code's cost-state lines among calls into calls
// for the tokens it counted but didn't log call by call: at each line,
// the tokens it counted for a model so far less those of the calls of that
// model up to then, priced at prices. The session's calls, its subagents'
// included, then add up to Claude Code's own token count. A difference in
// cost alone (the same tokens at other prices) adds no call: the models
// it is found for are returned. The lines themselves are dropped.
func reconcile(calls []ModelCall, prices *pricing.Table) ([]ModelCall, map[string]costDrift) {
	var reports []ModelCall
	calls = slices.DeleteFunc(calls, func(c ModelCall) bool {
		if c.report != nil {
			reports = append(reports, c)
			return true
		}
		return false
	})
	if len(reports) == 0 {
		return calls, nil
	}
	if !slices.IsSortedFunc(calls, byTime) {
		slices.SortStableFunc(calls, byTime)
	}
	counted := make(map[string]agentlog.ModelReport)
	var drift map[string]costDrift
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
			tokens := pricing.Tokens{
				Input:      max(0, reported.Tokens.Input-c.Tokens.Input),
				CacheRead:  max(0, reported.Tokens.CacheRead-c.Tokens.CacheRead),
				CacheWrite: max(0, reported.Tokens.CacheWrite-c.Tokens.CacheWrite-c.Tokens.CacheWrite1h),
				Output:     max(0, reported.Tokens.Output-c.Tokens.Output),
			}
			price, known := prices.Lookup(model)
			if !tokens.IsZero() && (!known || price.Cost(tokens) >= unitemizedMin) {
				call := ModelCall{Time: r.Time, Model: model, Tokens: tokens, Unitemized: true, cost: -1}
				if known {
					call.USD, call.Priced = price.Cost(tokens), true
				}
				added = append(added, call)
				c.USD += call.USD
				c.Tokens = c.Tokens.Add(tokens)
				counted[model] = c
			}
			// The same tokens, another cost: the prices differ. Where the
			// log has more tokens than Claude Code counted, the costs
			// differ for that.
			same := c.Tokens.Input == reported.Tokens.Input && c.Tokens.CacheRead == reported.Tokens.CacheRead &&
				c.Tokens.CacheWrite+c.Tokens.CacheWrite1h == reported.Tokens.CacheWrite && c.Tokens.Output == reported.Tokens.Output
			if known && same && drifted(reported.USD, c.USD) {
				if drift == nil {
					drift = make(map[string]costDrift)
				}
				drift[model] = costDrift{reported: reported.USD, priced: c.USD}
			}
		}
	}
	if len(added) == 0 {
		return calls, drift
	}
	if !slices.IsSortedFunc(added, byTime) {
		calls = append(calls, added...)
		slices.SortStableFunc(calls, byTime)
		return calls, drift
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
	return append(merged, added...), drift
}

// byTime orders calls by their time.
func byTime(a, b ModelCall) int { return a.Time.Compare(b.Time) }

// CallsDigest identifies calls, in order, so a client that has them can
// ask for the ones after them only: FNV-1a over each call's time, model,
// tokens, cost, subagent and provider, a line each
// ("%d|%s|%d|%d|%d|%d|%d|%x|%s|%t\n" of them, with "|%s" of the provider
// before the newline when it isn't the agent's own: the digests of calls
// before providers stay theirs). Empty for none. A chat asks for it on every turn, of sessions
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
		if call.Provider != "" {
			line = append(append(line, '|'), call.Provider...)
		}
		_, _ = h.Write(append(line, '\n'))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}
