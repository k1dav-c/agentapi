package native_test

import (
	"context"
	"fmt"
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

// waitCtx bounds how long a test waits for the conversation.
func waitCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// loginClock returns a mock clock and a trap that catches every wait before
// an agent that needs a login is started again.
func loginClock(t *testing.T) (*quartz.Mock, *quartz.Trap) {
	clock := quartz.NewMock(t)
	trap := clock.Trap().NewTimer("Conversation", "loginRetry")
	t.Cleanup(trap.Close)
	return clock, trap
}

func needsLogin(agent, command string) error {
	return &native.LoginRequiredError{Login: native.LoginRequired{Agent: agent, Message: agent + " is not logged in.", Command: command}}
}

func TestLoginRequiredAtStartWaitsAndRetries(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	var loggedIn atomic.Bool
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if !loggedIn.Load() {
			a.StartErr = needsLogin("Claude Code", "claude auth login")
		}
	}}
	c, rec := run(t, factory, native.Config{Clock: clock})
	retry.MustWait(ctx).Release()

	got := c.LoginRequired()
	require.NotNil(t, got)
	assert.Equal(t, "claude auth login", got.Command)
	assert.NotEmpty(t, got.Host)
	assert.NotEmpty(t, got.User)
	assert.Equal(t, conv.StatusInitializing, c.Status())
	select {
	case err := <-c.Exited():
		t.Fatalf("a missing login is not an exit: %v", err)
	default:
	}
	// Messages wait in the caller's queue.
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.Input{Text: "hi"}), conv.ErrMessageValidationChanging)
	errs := rec.get().errors
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "claude auth login")

	// Still not logged in after the interval: one more start, no second
	// error.
	_, w := clock.AdvanceNext()
	w.MustWait(ctx)
	retry.MustWait(ctx).Release()
	assert.Len(t, factory.Adapters(), 2)
	assert.NotNil(t, c.LoginRequired())

	loggedIn.Store(true)
	require.NoError(t, c.RetryLogin(ctx))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Nil(t, c.LoginRequired())
	logins := rec.get().logins
	assert.Nil(t, logins[len(logins)-1], "observers learn the login is fixed")
	assert.Len(t, rec.get().errors, 1)
	assert.ErrorIs(t, c.RetryLogin(ctx), native.ErrNoLoginRequired)
}

func TestLoginWarningSaysWhere(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) { a.StartErr = needsLogin("Codex", "codex login") }}
	c, rec := run(t, factory, native.Config{Clock: clock})
	retry.MustWait(ctx).Release()

	login := c.LoginRequired()
	require.NotNil(t, login)
	require.NotEmpty(t, login.Host)
	require.NotEmpty(t, login.User)
	errs := rec.get().errors
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], fmt.Sprintf("Run `codex login` in a terminal on %s as %s;", login.Host, login.User))

	// The next start finds the same; observers don't hear about it again.
	_, w := clock.AdvanceNext()
	w.MustWait(ctx)
	retry.MustWait(ctx).Release()
	assert.Len(t, factory.Adapters(), 2)
	assert.Len(t, rec.get().logins, 1)
	assert.Len(t, rec.get().errors, 1)
}

func TestLoginLostWhileRunningResumesOnRetry(t *testing.T) {
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef == "" {
			a.Ref = "session-1"
		}
	}, native.Config{})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().LoginRequired(native.LoginRequired{Agent: "Codex", Message: "Codex's login expired.", Command: "codex login"})
	first.Sink().TurnCompleted(native.TurnResult{Error: "401 Unauthorized"})
	require.NotNil(t, c.LoginRequired())

	require.NoError(t, c.RetryLogin(context.Background()))
	require.Eventually(t, func() bool { return len(factory.Adapters()) == 2 && c.Idle() }, 5*time.Second, time.Millisecond)
	second := factory.Last()
	assert.True(t, first.Closed())
	assert.Equal(t, "session-1", second.ResumeRef, "the session is resumed")
	assert.Len(t, c.Messages(), 1, "the conversation is kept")
	assert.Nil(t, c.LoginRequired())
}

func TestRetryLoginStartsNewAgentMessages(t *testing.T) {
	c, factory, _ := start(t, func(a *nativetest.Adapter) { a.Ref = "session-1" }, native.Config{})
	first := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	first.Sink().RichMessage(text("m1", "first reply"))
	first.Sink().LoginRequired(native.LoginRequired{Agent: "Codex", Message: "Codex's login expired.", Command: "codex login"})
	first.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonError, Error: "401 Unauthorized"})
	require.NoError(t, c.RetryLogin(context.Background()))
	require.Eventually(t, func() bool { return len(factory.Adapters()) == 2 && c.Idle() }, 5*time.Second, time.Millisecond)

	// The new agent numbers its messages from the start again.
	second := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "two"}))
	second.Sink().RichMessage(text("m1", "second reply"))
	msgs := c.Messages()
	require.Len(t, msgs, 4)
	assert.Equal(t, "first reply", msgs[1].Message)
	assert.Equal(t, "second reply", msgs[3].Message)
}

func TestLoginClearedByAWorkingTurn(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().LoginRequired(native.LoginRequired{Agent: "Pi", Message: "No API key.", Command: "pi"})
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonError, Error: "no api key"})
	require.NotNil(t, c.LoginRequired())

	// Turns that didn't need the model prove nothing.
	for _, reason := range []string{native.StopReasonInterrupted, native.StopReasonHandled} {
		require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: reason}))
		a.Sink().TurnCompleted(native.TurnResult{StopReason: reason})
		assert.NotNil(t, c.LoginRequired(), reason)
	}

	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "two"}))
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	assert.Nil(t, c.LoginRequired())
}

