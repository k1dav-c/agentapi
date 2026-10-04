package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithCodexReasoningSummary(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.toml")
	auto := []string{"-c", `model_reasoning_summary="auto"`}

	require.Equal(t, auto, withCodexReasoningSummary(nil, missing))
	require.Equal(t, append(auto, "resume", "--last"), withCodexReasoningSummary([]string{"resume", "--last"}, missing))

	// The user's own choice on the command line wins.
	explicit := []string{"-c", `model_reasoning_summary="none"`}
	require.Equal(t, explicit, withCodexReasoningSummary(explicit, missing))

	write := func(content string) string {
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}
	// ...and so does their config, top level or in a profile.
	require.Equal(t, []string{"x"}, withCodexReasoningSummary([]string{"x"}, write("model = \"o3\"\nmodel_reasoning_summary = \"none\"\n")))
	require.Equal(t, []string{"x"}, withCodexReasoningSummary([]string{"x"}, write("[profiles.fast]\n  model_reasoning_summary=\"concise\"\n")))
	// Comments and other keys don't count.
	require.Equal(t, append(auto, "x"), withCodexReasoningSummary([]string{"x"}, write("# model_reasoning_summary = \"none\"\nmodel_reasoning_effort = \"high\"\n")))
}
