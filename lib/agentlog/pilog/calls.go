package pilog

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// Calls reads Pi's assistant messages, which carry their usage and what
// they cost at Pi's prices, and the usage entries of the calls Pi makes
// outside the conversation (to keep the prompt cache warm).
type Calls struct {
	calls []agentlog.Call
}

// NewCalls returns a Calls.
func NewCalls() *Calls {
	return &Calls{}
}

// Line reads a line of the log.
func (c *Calls) Line(line []byte) {
	if !bytes.Contains(line, []byte(`"usage"`)) {
		return
	}
	var entry struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Timestamp string `json:"timestamp"`
		Message   *struct {
			Role      string   `json:"role"`
			Model     string   `json:"model"`
			Timestamp int64    `json:"timestamp"`
			Usage     *piUsage `json:"usage"`
		} `json:"message"`
		// A usage entry's.
		Model string   `json:"model"`
		Usage *piUsage `json:"usage"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	var call agentlog.Call
	switch {
	case entry.Type == "message" && entry.Message != nil && entry.Message.Role == "assistant" && entry.Message.Usage != nil:
		call = agentlog.Call{Time: Timestamp(entry.Timestamp, entry.Message.Timestamp), Model: entry.Message.Model}
		entry.Usage = entry.Message.Usage
	case entry.Type == "usage" && entry.Usage != nil:
		call = agentlog.Call{Time: Timestamp(entry.Timestamp, 0), Model: entry.Model}
	default:
		return
	}
	u := entry.Usage
	call.Tokens = pricing.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Output: u.Output}
	call.Cost = -1
	if entry.ID != "" {
		call.Key = "pi:" + entry.ID
	}
	if u.Cost != nil && u.Cost.Total != nil {
		call.Cost = *u.Cost.Total
	}
	if !call.Tokens.IsZero() {
		c.calls = append(c.calls, call)
	}
}

// Calls returns the calls read so far.
func (c *Calls) Calls() []agentlog.Call {
	return slices.Clone(c.calls)
}

// Size is about how many bytes of memory the reader holds.
func (c *Calls) Size() int {
	return len(c.calls) * agentlog.CallSize
}
