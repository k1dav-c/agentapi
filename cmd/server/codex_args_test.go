package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCodexCLI(t *testing.T) {
	for _, program := range []string{"codex", "/usr/local/bin/codex", "codex.exe"} {
		require.True(t, isCodexCLI(program), program)
	}
	for _, program := range []string{"python3", "codex-wrapper.sh", "/opt/codex/run", "npx"} {
		require.False(t, isCodexCLI(program), program)
	}
}
