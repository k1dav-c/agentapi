package native

import (
	"strconv"
	"time"
)

// AgentUsage is how much of its limits the agent used, as far as it
// reports: how full the model's context window is, and the account's rate
// limits.
type AgentUsage struct {
	Context    *ContextUsage `json:"context,omitempty" doc:"How full the model's context window is, after the last turn."`
	RateLimits []RateLimit   `json:"rate_limits,omitempty" doc:"The account's usage limits (Claude Code's 5-hour and weekly limits, Codex's primary and secondary windows)."`
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
	// Status is Claude Code's: allowed, allowed_warning or rejected.
	Status string `json:"status,omitempty" doc:"allowed, allowed_warning (close to the limit) or rejected (reached). Claude Code only."`
}

// UsageSink is implemented by the Sink a Conversation hands to adapters.
// An adapter reports through it its whole usage whenever a part changes.
type UsageSink interface {
	UsageChanged(AgentUsage)
}

// WindowLabel names a rate limit window by its length in minutes, for an
// agent that gives no name of its own.
func WindowLabel(minutes int64) string {
	switch {
	case minutes <= 0:
		return "Usage limit"
	case minutes == 7*24*60:
		return "Weekly limit"
	case minutes%(24*60) == 0:
		return strconv.FormatInt(minutes/(24*60), 10) + "-day limit"
	case minutes%60 == 0:
		return strconv.FormatInt(minutes/60, 10) + "-hour limit"
	}
	return strconv.FormatInt(minutes, 10) + "-minute limit"
}
