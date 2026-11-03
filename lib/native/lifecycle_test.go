package native_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
)

func TestRestartReplacesAdapter(t *testing.T) {
	c, factory, rec := start(t, func(a *nativetest.Adapter) { a.Ref = "session-1" }, native.Config{ResumeRef: "old"})
	first := factory.Last()
	assert.Equal(t, "old", first.ResumeRef)
	assert.Equal(t, "session-1", c.SessionRef())
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().RequestOpened(approval("r1"))

	require.NoError(t, c.Restart(context.Background()))
	second := factory.Last()
	assert.NotSame(t, first, second)
	assert.True(t, first.Closed())
	assert.Empty(t, second.ResumeRef, "a restart starts a new session")
	assert.Empty(t, c.Messages())
	r1, _ := c.Request("r1")
	assert.Equal(t, native.ResponseNotResumable, r1.Capability)
	assert.True(t, c.Idle())

	// Events from the replaced adapter are ignored.
	first.Sink().RichMessage(text("old", "stale"))
	assert.Empty(t, c.Messages())
	for _, m := range rec.get().rich {
		assert.NotEqual(t, "old", m.MessageID)
	}

	// The replaced adapter's exit is not reported.
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, rec.get().exits)
	assert.Len(t, factory.Adapters(), 2)
}

func TestRestartFailureAfterCtxEndsIsReported(t *testing.T) {
	boom := errors.New("boom")
	release := make(chan struct{})
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" { // the restart's agent
			a.StartBlock = release
			a.StartErr = boom
		}
	}, native.Config{ResumeRef: "old"})
	ctx, cancel := context.WithCancel(context.Background())
	restarted := make(chan error, 1)
	go func() { restarted <- c.Restart(ctx) }()
	require.Eventually(t, inStart(factory, ""), 5*time.Second, time.Millisecond)

	// The caller stops waiting while the new agent starts; its failure is
	// reported all the same, and the agent isn't started again.
	cancel()
	assert.ErrorIs(t, <-restarted, context.Canceled)
	close(release)
	require.Eventually(t, func() bool { return len(rec.get().exits) == 1 }, 5*time.Second, time.Millisecond, "the failed start was not reported")
	exit := rec.get().exits[0]
	assert.ErrorIs(t, exit.Err, boom)
	assert.Zero(t, exit.Attempt, "given up")
	assert.Contains(t, rec.get().errors, "Failed to start the agent: boom")
	assert.Len(t, factory.Adapters(), 2)
}

func TestStopWhileStartingIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*native.Conversation) error
	}{
		{"close", func(c *native.Conversation) error { return c.Close() }},
		{"restart", func(c *native.Conversation) error { return c.Restart(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logRecorder{}
			factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
				if a.ResumeRef != "" { // the first agent never finishes starting
					a.StartBlock = make(chan struct{})
				}
			}}
			c, rec := run(t, factory, native.Config{ResumeRef: "old", Logger: slog.New(logs)})
			require.Eventually(t, inStart(factory, "old"), 5*time.Second, time.Millisecond)

			require.NoError(t, tc.stop(c))
			require.Eventually(t, func() bool { return logs.logged("The agent stopped while it started") }, 5*time.Second, time.Millisecond)
			assert.Empty(t, rec.get().errors)
			assert.Empty(t, rec.get().exits)
		})
	}
}

func TestCapabilitiesStayWhileRestarting(t *testing.T) {
	release := make(chan struct{})
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.Caps = native.Capabilities{Steer: true, Images: true}
		if a.ResumeRef == "" {
			a.StartBlock = release
		}
	}, native.Config{ResumeRef: "old"})
	restarted := make(chan error, 1)
	go func() { restarted <- c.Restart(context.Background()) }()
	require.Eventually(t, inStart(factory, ""), 5*time.Second, time.Millisecond)

	assert.Equal(t, native.Capabilities{Steer: true, Images: true}, c.Capabilities(), "what the last agent supported")
	close(release)
	require.NoError(t, <-restarted)
}

func TestCloseExpiresRequests(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Sink().RequestOpened(approval("r1"))
	require.NoError(t, c.Close())
	r1, _ := c.Request("r1")
	assert.Equal(t, native.RequestStatusExpired, r1.Status)
	assert.Equal(t, native.ResponseNotResumable, r1.Capability)
	assert.False(t, c.TurnActive())
}

