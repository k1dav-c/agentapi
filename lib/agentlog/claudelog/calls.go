package claudelog

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// Calls reads the model calls of Claude Code's assistant entries: a call
// can be logged on several lines (one per content block), each with the
// call's usage. It reads Claude Code's cost-state records too, what it
// counted itself, as calls with a Report.
type Calls struct {
	calls []agentlog.Call
	// index finds a call by its message ID, or its request ID.
	index map[string]int
	// latest is the time of the latest line that has one: a cost-state
	// record has none of its own.
	latest time.Time
	drift  *agentlog.Drift
	// from is the first call Changed returns: the first read or changed
	// since it last returned.
	from int
}

// NewCalls returns a Calls that counts the entries it can't read into
// drift.
func NewCalls(drift *agentlog.Drift) *Calls {
	return &Calls{index: make(map[string]int), drift: drift}
}

// synthetic is the model of the messages Claude Code writes itself (an
// API error, a turn the user interrupted), with no call behind them and no
// usage.
const synthetic = "<synthetic>"

// Line reads a line of the log. Most of a line is the message's content,
// which a call's usage doesn't need: the line's fields are found without
// decoding it, and only the usage is decoded.
func (c *Calls) Line(line []byte) {
	var typ, timestamp, requestID string
	var message []byte
	if !agentlog.Members(line, func(key, value []byte) bool {
		switch string(key) {
		case "type":
			typ, _ = agentlog.Unquote(value)
		case "timestamp":
			timestamp, _ = agentlog.Unquote(value)
		case "requestId":
			requestID, _ = agentlog.Unquote(value)
		case "message":
			message = value
		}
		return true
	}) {
		return
	}
	if typ == "cost-state" {
		if r, ok := report(line); ok {
			c.calls = append(c.calls, agentlog.Call{Time: c.latest, Report: r, Cost: -1})
		}
		return
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err == nil {
		c.latest = at
	}
	if typ != "assistant" {
		return
	}
	var id, model string
	var usage []byte
	agentlog.Members(message, func(key, value []byte) bool {
		switch string(key) {
		case "id":
			id, _ = agentlog.Unquote(value)
		case "model":
			model, _ = agentlog.Unquote(value)
		case "usage":
			usage = value
		}
		return true
	})
	if model == synthetic {
		return
	}
	// Every call has its model and usage, and uses tokens: an entry
	// without them is one whose fields were renamed.
	if usage == nil || model == "" {
		c.drift.Call(agentlog.NoModelOrUsage)
		return
	}
	tokens := readUsage(usage)
	if tokens.IsZero() {
		c.drift.Call(agentlog.NoTokens)
		return
	}
	key := id
	if key == "" {
		key = requestID
	}
	if i, ok := c.index[key]; ok && key != "" {
		// A later line of the same call: its usage is the final one.
		if tokens.Output >= c.calls[i].Tokens.Output && tokens != c.calls[i].Tokens {
			c.calls[i].Tokens = tokens
			c.from = min(c.from, i)
		}
		return
	}
	call := agentlog.Call{Time: at, Model: model, Tokens: tokens, Cost: -1}
	if key != "" {
		// Without an ID nothing tells the call apart from another: it
		// counts, keyless, in the log it is in.
		c.index[key] = len(c.calls)
		call.Key = "claude:" + key
	}
	c.calls = append(c.calls, call)
}

// Changed returns the calls read or changed since it last returned. The
// calls stay: a later line can change any of them.
func (c *Calls) Changed() (int, []agentlog.Call) {
	from := c.from
	c.from = len(c.calls)
	return from, slices.Clone(c.calls[from:])
}

// Size is about how many bytes of memory the reader holds.
func (c *Calls) Size() int {
	return len(c.calls)*agentlog.CallSize + len(c.index)*64
}

// readUsage reads the tokens of a call's usage, which has a dozen other
// fields, without decoding them. A count that isn't an integer reads as 0.
func readUsage(usage []byte) pricing.Tokens {
	var t pricing.Tokens
	var fiveMinutes, oneHour int
	split := false
	agentlog.Members(usage, func(key, value []byte) bool {
		switch string(key) {
		case "input_tokens":
			t.Input = integer(value)
		case "cache_read_input_tokens":
			t.CacheRead = integer(value)
		case "cache_creation_input_tokens":
			t.CacheWrite = integer(value)
		case "output_tokens":
			t.Output = integer(value)
		case "cache_creation":
			// Split by how long the cache keeps them.
			split = agentlog.Members(value, func(key, value []byte) bool {
				switch string(key) {
				case "ephemeral_5m_input_tokens":
					fiveMinutes = integer(value)
				case "ephemeral_1h_input_tokens":
					oneHour = integer(value)
				}
				return true
			})
		}
		return true
	})
	if split && fiveMinutes+oneHour == t.CacheWrite {
		t.CacheWrite, t.CacheWrite1h = fiveMinutes, oneHour
	}
	return t
}

// integer reads a JSON integer, or returns 0 for another value.
func integer(value []byte) int {
	digits, sign := value, 1
	if len(digits) > 0 && digits[0] == '-' {
		digits, sign = digits[1:], -1
	}
	if len(digits) == 0 || len(digits) > 18 {
		return 0
	}
	n := 0
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return sign * n
}

// report reads a cost-state line: what Claude Code counted for each model
// of the session so far, its subagents' included.
func report(line []byte) (map[string]agentlog.ModelReport, bool) {
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
	r := make(map[string]agentlog.ModelReport, len(entry.ModelUsage))
	for model, u := range entry.ModelUsage {
		r[model] = agentlog.ModelReport{USD: u.CostUSD, Tokens: pricing.Tokens{
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation,
		}}
	}
	return r, true
}
