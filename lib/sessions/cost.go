package sessions

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
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
	report map[string]claudeModelReport
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

// add counts call in c.
func (c *SessionCost) add(call ModelCall) {
	c.USD += call.USD
	c.Tokens = c.Tokens.Add(call.Tokens)
	c.Calls++
	i := slices.IndexFunc(c.Models, func(m ModelCost) bool { return m.Model == call.Model })
	if i < 0 {
		c.Models = append(c.Models, ModelCost{Model: call.Model, Priced: call.Priced})
		i = len(c.Models) - 1
	}
	m := &c.Models[i]
	m.USD += call.USD
	m.Tokens = m.Tokens.Add(call.Tokens)
	m.Calls++
	hour := call.Time.UTC().Truncate(time.Hour)
	j := slices.IndexFunc(c.Hourly, func(h HourCost) bool { return h.Hour.Equal(hour) })
	if j < 0 {
		c.Hourly = append(c.Hourly, HourCost{Hour: hour})
		j = len(c.Hourly) - 1
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
	var c SessionCost
	for _, call := range calls {
		c.add(call)
	}
	return c.sorted()
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
func (s *Store) Costs() (DirectoryCost, error) {
	files, err := s.h.files(s.cwd)
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
	subagents := s.subagentIndex(files)
	for _, file := range files {
		summary, ok := s.summarize(file)
		if !ok {
			continue
		}
		calls := slices.Clone(s.cachedCalls(file.path, file.modTime, nil))
		for _, sub := range subagents(summary.ID) {
			for _, call := range s.cachedCalls(sub.path, sub.UpdatedAt, sub.lines) {
				call.SubagentID = sub.ID
				calls = append(calls, call)
			}
		}
		for i := range calls {
			calls[i] = priced(calls[i], s.prices)
		}
		calls = reconcile(calls)
		born, _ := agentlog.BirthTime(file.path)
		sessions = append(sessions, session{id: summary.ID, born: born, started: summary.CreatedAt, changed: file.modTime, calls: calls})
	}
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

// claudeModelReport is what a Claude Code cost-state line counts for one
// model.
type claudeModelReport struct {
	USD    float64
	Tokens pricing.Tokens
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
	counted := make(map[string]claudeModelReport)
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
// found in one pass over files.
func (s *Store) subagentIndex(files []historyFile) func(id string) []Subagent {
	if s.agent != "codex" {
		return func(id string) []Subagent {
			subs, _ := s.Subagents(id)
			return subs
		}
	}
	byParent := make(map[string][]Subagent)
	for _, f := range files {
		var meta *codexlog.SessionMeta
		agentlog.ScanFile(f.path, func(line []byte) bool {
			var m codexlog.SessionMeta
			if json.Unmarshal(line, &m) == nil {
				meta = &m
			}
			return false
		})
		if meta == nil || !meta.IsSubAgent() {
			continue
		}
		parent := meta.Payload.ParentThreadID
		if parent == "" {
			parent = meta.Payload.SessionID
		}
		byParent[parent] = append(byParent[parent], Subagent{
			ID: meta.Payload.ID, UpdatedAt: f.modTime, path: f.path, kind: "codex", from: meta.Payload.HistoryStart,
		})
	}
	return func(id string) []Subagent { return byParent[id] }
}

// cachedCallList is the model calls of a log as it was when last read.
type cachedCallList struct {
	modTime time.Time
	calls   []ModelCall
}

// cachedCalls returns the model calls of the log at path, from the cache
// while it is unchanged. lines reads a subagent's own lines; nil reads the
// whole log.
func (s *Store) cachedCalls(path string, modTime time.Time, lines func(func([]byte) bool)) []ModelCall {
	s.mu.Lock()
	cached, ok := s.calls[path]
	s.mu.Unlock()
	if ok && cached.modTime.Equal(modTime) {
		return cached.calls
	}
	if lines == nil {
		lines = func(fn func([]byte) bool) { agentlog.ScanFile(path, fn) }
	}
	calls := readCalls(s.agent, lines)
	s.mu.Lock()
	s.calls[path] = cachedCallList{modTime: modTime, calls: calls}
	s.mu.Unlock()
	return calls
}

// readCalls reads the model calls of a log.
func readCalls(agentType string, lines func(func([]byte) bool)) []ModelCall {
	switch agentType {
	case "claude":
		return claudeCalls(lines)
	case "codex":
		return codexCalls(lines)
	case "pi":
		return piCalls(lines)
	}
	return nil
}

// claudeCalls reads Claude Code's assistant entries: a call can be logged
// on several lines (one per content block), each with the call's usage.
func claudeCalls(lines func(func([]byte) bool)) []ModelCall {
	var calls []ModelCall
	index := make(map[string]int)
	// latest is the time of the latest line that has one: a cost-state line
	// has none of its own.
	var latest time.Time
	lines(func(line []byte) bool {
		if bytes.Contains(line, []byte(`"cost-state"`)) {
			if r, ok := claudeReport(line); ok {
				calls = append(calls, ModelCall{Time: latest, report: r, cost: -1})
			}
			return true
		}
		if at, ok := lineTime(line); ok {
			latest = at
		}
		if !bytes.Contains(line, []byte(`"assistant"`)) || !bytes.Contains(line, []byte(`"usage"`)) {
			return true
		}
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			RequestID string `json:"requestId"`
			Message   *struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					Input         int `json:"input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
					CacheCreation int `json:"cache_creation_input_tokens"`
					Output        int `json:"output_tokens"`
					Split         *struct {
						FiveMinutes int `json:"ephemeral_5m_input_tokens"`
						OneHour     int `json:"ephemeral_1h_input_tokens"`
					} `json:"cache_creation"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" || entry.Message == nil || entry.Message.Usage == nil {
			return true
		}
		m := entry.Message
		if m.Model == "" || m.Model == "<synthetic>" {
			return true
		}
		u := m.Usage
		tokens := pricing.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation, Output: u.Output}
		if u.Split != nil && u.Split.FiveMinutes+u.Split.OneHour == u.CacheCreation {
			tokens.CacheWrite, tokens.CacheWrite1h = u.Split.FiveMinutes, u.Split.OneHour
		}
		key := m.ID
		if key == "" {
			key = entry.RequestID
		}
		if i, ok := index[key]; ok && key != "" {
			// A later line of the same call: its usage is the final one.
			if tokens.Output >= calls[i].Tokens.Output {
				calls[i].Tokens = tokens
			}
			return true
		}
		at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if key != "" {
			index[key] = len(calls)
		}
		calls = append(calls, ModelCall{Time: at, Model: m.Model, Tokens: tokens, key: "claude:" + key, cost: -1})
		return true
	})
	return slices.DeleteFunc(calls, func(c ModelCall) bool { return c.report == nil && c.Tokens.IsZero() })
}

// lineTime is the timestamp of a log line, if it has one.
func lineTime(line []byte) (time.Time, bool) {
	i := bytes.Index(line, []byte(`"timestamp":"`))
	if i < 0 {
		return time.Time{}, false
	}
	rest := line[i+len(`"timestamp":"`):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(rest[:j]))
	return at, err == nil
}

// claudeReport reads a cost-state line: what Claude Code counted for each
// model of the session so far, its subagents' included.
func claudeReport(line []byte) (map[string]claudeModelReport, bool) {
	var entry struct {
		Type       string `json:"type"`
		ModelUsage map[string]struct {
			Input         int     `json:"inputTokens"`
			Output        int     `json:"outputTokens"`
			CacheRead     int     `json:"cacheReadInputTokens"`
			CacheCreation int     `json:"cacheCreationInputTokens"`
			CostUSD       float64 `json:"costUSD"`
		} `json:"modelUsage"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "cost-state" || len(entry.ModelUsage) == 0 {
		return nil, false
	}
	report := make(map[string]claudeModelReport, len(entry.ModelUsage))
	for model, u := range entry.ModelUsage {
		report[model] = claudeModelReport{USD: u.CostUSD, Tokens: pricing.Tokens{
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation,
		}}
	}
	return report, true
}

// codexCalls reads Codex's token counts: the change of the thread's running
// total since the last count is a call's usage. The model is the turn's.
func codexCalls(lines func(func([]byte) bool)) []ModelCall {
	var (
		calls []ModelCall
		model string
		last  *codexTotals
	)
	lines(func(line []byte) bool {
		switch {
		case bytes.Contains(line, []byte(`"turn_context"`)):
			var entry struct {
				Type    string `json:"type"`
				Payload struct {
					Model string `json:"model"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &entry) == nil && entry.Type == "turn_context" && entry.Payload.Model != "" {
				model = entry.Payload.Model
			}
		case bytes.Contains(line, []byte(`"token_count"`)):
			var entry struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type string `json:"type"`
					Info *struct {
						Total codexTotals `json:"total_token_usage"`
						Last  codexTotals `json:"last_token_usage"`
					} `json:"info"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &entry) != nil || entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
				return true
			}
			info := entry.Payload.Info
			used := info.Last
			if last != nil {
				// The total can start from a thread this one continues;
				// within the log, its change is what was used.
				used = info.Total.minus(*last)
			}
			total := info.Total
			last = &total
			if used.InputTokens <= 0 && used.OutputTokens <= 0 {
				return true
			}
			at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
			calls = append(calls, ModelCall{Time: at, Model: model, Tokens: used.usage(), cost: -1})
		}
		return true
	})
	return calls
}

// codexTotals is Codex's TokenUsage. Its input counts the cached input
// too.
type codexTotals struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	CacheWriteTokens  int `json:"cache_write_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

func (t codexTotals) minus(o codexTotals) codexTotals {
	return codexTotals{
		InputTokens:       t.InputTokens - o.InputTokens,
		CachedInputTokens: t.CachedInputTokens - o.CachedInputTokens,
		CacheWriteTokens:  t.CacheWriteTokens - o.CacheWriteTokens,
		OutputTokens:      t.OutputTokens - o.OutputTokens,
	}
}

func (t codexTotals) usage() pricing.Tokens {
	return pricing.Tokens{
		Input:      max(0, t.InputTokens-t.CachedInputTokens-t.CacheWriteTokens),
		CacheRead:  max(0, t.CachedInputTokens),
		CacheWrite: max(0, t.CacheWriteTokens),
		Output:     max(0, t.OutputTokens),
	}
}

// piCalls reads Pi's assistant messages, which carry their usage and what
// they cost at Pi's prices.
func piCalls(lines func(func([]byte) bool)) []ModelCall {
	var calls []ModelCall
	lines(func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"assistant"`)) || !bytes.Contains(line, []byte(`"usage"`)) {
			return true
		}
		var entry struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Timestamp string `json:"timestamp"`
			Message   *struct {
				Role      string `json:"role"`
				Model     string `json:"model"`
				Timestamp int64  `json:"timestamp"`
				Usage     *struct {
					Input      int `json:"input"`
					Output     int `json:"output"`
					CacheRead  int `json:"cacheRead"`
					CacheWrite int `json:"cacheWrite"`
					Cost       *struct {
						Total *float64 `json:"total"`
					} `json:"cost"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "message" || entry.Message == nil || entry.Message.Role != "assistant" || entry.Message.Usage == nil {
			return true
		}
		u := entry.Message.Usage
		call := ModelCall{
			Time:   pilog.Timestamp(entry.Timestamp, entry.Message.Timestamp),
			Model:  entry.Message.Model,
			Tokens: pricing.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Output: u.Output},
			cost:   -1,
		}
		if entry.ID != "" {
			call.key = "pi:" + entry.ID
		}
		if u.Cost != nil && u.Cost.Total != nil {
			call.cost = *u.Cost.Total
		}
		if !call.Tokens.IsZero() {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}