func TestInitialPrompt(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{InitialPrompt: "do it"})
	// The prompt's turn began when the agent started, so nothing got in
	// first.
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "queued"}), conv.ErrMessageValidationChanging)
	assert.NotContains(t, rec.get().statuses, conv.StatusStable, "the agent never looked idle")

	require.Eventually(t, func() bool { return len(factory.Last().Turns()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "do it", factory.Last().Turns()[0].Text)
	assert.Len(t, c.Messages(), 1)
}

func TestRestartSendsInitialPromptAgain(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{
		InitialPrompt: "do it",
		InitialMessages: []conv.Message{
			{Id: 0, Role: conv.RoleUser, Message: "do it"},
			{Id: 1, Role: conv.RoleAgent, Message: "done"},
		},
	})
	assert.Empty(t, factory.Last().Turns(), "the continued conversation had the prompt")

	require.NoError(t, c.Restart(context.Background()))
	assert.Equal(t, []native.Input{{Text: "do it"}}, factory.Last().Turns(), "the new session gets it")
	msgs := c.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, 0, msgs[0].Id)
}

func TestSendAndInitialPromptHaveDeadlines(t *testing.T) {
	var mu sync.Mutex
	var deadlines []bool
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.AutoStartTurn = false
		a.OnStartTurn = func(ctx context.Context) {
			_, ok := ctx.Deadline()
			mu.Lock()
			defer mu.Unlock()
			deadlines = append(deadlines, ok)
		}
	}, native.Config{InitialPrompt: "do it"})
	a := factory.Last()
	require.Eventually(t, func() bool { return len(a.Turns()) == 1 }, 5*time.Second, time.Millisecond)
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})

	require.NoError(t, c.Send(context.Background(), conv.MessagePartText{Content: "hi"}))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []bool{true, true}, deadlines, "an agent that hangs can't hold its caller forever")
}

func TestReloadResumesTheSession(t *testing.T) {
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = "session-1"
		}
	}, native.Config{})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	assert.ErrorIs(t, c.Reload(context.Background()), native.ErrTurnActive, "a reload would stop the turn")
	first.Sink().RichMessage(text("m1", "reply"))
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})

	require.NoError(t, c.Reload(context.Background()))
	second := factory.Last()
	assert.NotSame(t, first, second)
	assert.True(t, first.Closed())
	assert.Equal(t, "session-1", second.ResumeRef, "the new agent resumes the session")
	assert.Len(t, c.Messages(), 2, "the conversation stays")
	assert.True(t, c.Idle())
}

func TestReloadNeedsResume(t *testing.T) {
	c, _, _ := start(t, func(a *nativetest.Adapter) { a.Caps.Resume = false }, native.Config{})
	assert.ErrorIs(t, c.Reload(context.Background()), native.ErrUnsupported)
}

func TestSwitchOpensAnotherSession(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{InitialPrompt: "hi"})
	first := factory.Last()
	require.Eventually(t, func() bool { return len(first.Turns()) == 1 }, 5*time.Second, time.Millisecond)
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})

	history := []conv.Message{
		{Id: 0, Role: conv.RoleUser, Message: "earlier"},
		{Id: 1, Role: conv.RoleAgent, Message: "done"},
	}
	require.NoError(t, c.Switch(context.Background(), native.Resume{Ref: "earlier-session"}, history))
	second := factory.Last()
	assert.NotSame(t, first, second)
	assert.True(t, first.Closed())
	assert.Equal(t, "earlier-session", second.ResumeRef)
	assert.False(t, second.Fork)
	assert.Equal(t, "earlier-session", c.SessionRef())
	assert.Equal(t, history, c.Messages())
	assert.Empty(t, second.Turns(), "the initial prompt is not sent again")
	assert.Contains(t, rec.get().refs, "earlier-session", "stored to resume it after a restart")

	// The conversation goes on after the session's messages.
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "more"}))
	msgs := c.Messages()
	require.Len(t, msgs, 3)
	assert.Equal(t, 2, msgs[2].Id)
}

