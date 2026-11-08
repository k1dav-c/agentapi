package native_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

func TestRequestLifecycle(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))

	// Waiting on the user counts as stable, but no new turn can start.
	assert.Equal(t, conv.StatusStable, c.Status())
	assert.False(t, c.Idle())
	pending := c.PendingRequests()
	require.Len(t, pending, 1)
	assert.Equal(t, agentstate.ResponseLive, pending[0].Capability)

	_, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "nope"})
	assert.ErrorIs(t, err, native.ErrInvalidResponse)
	_, err = c.Respond(context.Background(), "missing", agentstate.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	state, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "allow"})
	require.NoError(t, err)
	assert.Equal(t, agentstate.RequestStatusResolved, state.Status)
	assert.Equal(t, conv.StatusChanging, c.Status())
	assert.Equal(t, []nativetest.Response{{RequestID: "r1", Response: agentstate.Response{OptionID: "allow"}}}, a.Responses())

	// A second answer is refused.
	_, err = c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "deny"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	// The agent withdraws a request; then a turn end expires the rest.
	a.Sink().RequestOpened(approval("r2"))
	a.Sink().RequestClosed("r2")
	r2, ok := c.Request("r2")
	require.True(t, ok)
	assert.Equal(t, agentstate.RequestStatusWithdrawn, r2.Status)

	a.Sink().RequestOpened(approval("r3"))
	a.Sink().RequestOpened(question("q1"))
	a.Sink().TurnCompleted(native.TurnResult{})
	r3, _ := c.Request("r3")
	q1, _ := c.Request("q1")
	assert.Equal(t, agentstate.RequestStatusExpired, r3.Status)
	assert.Equal(t, agentstate.ResponseNotResumable, r3.Capability)
	assert.Equal(t, agentstate.RequestStatusExpired, q1.Status)
	assert.Equal(t, agentstate.ResponseMessage, q1.Capability)
	assert.Empty(t, c.PendingRequests())
	assert.NotEmpty(t, rec.get().requests)
}

func TestWithdrawnRequestCapability(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))
	a.Sink().RequestOpened(question("q1"))
	// Pi closes the open dialogs of a turn before the turn ends.
	a.Sink().RequestClosed("r1")
	a.Sink().RequestClosed("q1")
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})

	r1, _ := c.Request("r1")
	assert.Equal(t, agentstate.RequestStatusWithdrawn, r1.Status)
	assert.Equal(t, agentstate.ResponseNotResumable, r1.Capability)
	q1, _ := c.Request("q1")
	assert.Equal(t, agentstate.RequestStatusWithdrawn, q1.Status)
	assert.Equal(t, agentstate.ResponseMessage, q1.Capability, "a message can still answer a question")
}

func TestRespondFailureKeepsRequestPending(t *testing.T) {
	boom := errors.New("boom")
	c, factory, _ := start(t, func(a *nativetest.Adapter) { a.RespondErr = boom }, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(approval("r1"))
	_, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, boom)
	assert.Len(t, c.PendingRequests(), 1)
}

// An answer the agent didn't confirm may have reached it: the request
// takes no other answer until the agent closes it or the turn ends.
func TestUnconfirmedAnswerHoldsTheRequest(t *testing.T) {
	unconfirmed := native.Unconfirmed(context.DeadlineExceeded)
	for _, tc := range []struct {
		name   string
		close  func(a *nativetest.Adapter)
		status agentstate.RequestStatus
	}{
		{"closed by the agent", func(a *nativetest.Adapter) { a.Sink().RequestClosed("r1") }, agentstate.RequestStatusWithdrawn},
		{"the turn ends", func(a *nativetest.Adapter) { a.Sink().TurnCompleted(native.TurnResult{}) }, agentstate.RequestStatusExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, factory, rec := start(t, func(a *nativetest.Adapter) {
				a.RespondErr = unconfirmed
				a.OnRespond = func(context.Context) { calls++ }
			}, native.Config{})
			a := factory.Last()
			require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
			a.Sink().RequestOpened(approval("r1"))

			state, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "allow"})
			require.ErrorIs(t, err, native.ErrUnconfirmed)
			assert.Equal(t, agentstate.RequestStatusPending, state.Status)
			assert.Equal(t, conv.StatusChanging, c.Status(), "the answer is on its way")
			_, err = c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "deny"})
			require.ErrorIs(t, err, native.ErrRequestNotFound)
			assert.Equal(t, 1, calls, "the adapter got one answer")
			assert.Len(t, rec.get().requests, 1, "nothing changed")

			tc.close(a)
			r1, _ := c.Request("r1")
			assert.Equal(t, tc.status, r1.Status)
			assert.Nil(t, r1.Response)
			assert.Empty(t, c.PendingRequests())
		})
	}
}

