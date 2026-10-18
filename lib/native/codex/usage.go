package codex

import (
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// rateLimitWindow is RateLimitWindow.
type rateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int64   `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

// rateLimitsUpdatedNotification is AccountRateLimitsUpdatedNotification.
type rateLimitsUpdatedNotification struct {
	RateLimits struct {
		LimitID   string           `json:"limitId"`
		LimitName string           `json:"limitName"`
		Primary   *rateLimitWindow `json:"primary"`
		Secondary *rateLimitWindow `json:"secondary"`
	} `json:"rateLimits"`
}

// rateLimitsUpdated records the account's rate limits: the snapshot of
// Codex's own limit (limitId "codex", or none); another limit (a model's
// quota) is named after it. Read loop only.
func (s *loopState) rateLimitsUpdated(n rateLimitsUpdatedNotification) {
	r := n.RateLimits
	prefix := ""
	if r.LimitID != "" && r.LimitID != "codex" {
		prefix = r.LimitID + "-"
		if r.LimitName == "" {
			r.LimitName = r.LimitID
		}
	}
	limits := slices.DeleteFunc(slices.Clone(s.usage.RateLimits), func(l native.RateLimit) bool {
		return l.ID == prefix+"primary" || l.ID == prefix+"secondary"
	})
	for id, w := range map[string]*rateLimitWindow{"primary": r.Primary, "secondary": r.Secondary} {
		if w == nil {
			continue
		}
		percent := w.UsedPercent
		limit := native.RateLimit{ID: prefix + id, Label: native.WindowLabel(w.WindowDurationMins), UsedPercent: &percent}
		if r.LimitName != "" && prefix != "" {
			limit.Label = r.LimitName + " " + limit.Label
		}
		limit.ResetsAt = native.ResetTime(w.ResetsAt)
		limits = append(limits, limit)
	}
	slices.SortFunc(limits, func(x, y native.RateLimit) int { return strings.Compare(x.ID, y.ID) })
	s.usage.RateLimits = limits
	s.reportUsage()
}

// contextUsed records how full the context window is after a model call.
// Read loop only.
func (s *loopState) contextUsed(used, size int64) {
	if size <= 0 {
		return
	}
	c := &native.ContextUsage{Used: int(used), Size: int(size)}
	if s.usage.Context != nil && *s.usage.Context == *c {
		return
	}
	s.usage.Context = c
	s.reportUsage()
}

// reportUsage tells the conversation about the usage. Read loop only.
func (s *loopState) reportUsage() {
	native.ReportUsage(s.sink(), s.usage)
}