func TestSwitchToAFork(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	require.NoError(t, c.Switch(context.Background(), native.Resume{Ref: "original", Fork: true}, nil))
	fork := factory.Last()
	assert.Equal(t, "original", fork.ResumeRef)
	assert.True(t, fork.Fork)

	// A reload before the agent reported the copy forks again, rather than
	// go on in the original.
	require.NoError(t, c.Reload(context.Background()))
	assert.True(t, factory.Last().Fork)

	// Once the agent reports the copy, it is the session to resume.
	factory.Last().Sink().SessionRef("copy")
	require.NoError(t, c.Reload(context.Background()))
	assert.Equal(t, "copy", factory.Last().ResumeRef)
	assert.False(t, factory.Last().Fork)
}

func TestSwitchWhileATurnRuns(t *testing.T) {
	c, _, _ := start(t, nil, native.Config{})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "work"}))
	assert.ErrorIs(t, c.Switch(context.Background(), native.Resume{Ref: "other"}, nil), native.ErrTurnActive)
}

// A switch is refused while the agent compacts too, and a refused switch
// changes nothing: reset doesn't run and the agent goes on.
func TestBeginSwitchWhileCompactingChangesNothing(t *testing.T) {
	compacting, release := make(chan struct{}), make(chan struct{})
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.Caps.Compact = true
		a.OnCompact = func(context.Context) error {
			close(compacting)
			<-release
			return nil
		}
	}, native.Config{})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	before := c.Messages()
	compacted := make(chan error, 1)
	go func() { compacted <- c.Compact(context.Background()) }()
	<-compacting

	reset := false
	_, err := c.BeginSwitch(native.Resume{Ref: "other"}, nil, func() { reset = true })
	assert.ErrorIs(t, err, native.ErrTurnActive)
	assert.False(t, reset)
	assert.Equal(t, before, c.Messages())
	assert.False(t, first.Closed())
	assert.Len(t, factory.Adapters(), 1)
	_, err = c.BeginReload()
	assert.ErrorIs(t, err, native.ErrTurnActive)

	close(release)
	require.NoError(t, <-compacted)
	assert.True(t, c.Idle())
}

// BeginRestart retires the agent before reset runs, so nothing the old
// agent still reports lands in the conversation that replaces it; Start
// closes it.
func TestBeginRestartRetiresFirst(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().RequestOpened(approval("r1"))

	retired := false
	r := c.BeginRestart(func() {
		requests := rec.get().requests
		retired = len(requests) > 0 && requests[len(requests)-1].Status == native.RequestStatusExpired
	})
	assert.True(t, retired, "the agent's requests expired before the reset")
	statuses := len(rec.get().statuses)
	first.Sink().RichMessage(text("late", "stale"))
	assert.Empty(t, c.Messages(), "the retired agent's events are ignored")
	assert.False(t, c.Idle(), "no turn starts until the new agent did")
	assert.False(t, first.Closed(), "Start closes it")

	require.NoError(t, r.Start(context.Background()))
	assert.True(t, first.Closed())
	assert.Len(t, factory.Adapters(), 2)
	assert.True(t, c.Idle())
	assert.Equal(t, []conv.Status{conv.StatusInitializing, conv.StatusStable}, rec.get().statuses[statuses:],
		"Start reports the new agent's status, from the start")
}

// restartClock returns a mock clock and a trap that catches every wait
// before the agent is started again after an exit.
func restartClock(t *testing.T) (*quartz.Mock, *quartz.Trap) {
	t.Helper()
	clock := quartz.NewMock(t)
	trap := clock.Trap().NewTimer("Conversation", "restart")
	t.Cleanup(trap.Close)
	return clock, trap
}

// passRestartDelay lets the next wait before a restart pass, which must
// be d.
func passRestartDelay(ctx context.Context, t *testing.T, clock *quartz.Mock, trap *quartz.Trap, d time.Duration) {
	t.Helper()
	call := trap.MustWait(ctx)
	assert.Equal(t, d, call.Duration)
	call.Release()
	clock.Advance(d).MustWait(ctx)
}