func TestLoginRetriesDontRepeatStartWarnings(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		a.StartWarnings = []string{"Codex could not find bubblewrap on PATH."}
		a.StartErr = needsLogin("Codex", "codex login")
	}}
	c, rec := run(t, factory, native.Config{Clock: clock})
	retry.MustWait(ctx).Release()
	for range 3 {
		require.NoError(t, c.RetryLogin(ctx))
		retry.MustWait(ctx).Release()
	}
	assert.Len(t, factory.Adapters(), 4)
	errs := rec.get().errors
	require.Len(t, errs, 2, "the first attempt's warning and the login, once each: %q", errs)
	assert.Contains(t, errs[0], "bubblewrap")
	assert.Contains(t, errs[1], "codex login")
}

func TestLoginWaitShowsNewStartWarnings(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	var loggedIn atomic.Bool
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		a.StartWarnings = []string{"Codex could not find bubblewrap on PATH."}
		if loggedIn.Load() {
			a.StartWarnings = append(a.StartWarnings, "Could not resume Codex thread t1; starting a new thread.")
		} else {
			a.StartErr = needsLogin("Codex", "codex login")
		}
	}}
	c, rec := run(t, factory, native.Config{Clock: clock, ResumeRef: "t1"})
	retry.MustWait(ctx).Release()

	// The user logs in while AgentAPI waits; the start that works has news.
	loggedIn.Store(true)
	require.NoError(t, c.RetryLogin(ctx))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	errs := rec.get().errors
	require.Len(t, errs, 3, "%q", errs)
	assert.Contains(t, errs[0], "bubblewrap")
	assert.Contains(t, errs[1], "codex login")
	assert.Contains(t, errs[2], "Could not resume Codex thread")
}

func TestRestartAfterLostLoginShowsWarnings(t *testing.T) {
	c, factory, rec := start(t, func(a *nativetest.Adapter) {
		if a.ResumeRef != "" {
			a.StartWarnings = []string{"Could not resume the thread; starting a new one."}
		}
		a.Ref = "session-1"
	}, native.Config{})
	a := factory.Last()
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "one"}))
	a.Sink().LoginRequired(native.LoginRequired{Agent: "Codex", Message: "Codex is not logged in.", Command: "codex login"})
	a.Sink().TurnCompleted(native.TurnResult{Error: "401 Unauthorized"})
	require.NoError(t, c.RetryLogin(context.Background()))
	require.Eventually(t, func() bool { return len(factory.Adapters()) == 2 && c.Idle() }, 5*time.Second, time.Millisecond)
	errs := rec.get().errors
	assert.Contains(t, errs[len(errs)-1], "Could not resume", "the restarted agent's warnings show")
}

func TestRestartEndsTheLoginWait(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	stop := clock.Trap().TimerStop("Conversation", "loginRetry")
	t.Cleanup(stop.Close)
	var loggedIn atomic.Bool
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		if !loggedIn.Load() {
			a.StartErr = needsLogin("Codex", "codex login")
		}
	}}
	c, _ := run(t, factory, native.Config{Clock: clock})
	retry.MustWait(ctx).Release()

	// A restart ends the first wait, and the new agent has no login either.
	require.NoError(t, c.Restart(ctx))
	stop.MustWait(ctx).Release()
	retry.MustWait(ctx).Release()

	// Checking again ends the second wait.
	loggedIn.Store(true)
	require.NoError(t, c.RetryLogin(ctx))
	stop.MustWait(ctx).Release()
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Len(t, factory.Adapters(), 3)
	_, waiting := clock.Peek()
	assert.False(t, waiting, "no wait is left")
}

func TestRetryLoginDuringAStartIsNotQueued(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	release := make(chan struct{})
	var mu sync.Mutex
	var starts []time.Time
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		mu.Lock()
		defer mu.Unlock()
		starts = append(starts, clock.Now())
		if len(starts) == 2 {
			a.StartBlock = release
		}
		if len(starts) < 3 {
			a.StartErr = needsLogin("Codex", "codex login")
		}
	}}
	c, _ := run(t, factory, native.Config{Clock: clock, LoginRetryInterval: time.Minute})
	retry.MustWait(ctx).Release()
	require.NoError(t, c.RetryLogin(ctx))
	require.Eventually(t, func() bool { return len(factory.Adapters()) == 2 && factory.Last().Sink() != nil }, 5*time.Second, time.Millisecond)

	// Checking again while a start is under way changes nothing: that start
	// is the check.
	require.NoError(t, c.RetryLogin(ctx))
	close(release)
	retry.MustWait(ctx).Release()
	clock.Advance(time.Minute).MustWait(ctx)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, starts, 3)
	assert.Equal(t, time.Minute, starts[2].Sub(starts[1]), "the third start waited the whole interval")
}

func TestStartThatFoundNoLoginIsIgnored(t *testing.T) {
	ctx := waitCtx(t)
	clock, retry := loginClock(t)
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) { a.StartErr = needsLogin("Codex", "codex login") }}
	c, rec := run(t, factory, native.Config{Clock: clock})
	retry.MustWait(ctx).Release()

	// The closed adapter still says something while AgentAPI waits.
	first := factory.Last()
	first.Sink().Error("Codex exited", conv.ErrorLevelError)
	first.Sink().RichMessage(text("m1", "stale"))
	assert.Len(t, rec.get().errors, 1, "only the login")
	assert.Empty(t, c.Messages())
}
