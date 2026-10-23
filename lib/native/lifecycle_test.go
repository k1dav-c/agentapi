package native_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

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
	select {
	case err := <-c.Exited():
		t.Fatalf("unexpected exit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
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
	// reported all the same.
	cancel()
	assert.ErrorIs(t, <-restarted, context.Canceled)
	close(release)
	select {
	case err := <-c.Exited():
		assert.ErrorIs(t, err, boom)
	case <-time.After(5 * time.Second):
		t.Fatal("the failed start was not reported")
	}
	assert.Contains(t, rec.get().errors, "Failed to start the agent: boom")
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
			select {
			case err := <-c.Exited():
				t.Fatalf("unexpected exit: %v", err)
			default:
			}
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

func TestExitIsReported(t *testing.T) {
	c, factory, _ := start(t, nil, native.Config{})
	boom := errors.New("crashed")
	factory.Last().Exit(boom)
	select {
	case err := <-c.Exited():
		assert.ErrorIs(t, err, boom)
	case <-time.After(5 * time.Second):
		t.Fatal("exit not reported")
	}
	assert.Equal(t, conv.StatusInitializing, c.Status())
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

	require.NoError(t, c.Send(conv.MessagePartText{Content: "hi"}))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []bool{true, true}, deadlines, "an agent that hangs can't hold its caller forever")
}

func TestStartFailure(t *testing.T) {
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) { a.StartErr = errors.New("no login") }}
	c, rec := run(t, factory, native.Config{})
	select {
	case err := <-c.Exited():
		assert.ErrorContains(t, err, "no login")
	case <-time.After(5 * time.Second):
		t.Fatal("start failure not reported")
	}
	assert.Equal(t, []string{"Failed to start the agent: no login"}, rec.get().errors)
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
