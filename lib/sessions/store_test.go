package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/pricing"
)

// appendLines appends lines to a log and gives it back its modification
// time, as a coarse clock does for most appends.
func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	for _, line := range lines {
		_, err = f.WriteString(line)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	require.NoError(t, os.Chtimes(path, info.ModTime(), info.ModTime()))
}

func TestStoreReadsWhatTheAgentAppends(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	path := filepath.Join(dir, "s1.jsonl")
	writeLog(t, path, time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Fix the bug"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_1", 10, 20000, 0, 0, 200),
	)
	store := newStore(t, "claude", "/w")
	d, err := store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 1, d.Cost("s1").Calls)

	// The call goes on, a second one starts and the user writes again:
	// the modification time stays, the size tells.
	appendLines(t, path,
		claudeCall("s1", "2026-10-20T10:00:06.000Z", "msg_1", 10, 20000, 0, 0, 500)+"\n",
		claudePromptLine("s1", "2026-10-20T10:01:00.000Z", "And the tests")+"\n",
		claudeCall("s1", "2026-10-20T10:01:05.000Z", "msg_2", 5, 21000, 0, 0, 100)[:40],
	)
	d, err = store.Costs()
	require.NoError(t, err)
	c := d.Cost("s1")
	assert.Equal(t, 1, c.Calls, "the line being written waits")
	assert.Equal(t, 500, c.Tokens.Output, "the call's later line counts")
	list, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 2, list[0].Prompts)

	appendLines(t, path, claudeCall("s1", "2026-10-20T10:01:05.000Z", "msg_2", 5, 21000, 0, 0, 100)[40:]+"\n")
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 2, d.Cost("s1").Calls)
	opus, _ := pricing.Builtin().Lookup("claude-opus-5-5")
	want := opus.Cost(pricing.Tokens{Input: 10, CacheRead: 20000, Output: 500}) + opus.Cost(pricing.Tokens{Input: 5, CacheRead: 21000, Output: 100})
	assert.InDelta(t, want, d.Total.USD, 1e-12)

	// A log rewritten in place is read again.
	writeLog(t, path, time.Now(),
		claudePromptLine("s1", "2026-10-20T10:00:00.000Z", "Something else entirely, and longer than that"),
		claudeCall("s1", "2026-10-20T10:00:05.000Z", "msg_9", 10, 20000, 0, 0, 200),
		claudeCall("s1", "2026-10-20T10:00:06.000Z", "msg_9", 10, 20000, 0, 0, 900),
	)
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Equal(t, 1, d.Cost("s1").Calls)
	assert.Equal(t, 900, d.Cost("s1").Tokens.Output)
	list, err = store.List(10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 1, list[0].Prompts)
	assert.Equal(t, "Something else entirely, and longer than that", list[0].FirstPrompt)
}

func TestStoreForgetsLogsThatAreGone(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	for _, id := range []string{"s1", "s2"} {
		writeLog(t, filepath.Join(dir, id+".jsonl"), time.Now(),
			claudePromptLine(id, "2026-10-20T10:00:00.000Z", "Hi"),
			claudeCall(id, "2026-10-20T10:00:05.000Z", "msg_"+id, 10, 20000, 0, 0, 200))
	}
	writeLog(t, filepath.Join(dir, "s1", "subagents", "agent-a1.jsonl"), time.Now(),
		claudeCall("s1", "2026-10-20T10:00:06.000Z", "msg_sub", 3, 1000, 0, 0, 100))
	store := newStore(t, "claude", "/w")
	d, err := store.Costs()
	require.NoError(t, err)
	assert.Len(t, d.Sessions, 2)
	assert.Equal(t, 2, store.logs.Len())
	assert.Equal(t, 1, store.subs.Len())

	require.NoError(t, os.RemoveAll(filepath.Join(dir, "s1")))
	require.NoError(t, os.Remove(filepath.Join(dir, "s1.jsonl")))
	d, err = store.Costs()
	require.NoError(t, err)
	assert.Len(t, d.Sessions, 1)
	assert.Equal(t, 1, store.logs.Len(), "what was read of a log that is gone is forgotten")
	assert.Zero(t, store.subs.Len())
	_, err = store.ReadHistory("s1")
	assert.ErrorIs(t, err, ErrSessionNotFound)
}
