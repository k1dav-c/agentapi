package native_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// claudeFailed502 is the error the Claude adapter ends a turn with when the
// API answered 502.
const claudeFailed502 = "Claude Code: the turn failed (api_error), API status 502: " + claude502

func transientResult() native.TurnResult {
	return native.TurnResult{StopReason: native.StopReasonTransient, Error: claudeFailed502, APIStatus: 502}
}

func TestTransientFailureIsTriedAgain(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()
	stopped := clock.Now()

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	a.Sink().TurnCompleted(transientResult())

	rw := c.RetryWait()
	require.NotNil(t, rw)
	assert.Equal(t, native.RetryWait{
		Message: claudeFailed502, APIStatus: 502, StoppedAt: stopped,
		Attempt: 1, Attempts: 3, RetryAt: stopped.Add(30 * time.Second),
	}, *rw)
	errs := rec.get().errors
	require.Len(t, errs, 1)
	assert.Equal(t, claudeFailed502+" AgentAPI tries again in 30s (1 of 3).", errs[0])
	assert.True(t, c.Idle())
	assert.True(t, c.HoldsQueue(clock.Now()))
	assert.True(t, c.HoldsQueue(clock.Now().Add(time.Hour)), "until the retry went out")

	sent, err := c.ContinueIfDue(context.Background(), stopped.Add(29*time.Second))
	require.NoError(t, err)
	assert.False(t, sent, "not due yet")

	// Each failure of a retry waits the next delay.
	for i, delay := range []time.Duration{2 * time.Minute, 5 * time.Minute} {
		due := c.RetryWait().RetryAt
		clock.Set(due)
		sent, err = c.ContinueIfDue(context.Background(), due)
		require.NoError(t, err)
		require.True(t, sent)
		assert.Nil(t, c.RetryWait(), "the retry runs")
		assert.False(t, c.HoldsQueue(due))
		turns := a.Turns()
		require.Len(t, turns, i+2)
		assert.Equal(t, native.ContinuePrompt, turns[i+1].Text)

		a.Sink().TurnCompleted(transientResult())
		rw = c.RetryWait()
		require.NotNil(t, rw)
		assert.Equal(t, i+2, rw.Attempt)
		assert.Equal(t, due.Add(delay), rw.RetryAt)
	}
	assert.Contains(t, rec.get().errors, claudeFailed502+" AgentAPI tries again in 5m (3 of 3).")

	// The last retry fails too: AgentAPI gives up.
	due := c.RetryWait().RetryAt
	sent, err = c.ContinueIfDue(context.Background(), due)
	require.NoError(t, err)
	require.True(t, sent)
	a.Sink().TurnCompleted(transientResult())
	assert.Nil(t, c.RetryWait())
	assert.False(t, c.HoldsQueue(due))
	errs = rec.get().errors
	assert.Equal(t, claudeFailed502+" AgentAPI tried again 3 times and gave up.", errs[len(errs)-1])
}

func TestRetryThatWorksStartsTheCountAgain(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	a.Sink().TurnCompleted(transientResult())
	sent, err := c.ContinueIfDue(context.Background(), c.RetryWait().RetryAt)
	require.NoError(t, err)
	require.True(t, sent)
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	assert.Nil(t, c.RetryWait())

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "more"}))
	a.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 1, c.RetryWait().Attempt, "a new failure is the first again")
}

func TestUserMessageReplacesTheRetry(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	a.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "try something else"}))
	assert.Nil(t, c.RetryWait())
	waits := rec.get().retryWaits
	require.Len(t, waits, 2)
	assert.Nil(t, waits[1])
	// Its failure is the first of a new run of retries.
	a.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 1, c.RetryWait().Attempt)
}

func TestRetryNowAndCancel(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()
	assert.ErrorIs(t, c.RetryNow(context.Background()), native.ErrNoRetryWait)
	assert.ErrorIs(t, c.CancelRetry(), native.ErrNoRetryWait)

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	a.Sink().TurnCompleted(transientResult())
	require.NoError(t, c.RetryNow(context.Background()))
	assert.Nil(t, c.RetryWait())
	assert.Equal(t, native.ContinuePrompt, a.Turns()[1].Text)
	// Retrying now counts as the retry.
	a.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 2, c.RetryWait().Attempt)

	require.NoError(t, c.CancelRetry())
	assert.Nil(t, c.RetryWait())
	assert.False(t, c.HoldsQueue(clock.Now()))
	sent, err := c.ContinueIfDue(context.Background(), clock.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, sent)
	assert.Len(t, a.Turns(), 2)
}

func TestRetryDelaysOff(t *testing.T) {
	clock := quartz.NewMock(t)
	stopped := clock.Now()
	restored := &native.RetryWait{Message: "502", StoppedAt: stopped, Attempt: 1, Attempts: 3, RetryAt: stopped}
	c, factory, rec := start(t, nil, native.Config{Clock: clock, RetryDelays: []time.Duration{}, RetryWait: restored})
	assert.Nil(t, c.RetryWait(), "a restored wait goes without retries")

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	factory.Last().Sink().TurnCompleted(transientResult())
	assert.Nil(t, c.RetryWait())
	assert.Equal(t, []string{claudeFailed502}, rec.get().errors)
}

func TestRestoredRetryWaitGoesOn(t *testing.T) {
	clock := quartz.NewMock(t)
	stopped := clock.Now().Add(-time.Hour)
	restored := &native.RetryWait{Message: claudeFailed502, StoppedAt: stopped, Attempt: 2, Attempts: 3, RetryAt: stopped.Add(2 * time.Minute)}
	c, factory, _ := start(t, nil, native.Config{Clock: clock, RetryWait: restored})
	require.NotNil(t, c.RetryWait(), "restored from the state database")
	assert.True(t, c.HoldsQueue(clock.Now()))

	sent, err := c.ContinueIfDue(context.Background(), clock.Now())
	require.NoError(t, err)
	require.True(t, sent, "overdue after the restart")
	factory.Last().Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 3, c.RetryWait().Attempt, "the count goes on")
	assert.Equal(t, clock.Now().Add(5*time.Minute), c.RetryWait().RetryAt)

	require.NoError(t, c.Restart(context.Background()))
	assert.Nil(t, c.RetryWait(), "a new conversation has nothing to try again")
}
