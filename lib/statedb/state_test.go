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
		Settings:  agentstate.Settings{Provider: "kimi", Model: "opus", PermissionMode: agentstate.PermissionModeAsk},
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

// A database of AgentAPI 3.7.2 or earlier, with each part of the stored
// state under a key of its own, opens without stored state; the keys stay
// as they are.
func TestStoredStateIgnoresTheOldKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.SetMeta(statedb.MetaAgentType, "claude")
	require.NoError(t, db.Close())
	setMetaRows(t, path, map[string]string{"settings": `{"model":"sonnet"}`, "retry_wait": `{"message":"API Error: 502"}`})
	db = open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{}, got)
	require.NoError(t, db.Close())
	assert.Equal(t, `{"model":"sonnet"}`, metaRows(t, path)["settings"])
}

// SetStoredState writes conversation_state alone.
func TestStoredStateWritesNoOldKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	db.SetMeta(statedb.MetaAgentType, "claude")
	db.SetStoredState(agentstate.StoredState{Settings: agentstate.Settings{Model: "opus"}})
	require.NoError(t, db.Close())
	meta := metaRows(t, path)
	assert.JSONEq(t, `{"version":2,"settings":{"model":"opus"}}`, meta[statedb.MetaState])
	for _, key := range []string{"settings", "limit_stop", "retry_wait"} {
		assert.NotContains(t, meta, key)
	}
}

// Version 1 stored only the model. It still restores, and the next write
// carries the model forward into the provider-aware envelope.
func TestStoredStateReadsVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := open(t, path)
	require.NoError(t, db.Close())
	setMetaRows(t, path, map[string]string{
		statedb.MetaState: `{"version":1,"settings":{"model":"sonnet"}}`,
	})
	db = open(t, path)
	snap, err := db.Load()
	require.NoError(t, err)
	got, err := snap.StoredState()
	require.NoError(t, err)
	assert.Equal(t, agentstate.StoredState{Settings: agentstate.Settings{Model: "sonnet"}}, got)
	db.SetStoredState(got)
	require.NoError(t, db.Close())
	assert.JSONEq(t, `{"version":2,"settings":{"model":"sonnet"}}`, metaRows(t, path)[statedb.MetaState])
}

func TestStoredStateThatCantBeRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		envelope string
		wantErr  string
	}{
		{name: "broken object", envelope: "{", wantErr: "decode the stored conversation state"},
		{name: "newer version", envelope: `{"version":3,"settings":{"model":"next"}}`, wantErr: "a newer AgentAPI stored the conversation state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db := open(t, path)
			db.SetMeta(statedb.MetaAgentType, "claude")
			require.NoError(t, db.Close())
			setMetaRows(t, path, map[string]string{statedb.MetaState: tc.envelope})

			db = open(t, path)
			snap, err := db.Load()
			require.NoError(t, err)
			got, err := snap.StoredState()
			assert.ErrorContains(t, err, tc.wantErr)
			assert.Equal(t, agentstate.StoredState{}, got)

			// Restoring an empty state and changing it later must not destroy
			// what only a newer AgentAPI, or a repair, can read. Other writes
			// still go through.
			db.SetStoredState(agentstate.StoredState{})
			db.SetStoredState(agentstate.StoredState{Settings: agentstate.Settings{Model: "older"}})
			db.SetSessionName("changed after restore")
			require.NoError(t, db.Close())
			meta := metaRows(t, path)
			assert.Equal(t, tc.envelope, meta[statedb.MetaState])
			assert.Equal(t, "changed after restore", meta[statedb.MetaSessionName])

			db = open(t, path)
			defer func() { _ = db.Close() }()
			snap, err = db.Load()
			require.NoError(t, err)
			assert.Equal(t, tc.envelope, snap.Meta[statedb.MetaState])
			assert.Equal(t, "changed after restore", snap.Meta[statedb.MetaSessionName])
			_, err = snap.StoredState()
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
