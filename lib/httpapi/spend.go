package httpapi

import (
	"math"
	"time"

	"github.com/coder/agentapi/lib/sessions"
)

// The summary's spend: what the agent's sessions in this directory cost
// over the last day and week, at the model providers' list prices, so a
// client that watches many AgentAPIs (a hub) sees each one's spend
// without reading its logs. Worked out from the logs once a minute; the
// logs are read incrementally, so a pass costs little.

// spendInterval is how often the spend is worked out again.
const spendInterval = time.Minute

// spendOf adds up the hours of hourly that end within the last day and
// week before now, to the cent.
func spendOf(hourly []sessions.HourCost, now time.Time) SummarySpend {
	day, week := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)
	var spend SummarySpend
	for _, h := range hourly {
		end := h.Hour.Add(time.Hour)
		if end.After(week) {
			spend.WeekUSD += h.USD
		}
		if end.After(day) {
			spend.DayUSD += h.USD
		}
	}
	spend.DayUSD = math.Round(spend.DayUSD*100) / 100
	spend.WeekUSD = math.Round(spend.WeekUSD*100) / 100
	return spend
}

// startSpend works the summary's spend out now and once a minute until the
// server stops, for an agent whose session logs AgentAPI reads (native
// Claude Code, Codex and Pi).
func (s *Server) startSpend() {
	if s.sessions == nil {
		return
	}
	update := func() {
		costs, err := s.sessions.Costs()
		if err != nil {
			s.logger.Debug("Failed to work out what the sessions spent", "error", err)
			return
		}
		s.emitter.SetSpend(spendOf(costs.Total.Hourly, time.Now()))
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
