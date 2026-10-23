package statedb_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/statedb"
)

func TestImportStateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "state.json")
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	require.NoError(t, os.WriteFile(file, []byte(`{"version":1,"messages":[
		{"id":0,"message":"Ready","role":"agent","time":"2026-10-20T10:00:00Z"},
		{"id":1,"message":"Fix the bug","role":"user","time":"2026-10-20T10:00:00Z"},
		{"id":2,"message":"Fixed.","role":"agent","time":"2026-10-20T10:00:00Z"}
	],"initial_prompt":"","initial_prompt_sent":true}`), 0o600))
	meta := map[string]string{statedb.MetaAgentType: "aider", statedb.MetaTransport: "pty", statedb.MetaCWD: "/work"}

	db := open(t, filepath.Join(dir, "state.db"))
	n, err := db.ImportStateFile(file, meta)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	snap, err := db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Clean, "an import is as good as a clean close")
	require.Len(t, snap.Messages, 3)
	assert.Equal(t, st.ConversationMessage{Id: 1, Message: "Fix the bug", Role: st.ConversationRoleUser, Time: at}, snap.Messages[1].Message)
	assert.EqualValues(t, 3, snap.Seq())
	assert.NotEmpty(t, snap.Meta[statedb.MetaEpoch])
	for key, value := range meta {
		assert.Equal(t, value, snap.Meta[key], key)
	}

	// Once imported, the database keeps the conversation.
	_, err = db.ImportStateFile(file, meta)
	require.ErrorIs(t, err, statedb.ErrNotEmpty)
}

func TestImportStateFileRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := open(t, filepath.Join(dir, "state.db"))

	n, err := db.ImportStateFile(filepath.Join(dir, "missing.json"), nil)
	require.NoError(t, err, "no state file: nothing to import")
	assert.Zero(t, n)

	for name, content := range map[string]string{
		"not JSON":      "{",
		"a new version": `{"version":2,"messages":[]}`,
	} {
		file := filepath.Join(dir, name+".json")
		require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
		_, err := db.ImportStateFile(file, nil)
		assert.Error(t, err, name)
	}
	snap, err := db.Load()
	require.NoError(t, err)
	assert.True(t, snap.Empty())
}
