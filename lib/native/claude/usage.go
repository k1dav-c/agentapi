package claude

import (
	"context"
	"encoding/json"
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

func unixTime(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0).UTC()
	return &t
}

// handleRateLimit records a rate_limit_event. Read loop only.
func (a *Adapter) handleRateLimit(raw json.RawMessage) {
	var info rateLimitInfo
	if decode(raw, &info) != nil {
		return
	}
	limits := a.usage.RateLimits
	if len(info.UnifiedWindows) > 0 {
		limits = nil
		for id, w := range info.UnifiedWindows {
			limit := native.RateLimit{ID: id, Label: windowLabel(id), ResetsAt: unixTime(w.ResetsAt)}
			if w.Utilization != nil {
				percent := *w.Utilization * 100
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
		if limits[i].ResetsAt == nil {
			limits[i].ResetsAt = unixTime(info.ResetsAt)
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

// readContextUsage asks how full the context window is, after a turn. The
// answer is handled in the read loop.
func (a *Adapter) readContextUsage() {
	ctx, cancel := context.WithTimeout(context.Background(), contextUsageTimeout)
	defer cancel()
	if _, err := a.request(ctx, "get_context_usage", nil, a.contextUsage); err != nil {
		a.cfg.Logger.Debug("Claude Code didn't report its context usage", "error", err)
	}
}

// contextUsage records a get_context_usage answer. Read loop only.
func (a *Adapter) contextUsage(response json.RawMessage) {
	var usage struct {
		TotalTokens int `json:"totalTokens"`
		MaxTokens   int `json:"maxTokens"`
	}
	if decode(response, &usage) != nil || usage.MaxTokens <= 0 {
		return
	}
	a.usage.Context = &native.ContextUsage{Used: usage.TotalTokens, Size: usage.MaxTokens}
	a.reportUsage()
}

// reportUsage tells the conversation about the usage. Read loop only.
func (a *Adapter) reportUsage() {
	if sink, ok := a.sink.(native.UsageSink); ok {
		usage := a.usage
		usage.RateLimits = slices.Clone(usage.RateLimits)
		sink.UsageChanged(usage)
	}
}
