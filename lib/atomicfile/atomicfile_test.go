package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The data is on disk before it takes the file's name, and the name after.
func TestWriteSyncsTheFileThenTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	var synced []string
	oldFile, oldDir := syncFile, syncDir
	t.Cleanup(func() { syncFile, syncDir = oldFile, oldDir })
	syncFile = func(f *os.File) error {
		data, err := os.ReadFile(f.Name())
		require.NoError(t, err)
		assert.Equal(t, "new", string(data), "all of it written")
		assert.True(t, strings.HasPrefix(filepath.Base(f.Name()), ".config-"), f.Name())
		old, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "old", string(old), "not renamed yet")
		synced = append(synced, "file")
		return f.Sync()
	}
	syncDir = func(d string) {
		assert.Equal(t, dir, d)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "new", string(data), "renamed")
		synced = append(synced, "dir")
	}
	require.NoError(t, Write(path, ".config-*", []byte("new")))
	assert.Equal(t, []string{"file", "dir"}, synced)
}

func TestWriteKeepsTheFileItsOwnersAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
	require.NoError(t, Write(path, ".entry-*", []byte("{}\n")))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(data))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "entry.json", entries[0].Name())
}

// A failed sync leaves the file as it was.
func TestWriteFailedSync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))
	old := syncFile
	t.Cleanup(func() { syncFile = old })
	syncFile = func(*os.File) error { return os.ErrInvalid }
	require.ErrorIs(t, Write(path, ".entry-*", []byte("new")), os.ErrInvalid)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old", string(data))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the temporary file removed")
}
