package screentracker_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/stretchr/testify/require"
)

type notifyingAgent struct {
	testAgent
	updates chan struct{}
	reads   atomic.Int64
}

func (a *notifyingAgent) ScreenUpdates() <-chan struct{} { return a.updates }
func (a *notifyingAgent) ReadScreen() string             { a.reads.Add(1); return a.testAgent.ReadScreen() }
func (a *notifyingAgent) update(s string) {
	a.setScreen(s)
	select {
	case a.updates <- struct{}{}:
	default:
	}
}

// screenEmitter records the last screen a conversation emitted.
type screenEmitter struct {
	conv.NopEmitter
	screen atomic.Value
}

func (e *screenEmitter) EmitScreen(screen string) { e.screen.Store(screen) }

func (e *screenEmitter) last() string {
	s, _ := e.screen.Load().(string)
	return s
}

func TestNotificationSnapshots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &notifyingAgent{updates: make(chan struct{}, 1)}
	a.screen = "ready"
	a.onWrite = func(data []byte) { a.screen += string(data) + "reply" }
	emitter := &screenEmitter{}
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		AgentIO: a, SnapshotInterval: 10 * time.Millisecond,
		ScreenStabilityLength: 30 * time.Millisecond, Logger: slog.Default(),
	}, emitter)
	c.Start(ctx)
	stable := func() {
		require.Eventually(t, func() bool { return c.Status() == conv.StatusStable }, 3*time.Second, 5*time.Millisecond)
	}
	idle := func() {
		// Allow a pending notification to drain before checking sustained sleep.
		time.Sleep(80 * time.Millisecond)
		n := a.reads.Load()
		time.Sleep(100 * time.Millisecond)
		require.Equal(t, n, a.reads.Load(), "stable screens must stop polling")
	}
	stable()
	idle()
	a.update("new output")
	require.Eventually(t, func() bool { return emitter.last() == "new output" }, time.Second, 5*time.Millisecond)
	stable()
	idle()
	// Send must wake a sleeping snapshot loop even when Write emits no event.
	sent := make(chan error, 1)
	go func() { sent <- c.Send(conv.MessagePartText{Content: "hello"}) }()
	select {
	case err := <-sent:
		require.NoError(t, err)
	case <-time.After(4 * time.Second):
		t.Fatal("send did not wake the snapshot loop")
	}
	stable()
	idle()
	c.Reset()
	stable()
	idle()
	cancel()
	time.Sleep(30 * time.Millisecond)
	n := a.reads.Load()
	a.update("after cancellation")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, n, a.reads.Load())
}

// statusEmitter records the last status a conversation emitted.
type statusEmitter struct {
	conv.NopEmitter
	status atomic.Value
}

func (e *statusEmitter) EmitStatus(status conv.Status) { e.status.Store(status) }

func (e *statusEmitter) last() conv.Status {
	s, _ := e.status.Load().(conv.Status)
	return s
}

func TestReadyWhenStable(t *testing.T) {
	// A shell never shows an input box. Without ReadyWhenStable it never
	// counts as ready; with it, it is ready once its screen settles, and the
	// conversation says so although the settled snapshot loop sleeps.
	for _, readyWhenStable := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		a := &notifyingAgent{updates: make(chan struct{}, 1)}
		a.screen = "my-app $"
		emitter := &statusEmitter{}
		c := st.NewPTY(ctx, st.PTYConversationConfig{
			AgentIO: a, SnapshotInterval: 10 * time.Millisecond, ScreenStabilityLength: 30 * time.Millisecond,
			ReadyForInitialPrompt: func(string) bool { return false },
			ReadyWhenStable:       readyWhenStable,
			Logger:                slog.Default(),
		}, emitter)
		c.Start(ctx)
		if readyWhenStable {
			require.Eventually(t, func() bool { return emitter.last() == conv.StatusStable }, 3*time.Second, 5*time.Millisecond)
			require.Equal(t, conv.StatusStable, c.Status())
		} else {
			time.Sleep(200 * time.Millisecond)
			require.Equal(t, conv.StatusChanging, c.Status())
			require.Equal(t, conv.StatusChanging, emitter.last())
		}
		cancel()
	}
}
