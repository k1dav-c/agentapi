package statedb

import (
	"bytes"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// A row Load can't decode is skipped, and logged: the server still starts
// with the rest of the conversation.
func TestLoadSkipsRowsItCantRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path, quiet())
	require.NoError(t, err)
	db.PutMessage(st.ConversationMessage{Id: 0, Message: "kept"}, 1)
	db.PutRequest(native.RequestState{Request: native.Request{ID: "r1"}, Status: native.RequestStatusResolved}, 2)
	db.Flush()
	for _, query := range []string{
		`INSERT INTO messages (id, seq, data) VALUES (1, 3, '{"id": "not a number"}')`,
		`INSERT INTO messages (id, seq, data) VALUES (2, 4, 'not JSON')`,
		`INSERT INTO requests (id, pos, seq, data) VALUES ('r2', 1, 5, '{')`,
	} {
		_, err := db.db.Exec(query)
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())

	var logs bytes.Buffer
	db, err = Open(path, slog.New(slog.NewTextHandler(&logs, nil)))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	require.Len(t, snap.Messages, 1)
	assert.Equal(t, "kept", snap.Messages[0].Message.Message)
	require.Len(t, snap.Requests, 1)
	assert.Equal(t, "r1", snap.Requests[0].State.ID)
	assert.Contains(t, logs.String(), "table=messages rows=2")
	assert.Contains(t, logs.String(), "table=requests rows=1")
}

func TestMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	sqlDB, err := sql.Open("sqlite", dataSourceName(path))
	require.NoError(t, err)
	version := func() int {
		var v int
		require.NoError(t, sqlDB.QueryRow("PRAGMA user_version").Scan(&v))
		return v
	}
	require.NoError(t, migrate(sqlDB))
	assert.Equal(t, len(migrations), version())
	require.NoError(t, migrate(sqlDB), "a migrated database is left as it is")
	assert.Equal(t, len(migrations), version())

	_, err = sqlDB.Exec("PRAGMA user_version = 99")
	require.NoError(t, err)
	assert.ErrorContains(t, migrate(sqlDB), "newer AgentAPI (schema 99")
	require.NoError(t, sqlDB.Close())
}
