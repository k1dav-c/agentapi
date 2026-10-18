package native_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	st "github.com/coder/agentapi/lib/screentracker"
)

// The conversation calls its adapter with its lock released (see
// native.Sink): every call of the fake adapter checks that the lock is
// free, through all the operations that reach the adapter.
func TestConversationCallsTheAdapterUnlocked(t *testing.T) {
	t.Parallel()
	var conv atomic.Pointer[native.Conversation]
	var mu sync.Mutex
	called := map[string]bool{}
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		configurable(native.Settings{Model: "fast"})(a)
		a.Caps.Compact = true
		a.OnCall = func(method string) {
			mu.Lock()
			called[method] = true
			mu.Unlock()
			if !nativetest.Returns(func() { conv.Load().Status() }) {
				t.Errorf("the conversation holds its lock while it calls %s", method)
			}
		}
	}}
	c := native.NewConversation(native.Config{Factory: factory.New})
	conv.Store(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	require.Eventually(t, func() bool { return c.Status() != st.ConversationStatusInitializing }, 5*time.Second, time.Millisecond)

	require.NoError(t, c.StartTurn(ctx, native.Input{Text: "one"}))
	require.NoError(t, c.Steer(ctx, native.Input{Text: "two"}))
	factory.Last().Sink().RequestOpened(approval("r1"))
	_, err := c.Respond(ctx, "r1", native.Response{OptionID: "allow"})
	require.NoError(t, err)
	require.NoError(t, c.Interrupt(ctx))
	factory.Last().Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonInterrupted})
	_, err = c.SettingOptions(ctx)
	require.NoError(t, err)
	require.NoError(t, c.ApplySettings(ctx, native.Settings{Effort: "high"}))
	require.NoError(t, c.Compact(ctx))
	require.NoError(t, c.Restart(ctx))
	require.NoError(t, c.Close())

	mu.Lock()
	defer mu.Unlock()
	for _, method := range []string{"Start", "Capabilities", "Settings", "Wait", "StartTurn", "Steer", "Respond", "Interrupt", "SettingOptions", "ApplySettings", "Compact", "Close"} {
		assert.True(t, called[method], "the test reaches %s", method)
	}
}
