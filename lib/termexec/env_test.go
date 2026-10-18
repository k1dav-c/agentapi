//go:build unix

package termexec

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
)

func TestStartProcessEnv(t *testing.T) {
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := StartProcess(ctx, StartProcessConfig{
		Program:        "/bin/sh",
		Args:           []string{"-c", `echo "[$GREETING][$SECRET][$TERM]"; sleep 5`},
		TerminalWidth:  80,
		TerminalHeight: 10,
		Env:            []string{"PATH=" + os.Getenv("PATH"), "GREETING=hi"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close(slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second) })
	require.Eventually(t, func() bool {
		return strings.Contains(p.ReadScreen(), "[hi][][vt100]")
	}, 5*time.Second, 20*time.Millisecond, "the process gets Env and TERM, nothing else")
}