// attempts returns the Attempt of each exit.
func attempts(exits []native.AgentExit) []int {
	out := make([]int, 0, len(exits))
	for _, exit := range exits {
		out = append(out, exit.Attempt)
	}
	return out
}

func TestExitStartsTheAgentAgainOnItsSession(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = "session-1"
		}
	}, native.Config{Clock: clock})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().RichMessage(text("m1", "working on it"))
	first.Sink().RequestOpened(approval("r1"))

	crash := errors.New("exit status 1: out of memory")
	first.Exit(crash)
	call := restart.MustWait(ctx)
	assert.Equal(t, 2*time.Second, call.Duration)
	r1, _ := c.Request("r1")
	assert.Equal(t, native.RequestStatusExpired, r1.Status, "the agent that asked is gone")
	assert.False(t, c.TurnActive())
	assert.Equal(t, conv.StatusInitializing, c.Status())
	assert.Equal(t, []native.AgentExit{{Err: crash, Attempt: 1, Attempts: 3, Delay: 2 * time.Second}}, rec.get().exits)
	assert.Contains(t, rec.get().errors, "The agent exited: exit status 1: out of memory. AgentAPI starts it again on its session in 2s (attempt 1 of 3).")
	require.Eventually(t, first.Closed, 5*time.Second, time.Millisecond)
	call.Release()
	clock.Advance(2 * time.Second).MustWait(ctx)

	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	second := factory.Last()
	assert.NotSame(t, first, second)
	assert.Equal(t, "session-1", second.ResumeRef, "the session is resumed")
	assert.False(t, second.Fork)
	assert.Len(t, c.Messages(), 2, "the conversation is kept")
	first.Sink().RichMessage(text("late", "stale"))
	assert.Len(t, c.Messages(), 2, "the old agent's events are ignored")
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "two"}))
	assert.Len(t, second.Turns(), 1)
}

// An agent that keeps failing is started again three times; then the
// conversation gives up, and no agent runs until a restart, which works.
func TestRepeatedFailuresGiveUp(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	var sessions atomic.Int32
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = fmt.Sprintf("session-%d", sessions.Add(1))
			return
		}
		a.StartErr = errors.New("the session can't be read")
	}, native.Config{Clock: clock})
	factory.Last().Exit(errors.New("exit status 1"))
	for _, delay := range []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second} {
		passRestartDelay(ctx, t, clock, restart, delay)
	}
	require.Eventually(t, func() bool { return len(rec.get().exits) == 4 }, 5*time.Second, time.Millisecond)
	exits := rec.get().exits
	assert.Equal(t, []int{1, 2, 3, 0}, attempts(exits))
	assert.ErrorContains(t, exits[3].Err, "the session can't be read")
	errs := rec.get().errors
	assert.Equal(t, "Failed to start the agent: the session can't be read. AgentAPI gave up after 3 attempts to start it again; restart the agent to go on.", errs[len(errs)-1])
	assert.Equal(t, conv.StatusInitializing, c.Status())
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "hi"}), conv.ErrMessageValidationChanging, "messages wait")
	clock.Advance(time.Hour).MustWait(ctx)
	assert.Len(t, factory.Adapters(), 4, "nothing starts by itself any more")

	require.NoError(t, c.Restart(context.Background()))
	assert.True(t, c.Idle())
	assert.Equal(t, "session-2", c.SessionRef())
	// The new agent's exits count from the first delay.
	factory.Last().Exit(errors.New("exit status 1"))
	call := restart.MustWait(ctx)
	assert.Equal(t, 2*time.Second, call.Duration)
	call.Release()
}

