package statedb_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/transcript"
)

func open(t *testing.T, path string) *statedb.DB {
	t.Helper()
	db, err := statedb.Open(path, nil)
	require.NoError(t, err)
	return db
}

func msg(id int, text string) st.ConversationMessage {
	return st.ConversationMessage{Id: id, Message: text, Role: st.ConversationRoleUser, Time: time.Unix(1700000000, 0).UTC()}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Empty())

	db.SetMeta(statedb.MetaAgentType, "claude")
	db.SetSeq("epoch-1", 7)
	db.PutMessage(msg(0, "a"), 1)
	db.PutMessage(msg(1, "b"), 2)
	db.PutMessage(msg(1, "b2"), 3) // replaces the pending write
	db.PutRichMessage(0, transcript.RichMessage{MessageID: "m1", Role: "assistant"}, 4)
	db.AppendSessionEvents([]transcript.SessionEvent{{EventID: 1, Kind: "text"}, {EventID: 2, Kind: "tool_call"}})
	db.PutRequest(native.RequestState{Request: native.Request{ID: "r2", Kind: native.RequestKindApproval}, Status: native.RequestStatusPending}, 5)
	db.PutRequest(native.RequestState{Request: native.Request{ID: "r1", Kind: native.RequestKindQuestion}, Status: native.RequestStatusPending}, 6)
	db.PutRequest(native.RequestState{Request: native.Request{ID: "r2", Kind: native.RequestKindApproval}, Status: native.RequestStatusResolved}, 7)
	db.PutQueue([]statedb.QueuedMessage{{ID: 3, Content: "later"}}, 3)
	require.NoError(t, db.Close())

	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err = db.Load()
	require.NoError(t, err)
	assert.False(t, snap.Empty())
	assert.Equal(t, "claude", snap.Meta[statedb.MetaAgentType])
	assert.Equal(t, "epoch-1", snap.Meta[statedb.MetaEpoch])
	assert.Equal(t, uint64(7), snap.Seq())
	assert.Equal(t, 3, snap.NextQueueID())
	require.Len(t, snap.Messages, 2)
	assert.Equal(t, "b2", snap.Messages[1].Message.Message)
	assert.Equal(t, uint64(3), snap.Messages[1].Seq)
	assert.Equal(t, msg(0, "a"), snap.Messages[0].Message)
	require.Len(t, snap.RichMessages, 1)
	assert.Equal(t, "m1", snap.RichMessages[0].Message.MessageID)
	require.Len(t, snap.Events, 2)
	assert.Equal(t, "tool_call", snap.Events[1].Kind)
	require.Len(t, snap.Requests, 2)
	// Requests keep the order they were first stored in.
	assert.Equal(t, "r2", snap.Requests[0].State.ID)
	assert.Equal(t, native.RequestStatusResolved, snap.Requests[0].State.Status)
	assert.Equal(t, uint64(7), snap.Requests[0].Seq)
	assert.Equal(t, "r1", snap.Requests[1].State.ID)
	assert.Equal(t, []statedb.QueuedMessage{{ID: 3, Content: "later"}}, snap.Queue)
}

func TestDeleteIsABarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.PutMessage(msg(0, "a"), 1)
	db.PutMessage(msg(1, "old"), 2)
	db.TruncateMessages(1)
	// Written after the truncation even though a write with the same key
	// was pending before it.
	db.PutMessage(msg(1, "new"), 3)
	db.Flush()
	snap, err := db.Load()
	require.NoError(t, err)
	require.Len(t, snap.Messages, 2)
	assert.Equal(t, "new", snap.Messages[1].Message.Message)

	db.TruncateMessages(1)
	db.Flush()
	snap, err = db.Load()
	require.NoError(t, err)
	require.Len(t, snap.Messages, 1)
	require.NoError(t, db.Close())
}

func TestReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.SetMeta(statedb.MetaAgentType, "codex")
	db.SetMeta(statedb.MetaSessionRef, "thread-1")
	db.PutMessage(msg(0, "a"), 1)
	db.Flush()
	db.PutMessage(msg(1, "pending"), 2)
	db.PutQueue([]statedb.QueuedMessage{{ID: 1, Content: "q"}}, 1)
	db.Reset("epoch-2")
	db.PutMessage(msg(0, "fresh"), 1)
	require.NoError(t, db.Close())

	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	assert.Equal(t, "codex", snap.Meta[statedb.MetaAgentType])
	assert.Empty(t, snap.Meta[statedb.MetaSessionRef])
	assert.Equal(t, "epoch-2", snap.Meta[statedb.MetaEpoch])
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, "fresh", snap.Messages[0].Message.Message)
	assert.Len(t, snap.Queue, 1)
}

func TestWritesAfterCloseAreIgnored(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, db.Close())
	db.PutMessage(msg(0, "late"), 1)
	db.Flush()
	require.NoError(t, db.Close())
}

func TestOpenCreatesPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are a Unix concept")
	}
	// A missing directory is created, and characters with a meaning in a
	// URI are taken literally.
	dir := filepath.Join(t.TempDir(), "agentapi")
	path := filepath.Join(dir, "a?b#c%20d.db")
	db := open(t, path)
	db.PutMessage(msg(0, "secret"), 1)
	db.Flush()
	for file, want := range map[string]os.FileMode{dir: 0o700, path: 0o600, path + "-wal": 0o600} {
		info, err := os.Stat(file)
		require.NoError(t, err)
		assert.Equal(t, want, info.Mode().Perm(), file)
	}
	assert.NoFileExists(t, path+"-shm", "the WAL index stays in memory")
	require.NoError(t, db.Close())

	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	require.Len(t, snap.Messages, 1)
}

func TestOpenRefusesAFileInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	_, err := statedb.Open(path, nil)
	require.ErrorContains(t, err, "state database "+path+" is in use by another AgentAPI")
	require.NoError(t, db.Close())

	db = open(t, path)
	require.NoError(t, db.Close())
}

// copyDB copies the database files as they are on disk, like a crash would
// leave them.
func copyDB(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(from + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(to+suffix, data, 0o600))
	}
}

func TestCleanClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db := open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Empty())
	db.SetSeq("epoch-1", 1)
	db.PutMessage(msg(0, "a"), 1)
	require.NoError(t, db.Close())

	db = open(t, path)
	snap, err = db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Clean)
	assert.NotContains(t, snap.Meta, "clean", "the flag is not meta of the conversation")
	db.PutMessage(msg(1, "b"), 2)
	db.Flush()
	// A crash while the file is open: what was written survives, but the
	// file isn't clean.
	crashed := filepath.Join(dir, "crashed.db")
	copyDB(t, path, crashed)
	require.NoError(t, db.Close())

	db = open(t, crashed)
	defer func() { _ = db.Close() }()
	snap, err = db.Load()
	require.NoError(t, err)
	assert.False(t, snap.Clean)
	assert.Len(t, snap.Messages, 2)
}
