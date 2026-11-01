package codexlog

import (
	"bytes"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/transcript"
)

// Calls reads Codex's token counts: the change of the thread's running
// total since the last count is a call's usage. The model is the turn's.
type Calls struct {
	calls []agentlog.Call
	model string
	last  *Totals
	drift *agentlog.Drift
}

// NewCalls returns a Calls that counts the records it can't read into
// drift.
func NewCalls(drift *agentlog.Drift) *Calls {
	return &Calls{drift: drift}
}

// Line reads a line of the log.
func (c *Calls) Line(line []byte) {
	switch {
	case bytes.Contains(line, []byte(`"turn_context"`)):
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if agentlog.Unmarshal(line, &entry) != nil || entry.Type != "turn_context" {
			return
		}
		if entry.Payload.Model == "" {
			c.drift.Call(agentlog.NoModelOrUsage)
			return
		}
		c.model = entry.Payload.Model
	case bytes.Contains(line, []byte(`"token_count"`)):
		var entry struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Payload   struct {
				Type string `json:"type"`
				Info *struct {
					Total Totals `json:"total_token_usage"`
					Last  Totals `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if agentlog.Unmarshal(line, &entry) != nil || entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
			// Before the first call, a count has only the rate limits.
			return
		}
		info := entry.Payload.Info
		if info.Total.none() || (c.last == nil && info.Last.none()) {
			// A running total is never empty, nor is the first call:
			// their fields were renamed.
			c.drift.Call(agentlog.NoTokens)
			if !info.Total.none() {
				total := info.Total
				c.last = &total
			}
			return
		}
		used := info.Last
		if c.last != nil {
			// The total can start from a thread this one continues;
			// within the log, its change is what was used.
			used = info.Total.minus(*c.last)
		}
		total := info.Total
		c.last = &total
		if used.none() {
			// The same count again, sent with the rate limits.
			return
		}
		if c.model == "" {
			c.drift.Call(agentlog.NoModelOrUsage)
		}
		at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		c.calls = append(c.calls, agentlog.Call{Time: at, Model: c.model, Tokens: used.Tokens(), Cost: -1})
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

// Totals is Codex's TokenUsage, a call's or a running total. Its input
// counts the input read from and written to the cache too.
type Totals struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	CacheWriteTokens  int `json:"cache_write_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

// none reports whether t counts no tokens.
func (t Totals) none() bool { return t.InputTokens <= 0 && t.OutputTokens <= 0 }

func (t Totals) minus(o Totals) Totals {
	return Totals{
		InputTokens:       t.InputTokens - o.InputTokens,
		CachedInputTokens: t.CachedInputTokens - o.CachedInputTokens,
		CacheWriteTokens:  t.CacheWriteTokens - o.CacheWriteTokens,
		OutputTokens:      t.OutputTokens - o.OutputTokens,
	}
}

// Usage is t as transcript.Usage counts tokens: the input apart from what
// was read from or written to the cache.
func (t Totals) Usage() *transcript.Usage {
	tokens := t.Tokens()
	return &transcript.Usage{
		InputTokens:              tokens.Input,
		OutputTokens:             tokens.Output,
		CacheReadInputTokens:     tokens.CacheRead,
		CacheCreationInputTokens: tokens.CacheWrite,
	}
}

// Tokens are the tokens of t as pricing counts them: the input without
// what was read from or written to the cache.
func (t Totals) Tokens() pricing.Tokens {
	return pricing.Tokens{
		Input:      max(0, t.InputTokens-t.CachedInputTokens-t.CacheWriteTokens),
		CacheRead:  max(0, t.CachedInputTokens),
		CacheWrite: max(0, t.CacheWriteTokens),
		Output:     max(0, t.OutputTokens),
	}
}