// An agent that reported a lost login before it exited waits for the login
// like one that finds none when it starts: no restarts, no retry loop.
func TestExitAfterALostLoginWaitsForTheLogin(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	var loggedIn atomic.Bool
	loggedIn.Store(true)
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = "session-1"
		}
		if !loggedIn.Load() {
			a.StartErr = needsLogin("Claude Code", "claude auth login")
		}
	}, native.Config{Clock: clock})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	loggedIn.Store(false)
	first.Sink().LoginRequired(native.LoginRequired{Agent: "Claude Code", Message: "Claude Code's login expired.", Command: "claude auth login"})
	first.Exit(errors.New("exit status 1: OAuth token has expired"))

	retry.MustWait(ctx).Release()
	assert.NotNil(t, c.LoginRequired())
	assert.Equal(t, conv.StatusInitializing, c.Status())
	for range 3 {
		_, w := clock.AdvanceNext()
		w.MustWait(ctx)
		retry.MustWait(ctx).Release()
	}
	assert.Len(t, factory.Adapters(), 4, "each start checks the login")
	assert.Empty(t, rec.get().exits, "not started again as after a crash")

	loggedIn.Store(true)
	require.NoError(t, c.RetryLogin(ctx))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, "session-1", factory.Last().ResumeRef)
	assert.Nil(t, c.LoginRequired())
	assert.Len(t, c.Messages(), 1)
	assert.Empty(t, rec.get().exits)
}

// A start the conversation makes by itself is made again when it fails;
// the agent that starts gets the initial prompt.
func TestFailedStartIsMadeAgain(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	var starts atomic.Int32
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if starts.Add(1) == 1 {
			a.StartErr = errors.New("claude: not found")
		}
	}}
	c, rec := run(t, factory, native.Config{Clock: clock, InitialPrompt: "do it"})
	passRestartDelay(ctx, t, clock, restart, 2*time.Second)
	require.Eventually(t, func() bool { return len(factory.Last().Turns()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "do it", factory.Last().Turns()[0].Text)
	assert.Equal(t, []string{"Failed to start the agent: claude: not found. AgentAPI starts it again in 2s (attempt 1 of 3)."}, rec.get().errors)
	assert.Equal(t, []int{1}, attempts(rec.get().exits))
	assert.Len(t, c.Messages(), 1)
}

// A failed restart a caller waits for isn't made again: the caller gets
// the error.
func TestFailedRestartIsNotMadeAgain(t *testing.T) {
	ctx := waitCtx(t)
	clock, _ := restartClock(t)
	boom := errors.New("boom")
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.StartErr = boom
		}
	}, native.Config{Clock: clock, ResumeRef: "old"})
	assert.ErrorIs(t, c.Restart(context.Background()), boom)
	assert.Equal(t, []native.AgentExit{{Err: boom}}, rec.get().exits)
	clock.Advance(time.Hour).MustWait(ctx)
	assert.Len(t, factory.Adapters(), 2)
}

// The count of restarts starts over once an agent ran five minutes.
func TestRestartsCountAgainAfterALongRun(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	c, factory, rec := start(t, nil, native.Config{Clock: clock})
	for _, delay := range []time.Duration{2 * time.Second, 10 * time.Second} {
		factory.Last().Exit(errors.New("crash"))
		passRestartDelay(ctx, t, clock, restart, delay)
		require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	}
	clock.Advance(5 * time.Minute).MustWait(ctx)
	factory.Last().Exit(errors.New("crash"))
	passRestartDelay(ctx, t, clock, restart, 2*time.Second)
	assert.Equal(t, []int{1, 2, 1}, attempts(rec.get().exits))
}

// An agent that can't resume its session starts a new one; the
// conversation stays.
func TestExitOfAnAgentThatCantResume(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		a.Caps.Resume = false
		a.Ref = "session-1"
	}, native.Config{Clock: clock})
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	factory.Last().Exit(nil)
	passRestartDelay(ctx, t, clock, restart, 2*time.Second)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Empty(t, factory.Last().ResumeRef)
	assert.Len(t, c.Messages(), 1)
	assert.Contains(t, rec.get().errors, "The agent exited. AgentAPI starts it again in a new session in 2s (attempt 1 of 3).")
}

// Closing the conversation cancels a restart it waits for.
func TestCloseCancelsARestart(t *testing.T) {
	ctx := waitCtx(t)
	clock, restart := restartClock(t)
	stopped := clock.Trap().TimerStop("Conversation", "restart")
	defer stopped.Close()
	c, factory, _ := start(t, nil, native.Config{Clock: clock})
	factory.Last().Exit(errors.New("crash"))
	restart.MustWait(ctx).Release()
	require.NoError(t, c.Close())
	stopped.MustWait(ctx).Release()
	clock.Advance(time.Hour).MustWait(ctx)
	assert.Len(t, factory.Adapters(), 1)
}