func TestWithdrawnWhileRespondingFails(t *testing.T) {
	boom := errors.New("boom")
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.RespondErr = boom
		// The agent stops waiting while the answer is on its way.
		a.OnRespond = func(context.Context) { a.Sink().RequestClosed("r1") }
	}, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))

	state, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, agentstate.RequestStatusWithdrawn, state.Status)
	assert.Equal(t, agentstate.ResponseNotResumable, state.Capability)
	assert.Empty(t, c.PendingRequests())
	assert.Equal(t, conv.StatusChanging, c.Status(), "nothing waits on the user any more")
}

func TestTurnEndWhileRespondingResolves(t *testing.T) {
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		// The turn ends before Respond returns.
		a.OnRespond = func(context.Context) {
			a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
		}
	}, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))

	state, err := c.Respond(context.Background(), "r1", agentstate.Response{OptionID: "allow"})
	require.NoError(t, err)
	assert.Equal(t, agentstate.RequestStatusResolved, state.Status)
	assert.Equal(t, &agentstate.Response{OptionID: "allow"}, state.Response)
	var statuses []agentstate.RequestStatus
	for _, r := range rec.get().requests {
		statuses = append(statuses, r.Status)
	}
	assert.Equal(t, []agentstate.RequestStatus{agentstate.RequestStatusPending, agentstate.RequestStatusResolved}, statuses,
		"observers hear about the request when it opens and when it closes")
}

func TestAnsweredRequestsAreTrimmed(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	for i := range agentstate.MaxClosedRequests + 10 {
		id := fmt.Sprint("r", i)
		a.Sink().RequestOpened(approval(id))
		_, err := c.Respond(context.Background(), id, agentstate.Response{OptionID: "allow"})
		require.NoError(t, err)
	}
	assert.Len(t, c.Requests(), agentstate.MaxClosedRequests)
	_, kept := c.Request("r0")
	assert.False(t, kept, "the oldest are dropped")
}

func TestQuestionValidation(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(agentstate.Request{
		ID: "q", Kind: agentstate.RequestKindQuestion,
		Questions: []agentstate.Question{
			{ID: "color", Question: "Color?", Options: []agentstate.QuestionOption{{Label: "Red"}, {Label: "Blue"}}},
			{ID: "tags", Question: "Tags?", MultiSelect: true, Options: []agentstate.QuestionOption{{Label: "a"}, {Label: "b"}}},
			{ID: "name", Question: "Name?", FreeText: true},
		},
	})
	cases := []map[string][]string{
		{"color": {"Red"}, "tags": {"a"}},
		{"color": {"Green"}, "tags": {"a"}, "name": {"x"}},
		{"color": {"Red", "Blue"}, "tags": {"a"}, "name": {"x"}},
	}
	for _, answers := range cases {
		_, err := c.Respond(context.Background(), "q", agentstate.Response{Answers: answers})
		assert.ErrorIs(t, err, native.ErrInvalidResponse, answers)
	}
	_, err := c.Respond(context.Background(), "q", agentstate.Response{Answers: map[string][]string{
		"color": {"Red"}, "tags": {"a", "b"}, "name": {"anything"},
	}})
	assert.NoError(t, err)
}

func TestSecretAnswersAreRedacted(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(agentstate.Request{
		ID: "q", Kind: agentstate.RequestKindQuestion,
		Questions: []agentstate.Question{{ID: "token", Question: "Token?", Secret: true}, {ID: "name", Question: "Name?"}},
	})
	answers := map[string][]string{"token": {"s3cret"}, "name": {"bob"}}
	state, err := c.Respond(context.Background(), "q", agentstate.Response{Answers: answers})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"token": {native.RedactedAnswer}, "name": {"bob"}}, state.Response.Answers)
	assert.Equal(t, []string{"s3cret"}, a.Responses()[0].Response.Answers["token"], "the agent gets the answer")
	assert.Equal(t, []string{"s3cret"}, answers["token"], "the caller's map is not changed")
	for _, r := range rec.get().requests {
		if r.Response != nil {
			assert.NotContains(t, r.Response.Answers["token"], "s3cret")
		}
	}
}

// The closed requests kept are the most recently closed, whenever they
// opened: a request that waited long isn't dropped as it closes.
func TestTheMostRecentlyClosedRequestsAreKept(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	sink := factory.Last().Sink()
	sink.RequestOpened(approval("long"))
	for i := range agentstate.MaxClosedRequests {
		id := fmt.Sprint("r", i)
		sink.RequestOpened(approval(id))
		clock.Advance(time.Second)
		sink.RequestClosed(id)
	}
	clock.Advance(time.Second)
	sink.RequestClosed("long")

	requests := c.Requests()
	require.Len(t, requests, agentstate.MaxClosedRequests)
	assert.Equal(t, "long", requests[0].ID, "kept, in the order it opened")
	assert.Equal(t, agentstate.RequestStatusWithdrawn, requests[0].Status)
	_, kept := c.Request("r0")
	assert.False(t, kept, "the first to close is dropped")
}
