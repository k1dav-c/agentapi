package agentstate

import (
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

// ProviderHealth is how a model provider answered the agent's calls
// through AgentAPI's provider proxy over the last hour.
type ProviderHealth struct {
	Requests int `json:"requests" doc:"The agent's calls to the provider over the last hour that got an answer or failed; those the agent cancelled before an answer don't count."`
	// Failures are the provider's; ClientErrors the agent's requests the
	// provider refused.
	Failures     int `json:"failures" doc:"Of the requests, those the provider failed: a network error or timeout, a 5xx or 429 answer, or an answer that broke off."`
	ClientErrors int `json:"client_errors" doc:"Of the requests, those answered with a 4xx other than 429 (a key the provider refuses, a model it doesn't have): the provider answered, so they don't count as failures."`
	// The percentiles are of the latest 100 answers within the hour that
	// weren't failures; 0 without one.
	FirstByteP50MS int64      `json:"first_byte_p50_ms" doc:"Median time in milliseconds from the call to the provider's first response byte (its headers), over the latest 100 answers of the hour that weren't failures; 0 without one."`
	FirstByteP95MS int64      `json:"first_byte_p95_ms" doc:"The 95th percentile of the same."`
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty" doc:"When the latest call of the hour that wasn't a failure ended; missing without one."`
	// LastFailure is the latest failure of the hour.
	LastFailure *ProviderFailure `json:"last_failure,omitempty" doc:"The latest failure of the hour; missing without one."`
}

// ProviderFailureKind is how a call to a model provider failed.
type ProviderFailureKind string

const (
	// ProviderFailureStatus: the provider answered 5xx or 429.
	ProviderFailureStatus ProviderFailureKind = "status"
	// ProviderFailureNetwork: it couldn't be reached.
	ProviderFailureNetwork ProviderFailureKind = "network"
	// ProviderFailureTimeout: it didn't answer in time.
	ProviderFailureTimeout ProviderFailureKind = "timeout"
	// ProviderFailureStream: its answer broke off.
	ProviderFailureStream ProviderFailureKind = "stream"
)

var ProviderFailureKindValues = []ProviderFailureKind{
	ProviderFailureStatus, ProviderFailureNetwork, ProviderFailureTimeout, ProviderFailureStream,
}

func (k ProviderFailureKind) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "ProviderFailureKind", ProviderFailureKindValues)
}

// ProviderFailure is a call a model provider failed.
type ProviderFailure struct {
	At      time.Time           `json:"at" doc:"When the call failed."`
	Kind    ProviderFailureKind `json:"kind" doc:"status (the provider answered 5xx or 429), network (it couldn't be reached), timeout (it didn't answer in time) or stream (its answer broke off)."`
	Status  int                 `json:"status,omitempty" doc:"The HTTP status the provider answered, for status and stream; missing for the others."`
	Message string              `json:"message" doc:"What went wrong, short: the provider's error message, else the status or the network error."`
}

// Clone returns a copy of h that shares nothing with it.
func (h ProviderHealth) Clone() ProviderHealth {
	if h.LastSuccessAt != nil {
		at := *h.LastSuccessAt
		h.LastSuccessAt = &at
	}
	if h.LastFailure != nil {
		failure := *h.LastFailure
		h.LastFailure = &failure
	}
	return h
}

// Equal reports whether h and o say the same.
func (h ProviderHealth) Equal(o ProviderHealth) bool {
	sameTime := func(a, b *time.Time) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Equal(*b)
	}
	sameFailure := func(a, b *ProviderFailure) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.At.Equal(b.At) && a.Kind == b.Kind && a.Status == b.Status && a.Message == b.Message
	}
	return h.Requests == o.Requests && h.Failures == o.Failures && h.ClientErrors == o.ClientErrors &&
		h.FirstByteP50MS == o.FirstByteP50MS && h.FirstByteP95MS == o.FirstByteP95MS &&
		sameTime(h.LastSuccessAt, o.LastSuccessAt) && sameFailure(h.LastFailure, o.LastFailure)
}
