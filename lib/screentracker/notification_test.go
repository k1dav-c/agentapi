package screentracker_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

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

func TestNotificationSnapshots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &notifyingAgent{updates: make(chan struct{}, 1)}
	a.screen = "ready"
	a.onWrite = func(data []byte) { a.screen += string(data) + "reply" }
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		AgentIO: a, SnapshotInterval: 10 * time.Millisecond,
		ScreenStabilityLength: 30 * time.Millisecond, Logger: slog.Default(),
	}, nil)
	c.Start(ctx)
	stable := func() {
		require.Eventually(t, func() bool { return c.Status() == st.ConversationStatusStable }, 3*time.Second, 5*time.Millisecond)
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
	require.Eventually(t, func() bool { return c.Text() == "new output" }, time.Second, 5*time.Millisecond)
	stable()
	idle()
	// Send must wake a sleeping snapshot loop even when Write emits no event.
	sent := make(chan error, 1)
	go func() { sent <- c.Send(st.MessagePartText{Content: "hello"}) }()
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
