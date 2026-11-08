package native

import (
	"slices"
	"strconv"
	"time"

	"github.com/coder/agentapi/lib/agentstate"
)

// UsageSink is implemented by the Sink a Conversation hands to adapters.
// An adapter reports through it its whole usage whenever a part changes.
type UsageSink interface {
	UsageChanged(agentstate.AgentUsage)
}

// ReportUsage reports usage to sink, if it is a UsageSink. The sink keeps
// what it gets, so the rate limits are copied.
func ReportUsage(sink Sink, usage agentstate.AgentUsage) {
	if s, ok := sink.(UsageSink); ok {
		usage.RateLimits = slices.Clone(usage.RateLimits)
		s.UsageChanged(usage)
	}
}

// ResetTime is the time a limit resets, from Unix seconds; nil for none.
func ResetTime(unix int64) *time.Time {
	if unix <= 0 {
		return nil
	}
	t := time.Unix(unix, 0).UTC()
	return &t
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
