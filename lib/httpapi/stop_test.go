package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
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
		AgentType:      instanceapi.AgentTypeClaude,
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
