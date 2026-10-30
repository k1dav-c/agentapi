package claude

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/native"
)

// contextUsageTimeout bounds the get_context_usage request after a turn.
const contextUsageTimeout = 10 * time.Second

// rateLimitInfo is the rate_limit_info of a rate_limit_event frame.
type rateLimitInfo struct {
	// Status, ResetsAt and RateLimitType are about the limit that applies
	// now.
	Status        string `json:"status"`
	ResetsAt      int64  `json:"resetsAt"`
	RateLimitType string `json:"rateLimitType"`
	// UnifiedWindows holds every window, with how much of it is used.
	UnifiedWindows map[string]struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    int64    `json:"resetsAt"`
	} `json:"unifiedWindows"`
}

// windowOrder sorts the windows Claude Code knows; others follow by name.
var windowOrder = []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet"}

// windowLabels name the windows; another one is named after its ID.
var windowLabels = map[string]string{
	"five_hour":        "5-hour limit",
	"seven_day":        "Weekly limit",
	"seven_day_opus":   "Weekly Opus limit",
	"seven_day_sonnet": "Weekly Sonnet limit",
}

func windowLabel(id string) string {
	if label, ok := windowLabels[id]; ok {
		return label
	}
	return strings.ReplaceAll(id, "_", " ")
}

// handleRateLimit records a rate_limit_event. Read loop only.
func (a *Adapter) handleRateLimit(raw json.RawMessage) {
	var info rateLimitInfo
	if a.drift.Decode("rate_limit_event", raw, &info) != nil {
		return
	}
	limits := a.usage.RateLimits
	if len(info.UnifiedWindows) > 0 {
		limits = nil
		for id, w := range info.UnifiedWindows {
			limit := native.RateLimit{ID: id, Label: windowLabel(id), ResetsAt: native.ResetTime(w.ResetsAt)}
			if w.Utilization != nil {
				// Two decimals: 0.29 is 29%, not 28.999999999999996.
				percent := math.Round(*w.Utilization*10000) / 100
				limit.UsedPercent = &percent
			}
			limits = append(limits, limit)
		}
	}
	if info.RateLimitType != "" {
		i := slices.IndexFunc(limits, func(l native.RateLimit) bool { return l.ID == info.RateLimitType })
		if i < 0 {
			limits = append(limits, native.RateLimit{ID: info.RateLimitType, Label: windowLabel(info.RateLimitType)})
			i = len(limits) - 1
		}
		limits[i].Status = info.Status
		if resetsAt := native.ResetTime(info.ResetsAt); resetsAt != nil {
			// Without unified windows (older CLIs), only the event says
			// when the next window starts.
			limits[i].ResetsAt = resetsAt
		}
	}
	slices.SortFunc(limits, func(x, y native.RateLimit) int {
		xi, yi := slices.Index(windowOrder, x.ID), slices.Index(windowOrder, y.ID)
		switch {
		case xi >= 0 && yi >= 0:
			return xi - yi
		case xi >= 0:
			return -1
		case yi >= 0:
			return 1
		}
		return strings.Compare(x.ID, y.ID)
	})
	a.usage.RateLimits = limits
	a.reportUsage()
}

// limitReached reports whether Claude Code said that a limit was reached
// and has yet to reset. Read loop only.
func (a *Adapter) limitReached() bool {
	now := a.cfg.Clock.Now()
	for _, l := range a.usage.RateLimits {
		if l.Status == "rejected" && (l.ResetsAt == nil || l.ResetsAt.After(now)) {
			return true
		}
	}
	return false
}

// resultUsage is the usage of a result frame: the turn's model calls.
type resultUsage struct {
	InputTokens          int `json:"input_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens"`
	CacheCreation        struct {
		OneHour  int `json:"ephemeral_1h_input_tokens"`
		FiveMins int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

// cacheTTL is how long the prompt cache a turn wrote is kept, or 0 when it
// wrote none. Claude Code writes 1-hour entries on a subscription, 5-minute
// ones with an API key, on Bedrock or Vertex, and past a subscription's
// limits.
func (u *resultUsage) cacheTTL() time.Duration {
	switch {
	case u.CacheCreation.OneHour > 0:
		return time.Hour
	case u.CacheCreation.FiveMins > 0:
		return 5 * time.Minute
	}
	return 0
}

// cacheUsed records when the prompt cache expires after a turn that called
// the model: its last use was just now. A turn that only read the cache
// keeps the time to live of the one before. Read loop only.
func (a *Adapter) cacheUsed(u *resultUsage) {
	if u == nil || u.InputTokens+u.CacheReadInputTokens+u.CacheCreation.OneHour+u.CacheCreation.FiveMins == 0 {
		return
	}
	ttl := u.cacheTTL()
	if ttl == 0 && a.usage.PromptCache != nil {
		ttl = time.Duration(a.usage.PromptCache.TTLSeconds) * time.Second
	}
	if ttl == 0 {
		return
	}
	a.usage.PromptCache = &native.PromptCache{TTLSeconds: int(ttl / time.Second), ExpiresAt: a.cfg.Clock.Now().Add(ttl).UTC()}
	a.reportUsage()
}

// readContextUsage asks how full the context window is, after a turn. The
// answer is handled in the read loop.
func (a *Adapter) readContextUsage() {
	ctx, cancel := context.WithTimeout(context.Background(), contextUsageTimeout)
	defer cancel()
	if _, err := a.request(ctx, "get_context_usage", nil, a.contextUsage); err != nil {
		a.log.Debug("Claude Code didn't report its context usage", "error", err)
	}
}

// contextUsage records a get_context_usage answer. Read loop only.
func (a *Adapter) contextUsage(response json.RawMessage) {
	var usage struct {
		TotalTokens int `json:"totalTokens"`
		MaxTokens   int `json:"maxTokens"`
	}
	if a.drift.Decode("get_context_usage response", response, &usage) != nil || usage.MaxTokens <= 0 {
		return
	}
	a.usage.Context = &native.ContextUsage{Used: usage.TotalTokens, Size: usage.MaxTokens}
	a.reportUsage()
}

// reportUsage tells the conversation about the usage. Read loop only.
func (a *Adapter) reportUsage() {
	native.ReportUsage(a.sink, a.usage)
}
