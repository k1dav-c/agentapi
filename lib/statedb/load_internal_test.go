package statedb

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
)

// A row Load can't decode is skipped, and logged: the server still starts
// with the rest of the conversation.
func TestLoadSkipsRowsItCantRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path, quiet())
	require.NoError(t, err)
	db.PutMessage(conv.Message{Id: 0, Message: "kept"}, 1)
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
	defer func() { require.NoError(t, sqlDB.Close()) }()
	require.NoError(t, migrate(sqlDB))
	assert.Equal(t, len(migrations), schema(t, sqlDB))
	require.NoError(t, migrate(sqlDB), "a migrated database is left as it is")
	assert.Equal(t, len(migrations), schema(t, sqlDB))
	assert.Empty(t, metaOf(t, sqlDB), "every migration so far only added")
}

// An older AgentAPI goes on with a database a newer one migrated, unless a
// migration changed what the older writes.
func TestMigrateNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	sqlDB, err := sql.Open("sqlite", dataSourceName(path))
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()
	known := migrations
	defer func() { migrations = known }()
	added := migration{since: "9.0.0", minWriter: 1, sql: "CREATE TABLE later (id INTEGER PRIMARY KEY)"}
	changed := migration{since: "9.1.0", minWriter: len(known) + 2, sql: "ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''"}

	// A newer AgentAPI only added a table.
	migrations = append(slices.Clone(known), added)
	require.NoError(t, migrate(sqlDB))
	assert.Empty(t, metaOf(t, sqlDB))
	migrations = known
	require.NoError(t, migrate(sqlDB))
	assert.Equal(t, len(known)+1, schema(t, sqlDB), "the older one leaves the schema as it is")

	// A later one changed what the older ones write.
	migrations = append(slices.Clone(known), added, changed)
	require.NoError(t, migrate(sqlDB))
	assert.Equal(t, map[string]string{metaMinWriterSchema: strconv.Itoa(len(known) + 2), metaMinWriterVersion: "9.1.0"}, metaOf(t, sqlDB))
	migrations = append(slices.Clone(known), added)
	assert.ErrorContains(t, migrate(sqlDB), fmt.Sprintf("needs AgentAPI 9.1.0 or later (schema %d;", len(known)+2))
	_, err = sqlDB.Exec("INSERT INTO meta (key, value) VALUES (?, '9.1.3')", metaWriterVersion)
	require.NoError(t, err)
	assert.ErrorContains(t, migrate(sqlDB), "last written by AgentAPI 9.1.3")
	migrations = append(slices.Clone(known), added, changed)
	require.NoError(t, migrate(sqlDB))
}

func schema(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	require.NoError(t, db.QueryRow("PRAGMA user_version").Scan(&v))
	return v
}

// metaOf returns the meta values a migration records.
func metaOf(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	meta := map[string]string{}
	rows, err := db.Query("SELECT key, value FROM meta WHERE key IN (?, ?)", metaMinWriterSchema, metaMinWriterVersion)
	require.NoError(t, err)
	for rows.Next() {
		var k, v string
		require.NoError(t, rows.Scan(&k, &v))
		meta[k] = v
	}
	require.NoError(t, closeRows(rows))
	return meta
}
