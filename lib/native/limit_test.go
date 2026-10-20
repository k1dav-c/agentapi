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

// limitTurn starts a turn and has the agent stop it on a usage limit.
func limitTurn(t *testing.T, c *native.Conversation, sink native.Sink, result native.TurnResult) {
	t.Helper()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	result.StopReason = native.StopReasonLimited
	sink.TurnCompleted(result)
}

func TestLimitStopHoldsTheQueueUntilTheReset(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock})
	sink := factory.Last().Sink()
	resets := clock.Now().Add(time.Hour)

	limitTurn(t, c, sink, native.TurnResult{Error: "You've hit your limit · resets 3pm", ResetsAt: &resets})
	ls := c.LimitStop()
	require.NotNil(t, ls)
	assert.Equal(t, "You've hit your limit · resets 3pm", ls.Message)
	assert.Equal(t, resets, *ls.ResetsAt)
	assert.Nil(t, ls.ContinueAt, "nothing continues without --auto-continue")
	assert.Empty(t, rec.get().errors, "the limit stop shows the message, not an error")
	assert.True(t, c.Idle())

	assert.True(t, c.HoldsQueue(clock.Now()))
	assert.False(t, c.HoldsQueue(resets), "the queue goes on once the limit reset")

	// A turn that starts ends the stop.
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "go on"}))
	assert.Nil(t, c.LimitStop())
	stops := rec.get().limitStops
	require.Len(t, stops, 2)
	assert.Nil(t, stops[1])
}

func TestLimitStopTakesTheResetOfTheReachedLimit(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	sink := factory.Last().Sink()
	weekly, fiveHour := clock.Now().Add(48*time.Hour), clock.Now().Add(2*time.Hour)
	full, half := 100.0, 50.0
	native.ReportUsage(sink, native.AgentUsage{RateLimits: []native.RateLimit{
		{ID: "five_hour", UsedPercent: &full, ResetsAt: &fiveHour},
		{ID: "seven_day", UsedPercent: &half, ResetsAt: &weekly},
	}})

	limitTurn(t, c, sink, native.TurnResult{Error: "limit"})
	ls := c.LimitStop()
	require.NotNil(t, ls)
	require.NotNil(t, ls.ResetsAt)
	assert.Equal(t, fiveHour, *ls.ResetsAt)
}

func TestLimitStopWithoutResetHoldsTheQueueUntilDismissed(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock, AutoContinue: true})
	limitTurn(t, c, factory.Last().Sink(), native.TurnResult{Error: "You're out of usage credits"})

	ls := c.LimitStop()
	require.NotNil(t, ls)
	assert.Nil(t, ls.ResetsAt)
	assert.Nil(t, ls.ContinueAt, "with no reset time there is nothing to wait for")
	assert.True(t, c.HoldsQueue(clock.Now().Add(24*time.Hour)))

	require.NoError(t, c.DismissLimitStop())
	assert.Nil(t, c.LimitStop())
	assert.False(t, c.HoldsQueue(clock.Now()))
	assert.ErrorIs(t, c.DismissLimitStop(), native.ErrNoLimitStop)
	assert.ErrorIs(t, c.ScheduleContinue(nil), native.ErrNoLimitStop)
}

func TestAutoContinueAfterTheReset(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock, AutoContinue: true})
	a := factory.Last()
	resets := clock.Now().Add(time.Hour)
	limitTurn(t, c, a.Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})

	ls := c.LimitStop()
	require.NotNil(t, ls)
	require.NotNil(t, ls.ContinueAt)
	assert.Equal(t, resets.Add(time.Minute), *ls.ContinueAt, "a minute after the reset")
	// The queue waits for the continuation, even after the reset.
	assert.True(t, c.HoldsQueue(resets.Add(30*time.Second)))

	sent, err := c.ContinueIfDue(context.Background(), resets)
	require.NoError(t, err)
	assert.False(t, sent, "not due yet")

	sent, err = c.ContinueIfDue(context.Background(), resets.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, sent)
	assert.Nil(t, c.LimitStop())
	turns := a.Turns()
	require.Len(t, turns, 2)
	assert.Equal(t, native.ContinuePrompt, turns[1].Text)

	// The limit stops the continuation again: it is scheduled again.
	later := resets.Add(time.Hour)
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonLimited, Error: "limit", ResetsAt: &later})
	ls = c.LimitStop()
	require.NotNil(t, ls)
	require.NotNil(t, ls.ContinueAt)
	assert.Equal(t, later.Add(time.Minute), *ls.ContinueAt)
}

func TestScheduledContinueCanBeCancelled(t *testing.T) {
	clock := quartz.NewMock(t)
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	a := factory.Last()
	resets := clock.Now().Add(time.Hour)
	limitTurn(t, c, a.Sink(), native.TurnResult{Error: "limit", ResetsAt: &resets})

	at := c.LimitStop().ContinueAtReset()
	require.NoError(t, c.ScheduleContinue(at))
	require.NotNil(t, c.LimitStop().ContinueAt)
	assert.True(t, c.HoldsQueue(resets.Add(30*time.Second)))

	require.NoError(t, c.ScheduleContinue(nil))
	assert.Nil(t, c.LimitStop().ContinueAt)
	sent, err := c.ContinueIfDue(context.Background(), resets.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, sent)
	assert.Len(t, a.Turns(), 1)
	assert.False(t, c.HoldsQueue(resets), "the queue still waits only for the reset")
}

func TestRestartClearsTheLimitStop(t *testing.T) {
	clock := quartz.NewMock(t)
	resets := clock.Now().Add(time.Hour)
	c, _, _ := start(t, nil, native.Config{Clock: clock, LimitStop: &native.LimitStop{Message: "limit", ResetsAt: &resets}})
	require.NotNil(t, c.LimitStop(), "restored from the state database")
	require.NoError(t, c.Restart(context.Background()))
	assert.Nil(t, c.LimitStop())
}
