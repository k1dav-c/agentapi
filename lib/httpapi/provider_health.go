package httpapi

import (
	"maps"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

// providerHealthInterval is how often the summary's provider_health is
// worked out again: it changes with each call, and its hour moves on.
const providerHealthInterval = 30 * time.Second

// providerHealth is the health of the providers the agent called over the
// last hour, by ID.
func (s *Server) providerHealth() []instanceapi.SummaryProviderHealth {
	all := s.proxy.AllHealth()
	var out []instanceapi.SummaryProviderHealth
	for _, id := range slices.Sorted(maps.Keys(all)) {
		out = append(out, instanceapi.SummaryProviderHealth{Provider: id, ProviderHealth: all[id]})
	}
	return out
}

// updateProviderHealth puts the providers' health in the summary now, for
// an agent that can run on providers.
func (s *Server) updateProviderHealth() {
	if s.proxy != nil {
		s.emitter.SetProviderHealth(s.providerHealth())
	}
}

// startProviderHealth puts the providers' health in the summary every
// providerHealthInterval until the server stops, and GET /summary does
// at once, for an agent that can run on providers.
func (s *Server) startProviderHealth() {
	if s.proxy == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(providerHealthInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.shutdownCtx.Done():
				return
			case <-ticker.C:
				s.updateProviderHealth()
			}
		}
	}()
}
