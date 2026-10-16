package statedb_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/statedb"
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
	db.PutRichMessage(0, jsonlwatcher.RichMessage{MessageID: "m1", Role: "assistant"}, 4)
	db.AppendSessionEvents([]jsonlwatcher.SessionEvent{{EventID: 1, Kind: "text"}, {EventID: 2, Kind: "tool_call"}})
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
