package statedb

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
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

// Check tells whether a database would open, without changing it.
func TestCheck(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Check(filepath.Join(dir, "missing", "state.db")), "Open creates it")

	path := filepath.Join(dir, "state.db")
	db, err := Open(path, quiet())
	require.NoError(t, err)
	db.PutMessage(conv.Message{Id: 0, Message: "kept"}, 1)
	db.Flush()
	assert.ErrorIs(t, Check(path), ErrInUse)
	require.NoError(t, db.Close())
	require.NoError(t, Check(path))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no -wal or -shm file")

	// An older schema isn't migrated.
	older := filepath.Join(dir, "older.db")
	sqlDB, err := sql.Open("sqlite", dataSourceName(older))
	require.NoError(t, err)
	require.NoError(t, migrateTo(sqlDB, 1))
	require.NoError(t, sqlDB.Close())
	require.NoError(t, Check(older))
	sqlDB, err = sql.Open("sqlite", dataSourceName(older))
	require.NoError(t, err)
	assert.Equal(t, 1, schema(t, sqlDB))
	require.NoError(t, sqlDB.Close())

	// A newer AgentAPI's: one that only added is fine, one that changed
	// what this one writes isn't.
	known := migrations
	defer func() { migrations = known }()
	migrations = append(slices.Clone(known),
		migration{since: "9.0.0", minWriter: 1, sql: "CREATE TABLE later (id INTEGER PRIMARY KEY)"})
	db, err = Open(path, quiet())
	require.NoError(t, err)
	require.NoError(t, db.Close())
	migrations = known
	require.NoError(t, Check(path))
	migrations = append(slices.Clone(known),
		migration{since: "9.0.0", minWriter: 1, sql: "CREATE TABLE later2 (id INTEGER PRIMARY KEY)"},
		migration{since: "9.1.0", minWriter: len(known) + 2, sql: "ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''"})
	newer := filepath.Join(dir, "newer.db")
	db, err = Open(newer, quiet())
	require.NoError(t, err)
	require.NoError(t, db.Close())
	migrations = known
	assert.ErrorContains(t, Check(newer), fmt.Sprintf("state database %s: needs AgentAPI 9.1.0 or later (schema %d", newer, len(known)+2))

	notADB := filepath.Join(dir, "text.db")
	require.NoError(t, os.WriteFile(notADB, []byte("not a database, but long enough to look at its header, which is a hundred bytes long......."), 0o600))
	assert.ErrorContains(t, Check(notADB), "state database "+notADB)
}

// CheckWriter tells whether another program could write an open database.
func TestCheckWriter(t *testing.T) {
	known := migrations
	defer func() { migrations = known }()
	migrations = append(slices.Clone(known),
		migration{since: "9.0.0", minWriter: 1, sql: "CREATE TABLE later (id INTEGER PRIMARY KEY)"},
		migration{since: "9.1.0", minWriter: len(known) + 2, sql: "ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''"})
	db, err := Open(filepath.Join(t.TempDir(), "state.db"), quiet())
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.CheckWriter(len(known)+2, "9.1.0"))
	require.NoError(t, db.CheckWriter(len(known)+5, "10.0.0"), "a newer program migrates it")
	assert.ErrorContains(t, db.CheckWriter(len(known)+1, "9.0.4"),
		fmt.Sprintf("needs AgentAPI 9.1.0 or later (schema %d; AgentAPI 9.0.4 knows schema %d)", len(known)+2, len(known)+1))
}
