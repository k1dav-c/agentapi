package agentstate

import "time"

// AgentUsage is how much of its limits the agent used, as far as it
// reports: how full the model's context window is, and the account's rate
// limits.
type AgentUsage struct {
	Context    *ContextUsage `json:"context,omitempty" doc:"How full the model's context window is, after the last turn."`
	RateLimits []RateLimit   `json:"rate_limits,omitempty" nullable:"false" doc:"The account's usage limits (Claude Code's 5-hour and weekly limits, Codex's primary and secondary windows)."`
	// PromptCache is set by agents that say how long the model provider
	// keeps their prompt cache (Claude Code).
	PromptCache *PromptCache `json:"prompt_cache,omitempty" doc:"How long the model provider keeps the conversation's prompt cache, after the last turn. Claude Code only."`
}

// PromptCache is the model provider's cache of the conversation's prompt.
// Once it expired, the next message has the provider read the whole
// context again, at the full price and against the usage limits.
type PromptCache struct {
	TTLSeconds int       `json:"ttl_seconds" doc:"How long the cache is kept after its last use: 300 (5 minutes) or 3600 (an hour)."`
	ExpiresAt  time.Time `json:"expires_at" doc:"When the cache expires unless the agent uses it again."`
}

// ContextUsage is how full the model's context window is.
type ContextUsage struct {
	Used int `json:"used" doc:"Tokens in the context window."`
	Size int `json:"size" doc:"Tokens the context window holds."`
}

// RateLimit is one of the account's usage limits.
type RateLimit struct {
	ID    string `json:"id" doc:"Which limit, as the agent names it: five_hour, seven_day, ... (Claude Code); primary, secondary (Codex)."`
	Label string `json:"label" doc:"Name to show, e.g. 5-hour limit."`
	// UsedPercent is nil when the agent doesn't say how much is used.
	UsedPercent *float64   `json:"used_percent,omitempty" doc:"How much of the limit is used, from 0 to 100."`
	ResetsAt    *time.Time `json:"resets_at,omitempty" doc:"When the limit resets."`
	// Status is Claude Code's: RateLimitAllowed, RateLimitAllowedWarning
	// or RateLimitRejected.
	Status string `json:"status,omitempty" doc:"allowed, allowed_warning (close to the limit) or rejected (reached). Claude Code only."`
}

// RateLimit.Status values.
const (
	RateLimitAllowed = "allowed"
	// RateLimitAllowedWarning: close to the limit.
	RateLimitAllowedWarning = "allowed_warning"
	// RateLimitRejected: the limit is reached.
	RateLimitRejected = "rejected"
)
