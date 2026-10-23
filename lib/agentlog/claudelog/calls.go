package claudelog

import (
	"bytes"
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
}

// NewCalls returns a Calls.
func NewCalls() *Calls {
	return &Calls{index: make(map[string]int)}
}

// Line reads a line of the log.
func (c *Calls) Line(line []byte) {
	if bytes.Contains(line, []byte(`"cost-state"`)) {
		if r, ok := report(line); ok {
			c.calls = append(c.calls, agentlog.Call{Time: c.latest, Report: r, Cost: -1})
		}
		return
	}
	if at, ok := agentlog.LineTime(line); ok {
		c.latest = at
	}
	if !bytes.Contains(line, []byte(`"assistant"`)) || !bytes.Contains(line, []byte(`"usage"`)) {
		return
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
		return
	}
	m := entry.Message
	if m.Model == "" || m.Model == "<synthetic>" {
		return
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
	if i, ok := c.index[key]; ok && key != "" {
		// A later line of the same call: its usage is the final one.
		if tokens.Output >= c.calls[i].Tokens.Output {
			c.calls[i].Tokens = tokens
		}
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if key != "" {
		c.index[key] = len(c.calls)
	}
	c.calls = append(c.calls, agentlog.Call{Time: at, Model: m.Model, Tokens: tokens, Key: "claude:" + key, Cost: -1})
}

// Calls returns the calls read so far.
func (c *Calls) Calls() []agentlog.Call {
	return slices.DeleteFunc(slices.Clone(c.calls), func(call agentlog.Call) bool { return call.Report == nil && call.Tokens.IsZero() })
}

// Size is about how many bytes of memory the reader holds.
func (c *Calls) Size() int {
	return len(c.calls)*agentlog.CallSize + len(c.index)*64
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
