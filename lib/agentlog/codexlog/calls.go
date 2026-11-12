package codexlog

import (
	"bytes"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/transcript"
)

// ProviderPrefix starts the name of the model_providers entry AgentAPI
// defines for one of its model providers: "agentapi-" and the provider's
// ID.
const ProviderPrefix = "agentapi-"

// ProviderID is the AgentAPI model provider a Codex model provider name
// stands for: the ID after ProviderPrefix, and "" for any other (the
// agent's own: "openai", or one of the user's config).
func ProviderID(name string) string {
	id, ok := strings.CutPrefix(name, ProviderPrefix)
	if !ok {
		return ""
	}
	return id
}

// Calls reads Codex's token counts: the change of the thread's running
// total since the last count is a call's usage. The model is the turn's,
// the provider the thread's: its session_meta's, then that of the thread
// settings applied since.
type Calls struct {
	// calls are the calls read since Changed last returned, taken how
	// many it returned.
	calls    []agentlog.Call
	taken    int
	model    string
	provider string
	last     *Totals
	drift    *agentlog.Drift
}

// NewCalls returns a Calls that counts the records it can't read into
// drift.
func NewCalls(drift *agentlog.Drift) *Calls {
	return &Calls{drift: drift}
}

// Line reads a line of the log.
func (c *Calls) Line(line []byte) {
	c.providerLine(line)
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
		if c.last != nil && !info.Total.below(*c.last) {
			// The total can start from a thread this one continues;
			// within the log, its change is what was used. A total that
			// went down started over: the call is the last one.
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
		c.calls = append(c.calls, agentlog.Call{Time: at, Model: c.model, Tokens: used.Tokens(), Cost: -1, Provider: c.provider})
	}
}

// providerLine follows the thread's model provider: a session_meta line
// names the one it started (or resumed) on, a thread_settings_applied
// event the one a change of its settings put it on.
func (c *Calls) providerLine(line []byte) {
	switch {
	case bytes.Contains(line, []byte(`"session_meta"`)) && bytes.Contains(line, []byte(`"model_provider"`)):
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ModelProvider *string `json:"model_provider"`
			} `json:"payload"`
		}
		if agentlog.Unmarshal(line, &entry) == nil && entry.Type == "session_meta" && entry.Payload.ModelProvider != nil {
			c.provider = ProviderID(*entry.Payload.ModelProvider)
		}
	case bytes.Contains(line, []byte(`"thread_settings_applied"`)) && bytes.Contains(line, []byte(`"model_provider_id"`)):
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type     string `json:"type"`
				Settings struct {
					ModelProviderID *string `json:"model_provider_id"`
				} `json:"thread_settings"`
			} `json:"payload"`
		}
		if agentlog.Unmarshal(line, &entry) == nil && entry.Type == "event_msg" && entry.Payload.Type == "thread_settings_applied" &&
			entry.Payload.Settings.ModelProviderID != nil {
			c.provider = ProviderID(*entry.Payload.Settings.ModelProviderID)
		}
	}
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

// below reports whether a count of t is less than o's: a running total
// that started over.
func (t Totals) below(o Totals) bool {
	return t.InputTokens < o.InputTokens || t.CachedInputTokens < o.CachedInputTokens ||
		t.CacheWriteTokens < o.CacheWriteTokens || t.OutputTokens < o.OutputTokens
}

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
