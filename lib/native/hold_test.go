package native_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// A message whose delivery fails takes its turn back, and with it the
// end of the limit stop: the queue still waits.
func TestFailedDeliveryKeepsTheLimitStop(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()
	limitTurn(t, c, a.Sink(), native.TurnResult{Error: "You're out of usage credits"})
	stop := c.LimitStop()
	require.NotNil(t, stop)

	boom := errors.New("connection reset")
	a.TurnErr = boom
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "go on"}), boom)
	assert.Equal(t, stop, c.LimitStop())
	assert.True(t, c.HoldsQueue(clock.Now()))
	stops := rec.get().limitStops
	assert.Equal(t, stop, stops[len(stops)-1], "the observer has it again")
}

func TestFailedDeliveryKeepsTheRetryWait(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock, RetryDelays: retryDelays})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	a.Sink().TurnCompleted(transientResult())
	wait := c.RetryWait()
	require.NotNil(t, wait)

	boom := errors.New("connection reset")
	a.TurnErr = boom
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "try something else"}), boom)
	assert.Equal(t, wait, c.RetryWait())
	assert.True(t, c.HoldsQueue(clock.Now()))
	waits := rec.get().retryWaits
	assert.Equal(t, wait, waits[len(waits)-1], "the observer has it again")

	// Nor does a retry asked for now that fails drop it.
	assert.ErrorIs(t, c.RetryNow(context.Background()), boom)
	assert.Equal(t, wait, c.RetryWait())
	a.TurnErr = nil
	require.NoError(t, c.RetryNow(context.Background()))
	a.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 2, c.RetryWait().Attempt, "the retry that went out counts")
}

// What the turn that ran was (a continuation, a retry) goes with its
// agent: the next one's turns start a count of their own.
func TestRestartForgetsTheContinuation(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	resets := clock.Now().Add(time.Hour)
	limitTurn(t, c, factory.Last().Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})
	require.NoError(t, c.ScheduleContinue(&resets))
	sent, err := c.ContinueIfDue(context.Background(), resets)
	require.NoError(t, err)
	require.Equal(t, native.HoldLimit, sent)

	// The agent restarts while it works on the continuation.
	require.NoError(t, c.Restart(context.Background()))
	limitTurn(t, c, factory.Last().Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})
	require.NotNil(t, c.LimitStop())
	assert.Nil(t, c.LimitStop().ContinueAt, "the user's turn isn't a continuation, and nothing continues without --auto-continue")
}

func TestAgentExitForgetsTheRetry(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock, RetryDelays: retryDelays})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	first.Sink().TurnCompleted(transientResult())
	require.NoError(t, c.RetryNow(context.Background()))

	// The agent exits while it works on the retry, and starts again.
	first.Exit(errors.New("exit status 1"))
	passRestartDelay(ctx, t, clock, restart, 2*time.Second)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	second := factory.Last()
	require.NotSame(t, first, second)

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "more"}))
	second.Sink().TurnCompleted(transientResult())
	require.NotNil(t, c.RetryWait())
	assert.Equal(t, 1, c.RetryWait().Attempt, "the user's turn is no retry")
}

func TestAgentExitForgetsTheContinuation(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	first := factory.Last()
	resets := clock.Now().Add(time.Hour)
	limitTurn(t, c, first.Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})
	require.NoError(t, c.ScheduleContinue(&resets))
	sent, err := c.ContinueIfDue(context.Background(), resets)
	require.NoError(t, err)
	require.Equal(t, native.HoldLimit, sent)

	first.Exit(errors.New("exit status 1"))
	passRestartDelay(ctx, t, clock, restart, 2*time.Second)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	second := factory.Last()
	require.NotSame(t, first, second)

	limitTurn(t, c, second.Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})
	require.NotNil(t, c.LimitStop())
	assert.Nil(t, c.LimitStop().ContinueAt, "the user's turn isn't a continuation")
}

// A continuation whose delivery fails is tried again a minute later; the
// stop stays meanwhile.
func TestFailedContinuationIsTriedAgain(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock, AutoContinue: true})
	a := factory.Last()
	resets := clock.Now().Add(time.Hour)
	limitTurn(t, c, a.Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})
	due := *c.LimitStop().ContinueAt

	boom := errors.New("connection reset")
	a.TurnErr = boom
	sent, err := c.ContinueIfDue(context.Background(), due)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, native.HoldLimit, sent, "what it tried")
	require.NotNil(t, c.LimitStop())
	assert.Equal(t, due.Add(time.Minute), *c.LimitStop().ContinueAt)
	assert.True(t, c.HoldsQueue(due))
}
