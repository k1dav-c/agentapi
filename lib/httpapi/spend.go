package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/sessions"
)

// The summary's spend: what the agent's sessions in this directory cost
// over the last day and week, at the model providers' list prices, so a
// client that watches many AgentAPIs (a hub) sees each one's spend
// without reading its logs. Worked out from the logs once a minute; the
// logs are read incrementally, so a pass costs little. Its scope says
// which sessions it adds up: two AgentAPIs of one agent in one directory
// add up the same, which a client that adds up many counts once. The
// tokens are those of the same calls: each call once, as its cost (a
// fork's copy of an earlier session's call counts in that one), and the
// calls Claude Code counted but didn't log (unitemized), whose tokens its
// own count has, as their cost.

// spendInterval is how often the spend is worked out again.
const spendInterval = time.Minute

// spendOf adds up the hours of hourly that end within the last day and
// week before now: the cost to the cent, and the tokens.
func spendOf(hourly []sessions.HourCost, now time.Time) instanceapi.SummarySpend {
	day, week := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	var spend instanceapi.SummarySpend
	var dayTokens, weekTokens pricing.Tokens
	for _, h := range hourly {
		end := h.Hour.Add(time.Hour)
		if end.After(week) {
			spend.WeekUSD += h.USD
			weekTokens = weekTokens.Add(h.Tokens)
		}
		if end.After(day) {
			spend.DayUSD += h.USD
			dayTokens = dayTokens.Add(h.Tokens)
		}
	}
	spend.DayUSD = math.Round(spend.DayUSD*100) / 100
	spend.WeekUSD = math.Round(spend.WeekUSD*100) / 100
	spend.DayTokens, spend.WeekTokens = summaryTokens(dayTokens), summaryTokens(weekTokens)
	return spend
}

// summaryTokens are tokens as a summary has them: both of Anthropic's
// cache writes in one, and their total.
func summaryTokens(t pricing.Tokens) *instanceapi.SummaryTokens {
	s := instanceapi.SummaryTokens{Input: t.Input, Output: t.Output, CacheWrite: t.CacheWrite + t.CacheWrite1h, CacheRead: t.CacheRead}
	s.Total = s.Input + s.Output + s.CacheWrite + s.CacheRead
	return &s
}

// spendScope identifies the sessions store adds up, as the logs of this
// machine hold them: a hash of the machine's host name and ID (where it
// has one) and the store's scope (the agent, the directory, where the
// agent keeps its logs).
func spendScope(store *sessions.Store) string {
	hostname, _ := os.Hostname()
	machineID, _ := os.ReadFile("/etc/machine-id")
	h := sha256.New()
	for _, part := range []string{hostname, strings.TrimSpace(string(machineID)), store.Scope()} {
		_, _ = io.WriteString(h, part)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// nextSpend is spend of scope, worked out at now after last: it keeps
// last's time while the amounts and tokens stay, so the summary changes with them
// only.
func nextSpend(last, spend instanceapi.SummarySpend, scope string, now time.Time) instanceapi.SummarySpend {
	spend.Scope = scope
	if last.UpdatedAt.IsZero() || spend.DayUSD != last.DayUSD || spend.WeekUSD != last.WeekUSD || spend.Scope != last.Scope || !spend.SameTokens(last) {
		spend.UpdatedAt = now.UTC().Truncate(time.Second)
	} else {
		spend.UpdatedAt = last.UpdatedAt
	}
	return spend
}

// startSpend works the summary's spend out now and once a minute until the
// server stops, for an agent whose session logs AgentAPI reads (native
// Claude Code, Codex and Pi).
func (s *Server) startSpend() {
	if s.sessions == nil {
		return
	}
	scope := spendScope(s.sessions)
	var last instanceapi.SummarySpend
	update := func() {
		costs, err := s.sessions.Costs()
		if err != nil {
			s.logger.Debug("Failed to work out what the sessions spent", "error", err)
			return
		}
		now := time.Now()
		last = nextSpend(last, spendOf(costs.Total.Hourly, now), scope, now)
		s.emitter.SetSpend(last)
	}
	go func() {
		update()
		ticker := time.NewTicker(spendInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.shutdownCtx.Done():
				return
			case <-ticker.C:
				update()
			}
		}
	}()
}
