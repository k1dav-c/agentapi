package pilog

import (
	"bytes"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// Calls reads Pi's assistant messages, which carry their usage and what
// they cost at Pi's prices, and the usage entries of the calls Pi makes
// outside the conversation (to keep the prompt cache warm).
type Calls struct {
	// calls are the calls read since Changed last returned, taken how
	// many it returned.
	calls []agentlog.Call
	taken int
	drift *agentlog.Drift
}

// NewCalls returns a Calls that counts the entries it can't read into
// drift.
func NewCalls(drift *agentlog.Drift) *Calls {
	return &Calls{drift: drift}
}

// Line reads a line of the log.
func (c *Calls) Line(line []byte) {
	if !bytes.Contains(line, []byte(`"assistant"`)) && !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	var entry struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Message   *struct {
			Role       string   `json:"role"`
			Model      string   `json:"model"`
			StopReason string   `json:"stopReason"`
			Timestamp  int64    `json:"timestamp"`
			Usage      *piUsage `json:"usage"`
		} `json:"message"`
		// A usage entry's.
		Model string   `json:"model"`
		Usage *piUsage `json:"usage"`
	}
	if agentlog.Unmarshal(line, &entry) != nil {
		return
	}
	var call agentlog.Call
	// failed: a call that failed or was aborted may have used nothing.
	failed := false
	switch {
	case entry.Type == "message" && entry.Message != nil && entry.Message.Role == "assistant":
		m := entry.Message
		call = agentlog.Call{Time: Timestamp(entry.Timestamp, m.Timestamp), Model: m.Model}
		entry.Usage = m.Usage
		failed = m.StopReason == "error" || m.StopReason == "aborted"
	case entry.Type == "usage":
		call = agentlog.Call{Time: Timestamp(entry.Timestamp, 0), Model: entry.Model}
	default:
		return
	}
	// Every call names its model and has its usage: an entry without
	// them is one whose fields were renamed.
	if call.Model == "" || entry.Usage == nil {
		c.drift.Call(agentlog.NoModelOrUsage)
		return
	}
	u := entry.Usage
	call.Tokens = pricing.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Output: u.Output}
	if call.Tokens.IsZero() {
		if !failed {
			c.drift.Call(agentlog.NoTokens)
		}
		return
	}
	call.Cost = -1
	if entry.ID != "" {
		call.Key = "pi:" + entry.ID
	}
	if u.Cost != nil && u.Cost.Total != nil {
		call.Cost = *u.Cost.Total
	}
	c.calls = append(c.calls, call)
}

// Changed returns the calls read since it last returned, and forgets
// them: a call is complete on its line.
func (c *Calls) Changed() (int, []agentlog.Call) {
	from, calls := c.taken, c.calls
	c.taken += len(calls)
	c.calls = nil
	return from, calls
}

// Size is about how many bytes of memory the reader holds.
func (c *Calls) Size() int {
	return len(c.calls) * agentlog.CallSize
}
