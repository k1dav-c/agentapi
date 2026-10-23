package httpapi

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
)

func quietContext() context.Context {
	return logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Stop waits for the queue loop to end: a tick under way could still send
// a message after the queue was stored, which a restart would send again.
func TestStopWaitsForTheQueueLoop(t *testing.T) {
	clock := quartz.NewMock(t)
	trap := clock.Trap().TickerFuncWait("messageQueueDispatch")
	defer trap.Close()
	srv, err := NewServer(quietContext(), ServerConfig{
		AgentType:      mf.AgentTypeClaude,
		Transport:      TransportNative,
		Native:         NativeConfig{Factory: (&nativetest.Factory{}).New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Clock:          clock,
	})
	require.NoError(t, err)
	stopped := make(chan error, 1)
	go func() { stopped <- srv.Stop(context.Background()) }()
	call := trap.MustWait(t.Context())
	select {
	case <-stopped:
		t.Fatal("Stop returned before the queue loop ended")
	default:
	}
	call.Release()
	require.NoError(t, <-stopped)
}

// Stop stops the session-log watcher of a terminal agent, which would
// otherwise go on changing the conversation of a stopped server.
func TestStopStopsTheSessionLogWatcher(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	srv, err := NewServer(quietContext(), ServerConfig{
		AgentType:      mf.AgentTypeClaude,
		Transport:      TransportPTY,
		PTY:            PTYConfig{AgentPID: os.Getpid()},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Clock:          quartz.NewMock(t),
	})
	require.NoError(t, err)
	require.NotNil(t, srv.jsonlWatcherCancel, "the watcher runs")
	watcher := srv.jsonlWatcherDone
	require.NoError(t, srv.Stop(context.Background()))
	assert.Nil(t, srv.jsonlWatcherCancel)
	assert.True(t, waitsFor(watcher, 5*time.Second), "the watcher still runs")
}

// waitsFor reports whether wg is done within timeout.
func waitsFor(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
