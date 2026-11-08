package statedb_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/statedb"
)

func TestStoredStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	resets := time.Unix(1800000000, 0).UTC()
	stopped := time.Unix(1700000000, 0).UTC()
	want := agentstate.StoredState{
		Settings:  agentstate.Settings{Model: "opus", PermissionMode: agentstate.PermissionModeAsk},
		LimitStop: &agentstate.LimitStop{Message: "You've hit your limit", StoppedAt: stopped, ResetsAt: &resets},
		RetryWait: &agentstate.RetryWait{Message: "API Error: 502 Bad Gateway", APIStatus: 502, StoppedAt: stopped, Attempt: 2, Attempts: 3, RetryAt: stopped.Add(2 * time.Minute)},
	}
	db.SetMeta(statedb.MetaAgentType, "claude")
	db.SetStoredState(want)
	require.NoError(t, db.Close())

	db = open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, want, got)

	db.SetStoredState(agentstate.StoredState{})
	require.NoError(t, db.Close())
	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err = db.Load()
	require.NoError(t, err)
	got, err = snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{}, got)
}

// metaRows returns the meta table of the closed database at path.
func metaRows(t *testing.T, path string) map[string]string {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = sqlDB.Close() }()
	rows, err := sqlDB.Query("SELECT key, value FROM meta")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	meta := map[string]string{}
	for rows.Next() {
		var k, v string
		require.NoError(t, rows.Scan(&k, &v))
		meta[k] = v
	}
	require.NoError(t, rows.Err())
	return meta
}

// setMetaRows writes meta values into the closed database at path, as an
// AgentAPI that didn't know conversation_state did.
func setMetaRows(t *testing.T, path string, values map[string]string) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = sqlDB.Close() }()
	for k, v := range values {
		_, err := sqlDB.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", k, v)
		require.NoError(t, err)
	}
}

// oldDB returns a closed database as AgentAPI 3.7.2 left it: the stored
// state in a key each, and no conversation_state.
func oldDB(t *testing.T, values map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.SetMeta(statedb.MetaAgentType, "claude")
	require.NoError(t, db.Close())
	setMetaRows(t, path, values)
	require.NotContains(t, metaRows(t, path), statedb.MetaState)
	return path
}

// A database of AgentAPI 3.7.2 has its state moved into conversation_state
// on its first open; the old keys stay, written as before.
func TestStoredStateMigratesTheOldKeys(t *testing.T) {
	path := oldDB(t, map[string]string{
		"settings":   `{"permission_mode":"plan","model":"sonnet"}`,
		"limit_stop": `{"message":"You've hit your limit","stopped_at":"2023-11-14T22:13:20Z","resets_at":"2027-01-15T08:00:00+08:00"}`,
		"retry_wait": "",
	})
	db := open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.Settings{PermissionMode: agentstate.PermissionModePlan, Model: "sonnet"}, got.Settings)
	require.NotNil(t, got.LimitStop)
	assert.Equal(t, "You've hit your limit", got.LimitStop.Message)
	assert.True(t, got.LimitStop.ResetsAt.Equal(time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)))
	assert.Nil(t, got.RetryWait)
	require.NoError(t, db.Close())

	meta := metaRows(t, path)
	assert.JSONEq(t, `{"version":1,"settings":{"permission_mode":"plan","model":"sonnet"},"limit_stop":{"message":"You've hit your limit","stopped_at":"2023-11-14T22:13:20Z","resets_at":"2027-01-15T08:00:00+08:00"}}`, meta[statedb.MetaState])
	assert.JSONEq(t, `{"permission_mode":"plan","model":"sonnet"}`, meta["settings"])
	assert.NotEmpty(t, meta["limit_stop"])
	assert.Empty(t, meta["retry_wait"])

	// Opened again, it reads the same.
	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err = db.Load()
	require.NoError(t, err)
	again, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, got.Settings, again.Settings)
	assert.True(t, again.LimitStop.ResetsAt.Equal(*got.LimitStop.ResetsAt))
}

// AgentAPI 3.7.2 opened on a database a newer one wrote finds its keys;
// what it changes there wins over conversation_state, which it doesn't
// know, when a newer AgentAPI opens the file again.
func TestStoredStateSurvivesADowngrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.SetMeta(statedb.MetaAgentType, "claude")
	db.SetStoredState(agentstate.StoredState{
		Settings:  agentstate.Settings{Model: "opus"},
		RetryWait: &agentstate.RetryWait{Message: "API Error: 502", StoppedAt: time.Unix(1700000000, 0).UTC(), Attempt: 1, Attempts: 3, RetryAt: time.Unix(1700000030, 0).UTC()},
	})
	require.NoError(t, db.Close())
	meta := metaRows(t, path)
	assert.JSONEq(t, `{"model":"opus"}`, meta["settings"], "the old keys are written too")
	assert.Contains(t, meta["retry_wait"], "API Error: 502")
	assert.Empty(t, meta["limit_stop"])

	// The older AgentAPI cancelled the retry and changed the model.
	setMetaRows(t, path, map[string]string{"retry_wait": "", "settings": `{"model":"haiku"}`})
	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{Settings: agentstate.Settings{Model: "haiku"}}, got)
	assert.JSONEq(t, `{"version":1,"settings":{"model":"haiku"}}`, snap.Meta[statedb.MetaState], "it is moved again")
}

func TestStoredStateThatCantBeRead(t *testing.T) {
	// A broken old key leaves out what it held (with a warning), and the
	// rest is moved.
	path := oldDB(t, map[string]string{"settings": `{"model":"opus"}`, "retry_wait": "{"})
	db := open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{Settings: agentstate.Settings{Model: "opus"}}, got)
	require.NoError(t, db.Close())
	assert.Empty(t, metaRows(t, path)["retry_wait"])

	// A broken object is read from the old keys.
	setMetaRows(t, path, map[string]string{statedb.MetaState: "{"})
	db = open(t, path)
	snap, err = db.Load()
	require.NoError(t, err)
	got, err = snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{Settings: agentstate.Settings{Model: "opus"}}, got)
	require.NoError(t, db.Close())

	// An object of a newer version is left as it is; the old keys, which
	// the newer AgentAPI writes too, are read.
	newer := `{"version":2,"settings":{"model":"next"}}`
	setMetaRows(t, path, map[string]string{statedb.MetaState: newer})
	db = open(t, path)
	defer func() { _ = db.Close() }()
	snap, err = db.Load()
	require.NoError(t, err)
	got, err = snap.StoredState()
	assert.ErrorContains(t, err, "a newer AgentAPI stored the conversation state")
	assert.Equal(t, agentstate.Settings{Model: "opus"}, got.Settings)
	assert.Equal(t, newer, snap.Meta[statedb.MetaState])
}
