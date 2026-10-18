package statedb

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	st "github.com/coder/agentapi/lib/screentracker"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestWriteRetriesAFailedBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path, quiet())
	require.NoError(t, err)
	db.OnWriteError(func(err error) { t.Errorf("reported %v, which went through on a retry", err) })
	failures := 2
	db.enqueue("flaky", func(*sql.Tx) error {
		if failures > 0 {
			failures--
			return errors.New("disk I/O error")
		}
		return nil
	})
	db.PutMessage(st.ConversationMessage{Id: 0, Message: "kept"}, 1)
	db.Flush()
	assert.Zero(t, failures, "the batch was tried until it went through")
	require.NoError(t, db.Close())

	db, err = Open(path, quiet())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Clean)
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, "kept", snap.Messages[0].Message.Message)
}

func TestWriteGivesUpAndReports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path, quiet())
	require.NoError(t, err)
	var mu sync.Mutex
	var reported []error
	db.OnWriteError(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	})
	broken := errors.New("disk full")
	attempts := 0
	db.enqueue("broken", func(*sql.Tx) error {
		attempts++
		return broken
	})
	db.PutMessage(st.ConversationMessage{Id: 0, Message: "lost"}, 1)
	db.Flush()
	assert.Equal(t, writeAttempts, attempts)
	// Another lost batch in the same outage is not reported again.
	db.enqueue("broken", func(*sql.Tx) error { return broken })
	db.Flush()
	mu.Lock()
	assert.Equal(t, []error{broken}, reported)
	mu.Unlock()
	// Writes that work again are kept, but the file misses changes.
	db.PutMessage(st.ConversationMessage{Id: 1, Message: "later"}, 2)
	err = db.Close()
	require.ErrorIs(t, err, broken)

	db, err = Open(path, quiet())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	assert.False(t, snap.Clean)
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, "later", snap.Messages[0].Message.Message)
}
