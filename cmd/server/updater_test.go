package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentUpdater(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the agent CLI")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The update lock goes in the cache directory.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	require.Nil(t, agentUpdater(AgentTypeClaude, "claude", logger))
	// Codex-type wrappers aren't the Codex CLI and have no update command.
	require.Nil(t, agentUpdater(AgentTypeCodex, "python3", logger))

	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	program := filepath.Join(dir, "codex")
	script := "#!/bin/sh\necho \"$* $CODEX_NON_INTERACTIVE [$AGENTAPI_API_TOKEN] [$CLAUDECODE]\" > " + record + "\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))

	// The update gets the agent's environment: not AgentAPI's token, not
	// the variables of an agent session AgentAPI runs in.
	t.Setenv("AGENTAPI_API_TOKEN", "secret")
	t.Setenv("CLAUDECODE", "1")
	agentUpdater(AgentTypeCodex, program, logger)(context.Background())
	got, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, "update 1 [] []\n", string(got))

	// A failing update must not panic or block the restart.
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 3\n"), 0o755))
	agentUpdater(AgentTypeCodex, program, logger)(context.Background())
	agentUpdater(AgentTypeCodex, filepath.Join(dir, "missing", "codex"), logger)(context.Background())

	// Pi updates itself only, not its installed packages.
	require.Nil(t, agentUpdater(AgentTypePi, "pi-wrapper", logger))
	pi := filepath.Join(dir, "pi")
	require.NoError(t, os.WriteFile(pi, []byte("#!/bin/sh\necho \"$*\" > "+record+"\n"), 0o755))
	agentUpdater(AgentTypePi, pi, logger)(context.Background())
	got, err = os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, "update --self\n", string(got))
}

// An update whose children outlive it, or that hangs, doesn't hold the
// restart: the update runs in its own process group, which is killed when
// the timeout ends, and output a child holds open isn't waited for.
func TestAgentUpdaterDoesntHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the agent CLI")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	program := filepath.Join(dir, "codex")

	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nsleep 300 &\necho updated\n"), 0o755))
	start := time.Now()
	agentUpdater(AgentTypeCodex, program, logger)(context.Background())
	require.Less(t, time.Since(start), 5*time.Second, "a child holding the output")

	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nsleep 300 &\nsleep 300\n"), 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start = time.Now()
	agentUpdater(AgentTypeCodex, program, logger)(ctx)
	require.Less(t, time.Since(start), 5*time.Second, "an update that hangs")
}

// The AgentAPIs that run one agent CLI update it one at a time.
func TestAgentUpdaterTakesTurns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the agent CLI")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	program := filepath.Join(dir, "pi")
	script := "#!/bin/sh\necho start >> " + record + "\nsleep 0.3\necho end >> " + record + "\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agentUpdater(AgentTypePi, program, logger)(context.Background())
		}()
	}
	wg.Wait()
	got, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("start\nend\n", 3), string(got))
}
