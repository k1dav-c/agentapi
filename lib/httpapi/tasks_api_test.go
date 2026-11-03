package httpapi

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
)

// echoTerminal is a terminal agent that shows what is typed and, on Enter,
// starts on it.
type echoTerminal struct {
	mu     sync.Mutex
	screen string
}

func (a *echoTerminal) ReadScreen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.screen
}

func (a *echoTerminal) Write(data []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if text := string(data); text == "\r" {
		a.screen += "\nOn it."
	} else {
		a.screen += strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "").Replace(text)
	}
	return len(data), nil
}

// The PTY transport reports the agent running as soon as a message waits to
// be typed, and the message once it was: one sent in the grace period
// starts a task all the same.
func TestPTYMessageInGracePeriod(t *testing.T) {
	t.Parallel()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      mf.AgentTypeCustom,
		Transport:      TransportPTY,
		PTY:            PTYConfig{AgentIO: &echoTerminal{screen: "$ "}, TerminalColumns: 80},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	stable := func() bool { return srv.emitter.StatusSnapshot().Status == AgentStatusStable }
	// The screen counts as stable after 2 seconds.
	require.Eventually(t, stable, 10*time.Second, 10*time.Millisecond)

	sendUserMessage(t, srv, "Rebuild the release")
	// The agent waits on an approval, and is interrupted. A terminal agent
	// has no requests, and the screen changes as a dialog goes, so the
	// grace period comes from a request here.
	approval := native.RequestState{Request: native.Request{ID: "perm-1", Kind: native.RequestKindApproval}, Status: native.RequestStatusPending}
	srv.emitter.EmitRequest(approval)
	require.Eventually(t, stable, 10*time.Second, 10*time.Millisecond)
	approval.Status = native.RequestStatusWithdrawn
	srv.emitter.EmitRequest(approval)

	require.False(t, sendUserMessage(t, srv, "Ask me which checks to run").Body.Queued)
	require.Eventually(t, func() bool { return len(srv.emitter.Tasks()) == 2 }, 10*time.Second, 10*time.Millisecond, "the message starts a task")
	tasks := srv.emitter.Tasks()
	assert.Equal(t, TaskStatusCompleted, tasks[0].Status)
	assert.Empty(t, tasks[0].Steered)
	assert.Equal(t, "Ask me which checks to run", tasks[1].Prompt)
	assert.Equal(t, TaskStatusRunning, tasks[1].Status)
}
