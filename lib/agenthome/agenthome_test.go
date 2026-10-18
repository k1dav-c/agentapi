package agenthome_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agenthome"
)

func TestDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, tc := range []struct {
		env, def string
		get      func() (string, error)
	}{
		{"CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"), agenthome.ClaudeConfigDir},
		{"CODEX_HOME", filepath.Join(home, ".codex"), agenthome.CodexHome},
		{"PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"), agenthome.PiAgentDir},
	} {
		t.Setenv(tc.env, "")
		got, err := tc.get()
		require.NoError(t, err)
		require.Equal(t, tc.def, got, tc.env)
		t.Setenv(tc.env, "/elsewhere")
		got, err = tc.get()
		require.NoError(t, err)
		require.Equal(t, "/elsewhere", got, tc.env)
	}
}
