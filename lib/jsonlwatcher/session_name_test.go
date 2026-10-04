package jsonlwatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeSessionName(t *testing.T) {
	n := NewSessionNamer("claude")
	name, changed := n.Name([]byte(`{"type":"user","message":{"content":"hi"}}`))
	assert.False(t, changed)
	assert.Empty(t, name)

	name, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Agentapi 實作調整","sessionId":"s1"}`))
	assert.True(t, changed)
	assert.Equal(t, "Agentapi 實作調整", name)
	_, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Agentapi 實作調整","sessionId":"s1"}`))
	assert.False(t, changed, "the same title again")

	name, changed = n.Name([]byte(`{"type":"custom-title","customTitle":"Release prep","sessionId":"s1"}`))
	assert.True(t, changed)
	assert.Equal(t, "Release prep", name)
	name, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Something else","sessionId":"s1"}`))
	assert.False(t, changed, "a /rename title wins over generated ones")
	assert.Equal(t, "Release prep", name)
}

func TestPiSessionName(t *testing.T) {
	n := NewSessionNamer("pi")
	name, changed := n.Name([]byte(`{"type":"session_info","id":"k1","parentId":"j0","timestamp":"2026-10-04T14:35:00.000Z","name":"Refactor auth module"}`))
	assert.True(t, changed)
	assert.Equal(t, "Refactor auth module", name)
	_, changed = n.Name([]byte(`{"type":"message","id":"m1","message":{"role":"user","content":"session_info"}}`))
	assert.False(t, changed)
}

func TestCodexSessionName(t *testing.T) {
	index := filepath.Join(t.TempDir(), "session_index.jsonl")
	n := &codexSessionNamer{indexPath: index}
	_, changed := n.Name([]byte(`{"timestamp":"2026-10-04T19:26:00Z","type":"session_meta","payload":{"id":"thread-1","session_id":"thread-1","cwd":"/proj"}}`))
	assert.False(t, changed, "no index yet")

	// Codex names the thread after its first turn; the index changes
	// outside the session log, so Poll picks it up.
	require.NoError(t, os.WriteFile(index, []byte(
		`{"id":"thread-0","thread_name":"Another thread","updated_at":"2026-10-04T19:00:00Z"}`+"\n"+
			`{"id":"thread-1","thread_name":"Run system inspection commands","updated_at":"2026-10-04T19:26:29Z"}`+"\n"), 0o600))
	name, changed := n.Poll()
	assert.True(t, changed)
	assert.Equal(t, "Run system inspection commands", name)
	_, changed = n.Poll()
	assert.False(t, changed, "index unchanged")

	// A rename appends a newer entry.
	f, err := os.OpenFile(index, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"id":"thread-1","thread_name":"Inspect the system","updated_at":"2026-10-04T19:40:00Z"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	later := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(index, later, later))
	name, changed = n.Poll()
	assert.True(t, changed)
	assert.Equal(t, "Inspect the system", name)

	assert.Nil(t, NewSessionNamer("aider"))
}
