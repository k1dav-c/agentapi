package codexlog

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
)

// Calls reads Codex's token counts: the change of the thread's running
// total since the last count is a call's usage. The model is the turn's.
type Calls struct {
	calls []agentlog.Call
	model string
	last  *Totals
}

// NewCalls returns a Calls.
func NewCalls() *Calls {
	return &Calls{}
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
		if json.Unmarshal(line, &entry) == nil && entry.Type == "turn_context" && entry.Payload.Model != "" {
			c.model = entry.Payload.Model
		}
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
		if json.Unmarshal(line, &entry) != nil || entry.Payload.Type != "token_count" || entry.Payload.Info == nil {
			return
		}
		info := entry.Payload.Info
		used := info.Last
		if c.last != nil {
			// The total can start from a thread this one continues;
			// within the log, its change is what was used.
			used = info.Total.minus(*c.last)
		}
		total := info.Total
		c.last = &total
		if used.InputTokens <= 0 && used.OutputTokens <= 0 {
			return
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

// Totals is Codex's TokenUsage. Its input counts the cached input too.
type Totals struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	CacheWriteTokens  int `json:"cache_write_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
}

func (t Totals) minus(o Totals) Totals {
	return Totals{
		InputTokens:       t.InputTokens - o.InputTokens,
		CachedInputTokens: t.CachedInputTokens - o.CachedInputTokens,
		CacheWriteTokens:  t.CacheWriteTokens - o.CacheWriteTokens,
		OutputTokens:      t.OutputTokens - o.OutputTokens,
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
