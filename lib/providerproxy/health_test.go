package providerproxy

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
)

func TestHealthWindow(t *testing.T) {
	t.Parallel()
	b := newHealthBook()
	start := time.Date(2026, 11, 14, 10, 0, 30, 0, time.UTC)
	assert.Nil(t, b.of("gw", start))

	failure := &agentstate.ProviderFailure{At: start, Kind: agentstate.ProviderFailureStatus, Status: 503, Message: "down"}
	b.record("gw", start, outcomeFailure, 0, failure)
	b.record("gw", start.Add(10*time.Minute), outcomeClientError, 300*time.Millisecond, nil)
	for i := range 10 {
		// 100 ms to 1 s.
		b.record("gw", start.Add(20*time.Minute), outcomeSuccess, time.Duration(i+1)*100*time.Millisecond, nil)
	}
	h := b.of("gw", start.Add(30*time.Minute))
	require.NotNil(t, h)
	assert.Equal(t, 12, h.Requests)
	assert.Equal(t, 1, h.Failures)
	assert.Equal(t, 1, h.ClientErrors)
	assert.EqualValues(t, 500, h.FirstByteP50MS, "of 11 answers, failures left out")
	assert.EqualValues(t, 1000, h.FirstByteP95MS)
	assert.Equal(t, *failure, *h.LastFailure)
	assert.Equal(t, start.Add(20*time.Minute), *h.LastSuccessAt)

	// An hour on, the failure left the window.
	h = b.of("gw", start.Add(time.Hour))
	require.NotNil(t, h)
	assert.Equal(t, 11, h.Requests)
	assert.Zero(t, h.Failures)
	assert.Nil(t, h.LastFailure)
	h = b.of("gw", start.Add(70*time.Minute))
	assert.Equal(t, 10, h.Requests)
	assert.Zero(t, h.ClientErrors)
	assert.EqualValues(t, 500, h.FirstByteP50MS, "nearest rank of 10")

	// Gone after an hour without calls; all forgets it.
	assert.Nil(t, b.of("gw", start.Add(81*time.Minute)))
	assert.Empty(t, b.all(start.Add(81*time.Minute)))
	assert.Empty(t, b.providers)
}

func TestHealthSamples(t *testing.T) {
	t.Parallel()
	b := newHealthBook()
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	// 150 slow answers, then 100 fast ones: the percentiles are of the
	// latest 100.
	for range 150 {
		b.record("gw", now, outcomeSuccess, 5*time.Second, nil)
	}
	for i := range 100 {
		b.record("gw", now, outcomeSuccess, time.Duration(i+1)*time.Millisecond, nil)
	}
	h := b.of("gw", now)
	assert.Equal(t, 250, h.Requests)
	assert.EqualValues(t, 50, h.FirstByteP50MS)
	assert.EqualValues(t, 95, h.FirstByteP95MS)

	// Each provider on its own.
	b.record("glm", now, outcomeFailure, 0, &agentstate.ProviderFailure{At: now, Kind: agentstate.ProviderFailureNetwork, Message: "refused"})
	h = b.of("glm", now)
	assert.Equal(t, 1, h.Requests)
	assert.Zero(t, h.FirstByteP50MS, "no answer")
	assert.Len(t, b.all(now), 2)
}

func TestHealthEqual(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	h := agentstate.ProviderHealth{Requests: 2, Failures: 1, LastSuccessAt: &at, LastFailure: &agentstate.ProviderFailure{At: at, Kind: agentstate.ProviderFailureTimeout}}
	c := h.Clone()
	assert.True(t, h.Equal(c))
	c.LastFailure.Message = "changed"
	assert.False(t, h.Equal(c), "a clone shares nothing")
	assert.Empty(t, h.LastFailure.Message)
	c = h.Clone()
	c.LastSuccessAt = nil
	assert.False(t, h.Equal(c))
}

func TestErrorMessage(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Overloaded", errorMessage(529, []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)))
	assert.Equal(t, "quota", errorMessage(429, []byte(`{"error":{"message":"quota","type":"insufficient_quota"}}`)))
	assert.Equal(t, "plain", errorMessage(500, []byte(`{"error":"plain"}`)))
	assert.Equal(t, "top", errorMessage(500, []byte(`{"message":"top"}`)))
	assert.Equal(t, "503 Service Unavailable", errorMessage(503, []byte(`upstream connect error`)))
	assert.Equal(t, "a b", shortMessage(" a\n\tb "))
	long := shortMessage(strings.Repeat("é", 500))
	assert.Equal(t, messageLimit, len([]rune(long)))
	assert.True(t, strings.HasSuffix(long, "…"))
}
