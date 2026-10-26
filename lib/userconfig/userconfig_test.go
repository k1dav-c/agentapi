package userconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAndSave(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agentapi", "config.yaml")
	config, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, config, "a missing file")

	config["webhook-url"] = "http://hub:8080/hooks"
	config["webhook-events"] = []string{"instance.*", "task.*"}
	require.NoError(t, Save(path, config))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	loaded, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://hub:8080/hooks", loaded["webhook-url"])
	assert.Equal(t, []any{"instance.*", "task.*"}, loaded["webhook-events"])

	require.NoError(t, os.WriteFile(path, []byte("webhook-url: [\n"), 0o600))
	_, err = Load(path)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	empty, err := Load(path)
	require.NoError(t, err)
	assert.NotNil(t, empty)
}
