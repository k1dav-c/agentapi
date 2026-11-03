//go:build unix && !aix

package filelock_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/filelock"
)

func TestLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := filelock.Lock(context.Background(), path)
	require.NoError(t, err)

	// flock locks are per open file, so a second open waits like another
	// process would.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = filelock.Lock(ctx, path)
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	unlock()
	unlock2, err := filelock.Lock(context.Background(), path)
	require.NoError(t, err)
	unlock2()
}
