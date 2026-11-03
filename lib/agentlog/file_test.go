package agentlog

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file's creation time is read where the system records it (Linux's
// statx, macOS's stat), and stays when the file changes.
func TestBirthTime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	before := time.Now().Add(-time.Second)
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
	born, ok := BirthTime(path)
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		assert.False(t, ok)
		return
	}
	if !ok {
		t.Skip("the file system doesn't record creation times")
	}
	assert.False(t, born.Before(before), born)
	assert.False(t, born.After(time.Now().Add(time.Second)), born)

	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	again, ok := BirthTime(path)
	require.True(t, ok)
	assert.True(t, born.Equal(again), "changing the modification time keeps the creation time")

	_, ok = BirthTime(filepath.Join(t.TempDir(), "missing"))
	assert.False(t, ok)
}
