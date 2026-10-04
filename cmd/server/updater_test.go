package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentUpdater(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the agent CLI")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	require.Nil(t, agentUpdater(AgentTypeClaude, "claude", logger))

	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	program := filepath.Join(dir, "codex")
	script := "#!/bin/sh\necho \"$* $CODEX_NON_INTERACTIVE\" > " + record + "\n"
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))

	agentUpdater(AgentTypeCodex, program, logger)(context.Background())
	got, err := os.ReadFile(record)
	require.NoError(t, err)
	require.Equal(t, "update 1\n", string(got))

	// A failing update must not panic or block the restart.
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 3\n"), 0o755))
	agentUpdater(AgentTypeCodex, program, logger)(context.Background())
	agentUpdater(AgentTypeCodex, filepath.Join(dir, "missing"), logger)(context.Background())
}
