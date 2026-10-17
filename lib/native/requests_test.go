package native_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	st "github.com/coder/agentapi/lib/screentracker"
)

func TestRequestLifecycle(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(approval("r1"))

	// Waiting on the user counts as stable, but no new turn can start.
	assert.Equal(t, st.ConversationStatusStable, c.Status())
	assert.False(t, c.Idle())
	pending := c.PendingRequests()
	require.Len(t, pending, 1)
	assert.Equal(t, native.ResponseLive, pending[0].Capability)

	_, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "nope"})
	assert.ErrorIs(t, err, native.ErrInvalidResponse)
	_, err = c.Respond(context.Background(), "missing", native.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	state, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	require.NoError(t, err)
	assert.Equal(t, native.RequestStatusResolved, state.Status)
	assert.Equal(t, st.ConversationStatusChanging, c.Status())
	assert.Equal(t, []nativetest.Response{{RequestID: "r1", Response: native.Response{OptionID: "allow"}}}, a.Responses())

	// A second answer is refused.
	_, err = c.Respond(context.Background(), "r1", native.Response{OptionID: "deny"})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)

	// The agent withdraws a request; then a turn end expires the rest.
	a.Sink().RequestOpened(approval("r2"))
	a.Sink().RequestClosed("r2")
	r2, ok := c.Request("r2")
	require.True(t, ok)
	assert.Equal(t, native.RequestStatusWithdrawn, r2.Status)

	a.Sink().RequestOpened(approval("r3"))
	a.Sink().RequestOpened(question("q1"))
	a.Sink().TurnCompleted(native.TurnResult{})
	r3, _ := c.Request("r3")
	q1, _ := c.Request("q1")
	assert.Equal(t, native.RequestStatusExpired, r3.Status)
	assert.Equal(t, native.ResponseNotResumable, r3.Capability)
	assert.Equal(t, native.RequestStatusExpired, q1.Status)
	assert.Equal(t, native.ResponseMessage, q1.Capability)
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
	assert.Equal(t, native.RequestStatusWithdrawn, r1.Status)
	assert.Equal(t, native.ResponseNotResumable, r1.Capability)
	q1, _ := c.Request("q1")
	assert.Equal(t, native.RequestStatusWithdrawn, q1.Status)
	assert.Equal(t, native.ResponseMessage, q1.Capability, "a message can still answer a question")
}

func TestRespondFailureKeepsRequestPending(t *testing.T) {
	boom := errors.New("boom")
	c, factory, _ := start(t, func(a *nativetest.Adapter) { a.RespondErr = boom }, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(approval("r1"))
	_, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, boom)
	assert.Len(t, c.PendingRequests(), 1)
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

	state, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, native.RequestStatusWithdrawn, state.Status)
	assert.Equal(t, native.ResponseNotResumable, state.Capability)
	assert.Empty(t, c.PendingRequests())
	assert.Equal(t, st.ConversationStatusChanging, c.Status(), "nothing waits on the user any more")
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

	state, err := c.Respond(context.Background(), "r1", native.Response{OptionID: "allow"})
	require.NoError(t, err)
	assert.Equal(t, native.RequestStatusResolved, state.Status)
	assert.Equal(t, &native.Response{OptionID: "allow"}, state.Response)
	var statuses []native.RequestStatus
	for _, r := range rec.get().requests {
		statuses = append(statuses, r.Status)
	}
	assert.Equal(t, []native.RequestStatus{native.RequestStatusPending, native.RequestStatusResolved}, statuses,
		"observers hear about the request when it opens and when it closes")
}

func TestAnsweredRequestsAreTrimmed(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	for i := range native.MaxClosedRequests + 10 {
		id := fmt.Sprint("r", i)
		a.Sink().RequestOpened(approval(id))
		_, err := c.Respond(context.Background(), id, native.Response{OptionID: "allow"})
		require.NoError(t, err)
	}
	assert.Len(t, c.Requests(), native.MaxClosedRequests)
	_, kept := c.Request("r0")
	assert.False(t, kept, "the oldest are dropped")
}

func TestQuestionValidation(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(native.Request{
		ID: "q", Kind: native.RequestKindQuestion,
		Questions: []native.Question{
			{ID: "color", Question: "Color?", Options: []native.QuestionOption{{Label: "Red"}, {Label: "Blue"}}},
			{ID: "tags", Question: "Tags?", MultiSelect: true, Options: []native.QuestionOption{{Label: "a"}, {Label: "b"}}},
			{ID: "name", Question: "Name?", FreeText: true},
		},
	})
	cases := []map[string][]string{
		{"color": {"Red"}, "tags": {"a"}},
		{"color": {"Green"}, "tags": {"a"}, "name": {"x"}},
		{"color": {"Red", "Blue"}, "tags": {"a"}, "name": {"x"}},
	}
	for _, answers := range cases {
		_, err := c.Respond(context.Background(), "q", native.Response{Answers: answers})
		assert.ErrorIs(t, err, native.ErrInvalidResponse, answers)
	}
	_, err := c.Respond(context.Background(), "q", native.Response{Answers: map[string][]string{
		"color": {"Red"}, "tags": {"a", "b"}, "name": {"anything"},
	}})
	assert.NoError(t, err)
}

func TestSecretAnswersAreRedacted(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().RequestOpened(native.Request{
		ID: "q", Kind: native.RequestKindQuestion,
		Questions: []native.Question{{ID: "token", Question: "Token?", Secret: true}, {ID: "name", Question: "Name?"}},
	})
	answers := map[string][]string{"token": {"s3cret"}, "name": {"bob"}}
	state, err := c.Respond(context.Background(), "q", native.Response{Answers: answers})
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
