package agentstate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/agentstate"
)

func TestExcessClosed(t *testing.T) {
	t.Parallel()
	at := func(second int) *time.Time {
		v := time.Date(2026, 10, 18, 9, 0, second, 0, time.UTC)
		return &v
	}
	requests := []agentstate.RequestState{{Status: agentstate.RequestStatusPending}}
	for i := range agentstate.MaxClosedRequests {
		requests = append(requests, agentstate.RequestState{Status: agentstate.RequestStatusResolved, ClosedAt: at(i + 1)})
	}
	state := func(i int) *agentstate.RequestState { return &requests[i] }
	assert.Empty(t, agentstate.ExcessClosed(len(requests), state), "pending requests don't count")

	requests = append(requests,
		agentstate.RequestState{Status: agentstate.RequestStatusExpired, ClosedAt: at(0)},
		agentstate.RequestState{Status: agentstate.RequestStatusWithdrawn},
		agentstate.RequestState{Status: agentstate.RequestStatusResolved, ClosedAt: at(1)})
	assert.Equal(t, []int{1, len(requests) - 3, len(requests) - 2}, agentstate.ExcessClosed(len(requests), state),
		"the first to close go, a close without a time first of all; a tie keeps the order")
}
