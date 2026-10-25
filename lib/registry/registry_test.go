package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndList(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "instances")
	hostname, _ := os.Hostname()
	start := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	later := Entry{ID: "host:3285", ServerID: "b2", URL: "http://localhost:3285", Hostname: hostname, Port: 3285, PID: os.Getpid(), StartedAt: start.Add(time.Minute)}
	first := Entry{ID: "host:3284", Name: "api", ServerID: "a1", URL: "http://localhost:3284", Hostname: hostname, Port: 3284, PID: os.Getpid(), AuthRequired: true, StartedAt: start}
	path, err := Write(dir, later)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "b2.json"), path)
	_, err = Write(dir, first)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	// Files that aren't entries are left alone.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600))

	entries, err := List(dir)
	require.NoError(t, err)
	assert.Equal(t, []Entry{first, later}, entries, "the earliest started first")

	// Writing again replaces the entry.
	first.Name = "renamed"
	_, err = Write(dir, first)
	require.NoError(t, err)
	entries, err = List(dir)
	require.NoError(t, err)
	assert.Equal(t, "renamed", entries[0].Name)
}

// An entry whose process is gone is removed; one of another machine is
// kept.
func TestListRemovesStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hostname, _ := os.Hostname()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	gone := cmd.Process.Pid
	_, err := Write(dir, Entry{ServerID: "gone", Hostname: hostname, PID: gone})
	require.NoError(t, err)
	_, err = Write(dir, Entry{ServerID: "remote", Hostname: "elsewhere", PID: gone})
	require.NoError(t, err)

	entries, err := List(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "remote", entries[0].ServerID)
	assert.NoFileExists(t, filepath.Join(dir, "gone.json"))
}

func TestListMissingDir(t *testing.T) {
	t.Parallel()
	entries, err := List(filepath.Join(t.TempDir(), "none"))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestFileName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ab_c____d.json", fileName("ab/c:../d"))
}
