package codexlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeThreads writes Codex thread logs, by thread ID, into a fresh
// CODEX_HOME.
func writeThreads(t *testing.T, logs map[string]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "sessions", "2026", "10", "18")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for id, body := range logs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "rollout-2026-10-18T10-00-00-"+id+".jsonl"), []byte(body), 0o644))
	}
}

// scanned lists what the lines scanned are: a session_meta line's thread
// ID, another line's payload n.
func scanned(t *testing.T, scan func(fn func(line []byte) bool) bool) []string {
	t.Helper()
	var got []string
	scan(func(line []byte) bool {
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
				N  string `json:"n"`
			} `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry.Type == "session_meta" {
			got = append(got, "meta:"+entry.Payload.ID)
		} else {
			got = append(got, entry.Payload.N)
		}
		return true
	})
	return got
}

// A fork of a fork continues both: the lines of the thread its parent
// forked come first.
func TestScanLogForkOfAFork(t *testing.T) {
	writeThreads(t, map[string]string{
		"t1": `{"ordinal":0,"type":"session_meta","payload":{"id":"t1"}}
{"ordinal":1,"type":"x","payload":{"n":"t1-a"}}
{"ordinal":2,"type":"x","payload":{"n":"t1-b"}}
{"ordinal":3,"type":"x","payload":{"n":"t1-after"}}
`,
		"t2": `{"ordinal":0,"type":"session_meta","payload":{"id":"t2","forked_from_id":"t1","forked_from_ordinal_exclusive":3}}
{"ordinal":1,"type":"x","payload":{"n":"t2-a"}}
{"ordinal":2,"type":"x","payload":{"n":"t2-after"}}
`,
		"t3": `{"ordinal":0,"type":"session_meta","payload":{"id":"t3","forked_from_id":"t2","forked_from_ordinal_exclusive":2}}
{"ordinal":1,"type":"x","payload":{"n":"t3-a"}}
`,
	})
	path, ok := LogOf("t3")
	require.True(t, ok)
	assert.Equal(t, []string{"meta:t3", "t1-a", "t1-b", "t2-a", "t3-a"},
		scanned(t, func(fn func([]byte) bool) bool { return ScanLog(path, fn) }))
	assert.Equal(t, []string{"t1-a", "t1-b", "t2-a"},
		scanned(t, func(fn func([]byte) bool) bool { return ScanForked("t2", 2, fn) }))
}

// Threads that name each other as their fork's source are read once
// each.
func TestScanLogForkCycle(t *testing.T) {
	writeThreads(t, map[string]string{
		"t1": `{"ordinal":0,"type":"session_meta","payload":{"id":"t1","forked_from_id":"t2"}}
{"ordinal":1,"type":"x","payload":{"n":"t1-a"}}
`,
		"t2": `{"ordinal":0,"type":"session_meta","payload":{"id":"t2","forked_from_id":"t1"}}
{"ordinal":1,"type":"x","payload":{"n":"t2-a"}}
`,
	})
	path, ok := LogOf("t2")
	require.True(t, ok)
	assert.Equal(t, []string{"meta:t2", "t1-a", "t2-a"},
		scanned(t, func(fn func([]byte) bool) bool { return ScanLog(path, fn) }))
	assert.Equal(t, []string{"t2-a", "t1-a"},
		scanned(t, func(fn func([]byte) bool) bool { return ScanForked("t1", -1, fn) }))
}
