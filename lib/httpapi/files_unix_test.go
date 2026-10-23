//go:build unix

package httpapi

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileContentOfAFIFO(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := (&fileAPI{cwd: dir}).getFileContent(t.Context(), &FilePathQuery{Path: "pipe"})
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
		assert.Equal(t, 400, statusOf(t, err))
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO waits for a writer")
	}
}
