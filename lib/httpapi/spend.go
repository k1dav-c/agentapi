package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"slices"
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
// own count has, as their cost. The providers split them by model
// provider (sessions.SessionCost.Providers).

// spendInterval is how often the spend is worked out again.
const spendInterval = time.Minute

// spendHours is how many hours the summary's spend lists, the hour under
// way included: one more than the day adds up, so a client that reads the
// summary less than hourly misses no hour's final figure.
const spendHours = 25

// spendOf adds up the hours of cost that end within the last day and week
// before now: the cost to the cent, and the tokens, in all and by
// provider. It lists the last spendHours hours with calls too.
func spendOf(cost sessions.SessionCost, now time.Time) instanceapi.SummarySpend {
	listed := now.UTC().Truncate(time.Hour).Add(-(spendHours - 1) * time.Hour)
	var spend instanceapi.SummarySpend
	w := windowsOf(cost.Hourly, now)
	spend.DayUSD, spend.WeekUSD = w.dayUSD, w.weekUSD
	spend.DayTokens, spend.WeekTokens = summaryTokens(w.dayTokens), summaryTokens(w.weekTokens)
	for _, h := range cost.Hourly {
		if !h.Hour.Before(listed) {
			// To a millionth of a dollar: enough for any sum of them.
			usd := math.Round(h.USD*1e6) / 1e6
			spend.Hours = append(spend.Hours, instanceapi.SummaryHour{Hour: h.Hour.UTC(), USD: usd, Tokens: *summaryTokens(h.Tokens)})
		}
	}
	for _, p := range cost.Providers {
		w := windowsOf(p.Hourly(), now)
		if !w.inWeek {
			continue
		}
		spend.Providers = append(spend.Providers, instanceapi.SummaryProviderSpend{
			Provider: p.Provider, DayUSD: w.dayUSD, WeekUSD: w.weekUSD,
			DayTokens: *summaryTokens(w.dayTokens), WeekTokens: *summaryTokens(w.weekTokens),
		})
	}
	slices.SortStableFunc(spend.Providers, func(a, b instanceapi.SummaryProviderSpend) int {
		switch {
		case a.WeekUSD > b.WeekUSD:
			return -1
		case a.WeekUSD < b.WeekUSD:
			return 1
		}
		if a.WeekTokens.Total != b.WeekTokens.Total {
			return b.WeekTokens.Total - a.WeekTokens.Total
		}
		return strings.Compare(a.Provider, b.Provider)
	})
	return spend
}

// spendWindows are the cost (to the cent) and tokens of the hours that end
// within the last day and week; inWeek says there are any of the week.
type spendWindows struct {
	dayUSD, weekUSD       float64
	dayTokens, weekTokens pricing.Tokens
	inWeek                bool
}

// windowsOf adds up the hours of hourly that end within the last day and
// week before now.
func windowsOf(hourly []sessions.HourCost, now time.Time) spendWindows {
	day, week := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	var w spendWindows
	for _, h := range hourly {
		end := h.Hour.Add(time.Hour)
		if end.After(week) {
			w.weekUSD += h.USD
			w.weekTokens = w.weekTokens.Add(h.Tokens)
			w.inWeek = true
		}
		if end.After(day) {
			w.dayUSD += h.USD
			w.dayTokens = w.dayTokens.Add(h.Tokens)
		}
	}
	w.dayUSD = math.Round(w.dayUSD*100) / 100
	w.weekUSD = math.Round(w.weekUSD*100) / 100
	return w
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
// last's time and hours while the amounts and tokens stay, so the summary
// changes with them only. An hour that left the list meanwhile stays until
// then: its figures are final.
func nextSpend(last, spend instanceapi.SummarySpend, scope string, now time.Time) instanceapi.SummarySpend {
	spend.Scope = scope
	if last.UpdatedAt.IsZero() || spend.DayUSD != last.DayUSD || spend.WeekUSD != last.WeekUSD || spend.Scope != last.Scope ||
		!spend.SameTokens(last) || !spend.SameProviders(last) {
		spend.UpdatedAt = now.UTC().Truncate(time.Second)
	} else {
		spend.UpdatedAt = last.UpdatedAt
		spend.Hours = last.Hours
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
		last = nextSpend(last, spendOf(costs.Total, now), scope, now)
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
