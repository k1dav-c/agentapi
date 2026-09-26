package jsonlwatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testMainThread = "main-thread"
	testCWD        = "/home/user/project"
)

func todaySessionDir(t *testing.T, root string) string {
	t.Helper()
	dir := codexSessionDirs(root, time.Now())[0]
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func codexRecord(ts time.Time, typ string, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`+"\n", ts.UTC().Format(time.RFC3339Nano), typ, payload)
}

func writeMainSession(t *testing.T, dir string, ts time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "rollout-main.jsonl")
	meta := fmt.Sprintf(`{"id":%q,"session_id":%q,"cwd":%q,"source":"cli"}`, testMainThread, testMainThread, testCWD)
	require.NoError(t, os.WriteFile(path, []byte(codexRecord(ts, "session_meta", meta)), 0o644))
	return path
}

// writeSubAgentSession writes a sub-agent file whose first two records after
// the metadata are copied parent history.
func writeSubAgentSession(t *testing.T, dir, id, agentPath string, ts time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "rollout-"+id+".jsonl")
	meta := fmt.Sprintf(`{"id":%q,"session_id":%q,"parent_thread_id":%q,"cwd":%q,"agent_path":%q,"agent_nickname":"Ampere",`+
		`"subagent_history_start_ordinal":3,"source":{"subagent":{"thread_spawn":{"depth":1,"agent_role":null}}}}`,
		id, testMainThread, testMainThread, testCWD, agentPath)
	content := codexRecord(ts, "session_meta", meta) +
		// Copied parent history must not affect the sub-agent's state.
		codexRecord(ts, "event_msg", `{"type":"task_started"}`) +
		codexRecord(ts, "event_msg", `{"type":"task_complete","last_agent_message":"parent answer"}`)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	for _, line := range lines {
		_, err := f.WriteString(line)
		require.NoError(t, err)
	}
}

func TestCodexResolver_SkipsSubAgentThreads(t *testing.T) {
	root := t.TempDir()
	dir := todaySessionDir(t, root)
	now := time.Now()
	mainPath := writeMainSession(t, dir, now.Add(-time.Minute))
	subPath := writeSubAgentSession(t, dir, "sub-1", "/root/worker", now)
	// The sub-agent file is newer and shares the cwd.
	require.NoError(t, os.Chtimes(mainPath, now.Add(-time.Minute), now.Add(-time.Minute)))
	require.NoError(t, os.Chtimes(subPath, now, now))

	resolved, err := (&CodexResolver{CWD: testCWD, SessionsDir: root}).Resolve()
	require.NoError(t, err)
	require.Equal(t, mainPath, resolved)
}

func TestCodexAgentTracker(t *testing.T) {
	root := t.TempDir()
	dir := todaySessionDir(t, root)
	start := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	writeMainSession(t, dir, start)
	tracker := NewCodexAgentTracker(&CodexResolver{CWD: testCWD, SessionsDir: root})

	agents, changed := tracker.Poll()
	require.Empty(t, agents)
	require.False(t, changed, "an empty list is unchanged from the initial state")

	subPath := writeSubAgentSession(t, dir, "sub-1", "/root/worker", start.Add(time.Second))
	// A sub-agent of another session is ignored.
	other := filepath.Join(dir, "rollout-other.jsonl")
	require.NoError(t, os.WriteFile(other, []byte(codexRecord(start, "session_meta",
		`{"id":"x","session_id":"other","parent_thread_id":"other","cwd":"/home/user/project"}`)), 0o644))

	appendLines(t, subPath,
		codexRecord(start.Add(2*time.Second), "event_msg", `{"type":"task_started"}`),
		codexRecord(start.Add(3*time.Second), "response_item",
			`{"type":"custom_tool_call","name":"exec","input":"const r = await tools.exec_command({cmd:\"sleep 300\",yield_time_ms:1000});text(r);\n"}`),
		codexRecord(start.Add(4*time.Second), "response_item",
			`{"type":"custom_tool_call","name":"exec","input":"text(await tools.write_stdin({session_id:1,chars:\"\"}));\n"}`),
		codexRecord(start.Add(5*time.Second), "response_item", `{"type":"function_call","name":"wait","arguments":"{}"}`),
		codexRecord(start.Add(6*time.Second), "event_msg", `{"type":"token_count","info":{"total_token_usage":{"total_tokens":1234}}}`),
	)

	agents, changed = tracker.Poll()
	require.True(t, changed)
	require.Len(t, agents, 1)
	require.Equal(t, SubAgent{
		ThreadID:       "sub-1",
		ParentThreadID: testMainThread,
		Path:           "/root/worker",
		Nickname:       "Ampere",
		Depth:          1,
		Status:         SubAgentRunning,
		Activity:       "$ sleep 300",
		TotalTokens:    1234,
		StartedAt:      start.Add(time.Second),
		UpdatedAt:      start.Add(6 * time.Second),
	}, agents[0])

	_, changed = tracker.Poll()
	require.False(t, changed)

	// A partial line is not applied until it is complete.
	complete := codexRecord(start.Add(7*time.Second), "event_msg", `{"type":"task_complete","last_agent_message":"done"}`)
	appendLines(t, subPath, complete[:20])
	_, changed = tracker.Poll()
	require.False(t, changed)
	appendLines(t, subPath, complete[20:])

	agents, changed = tracker.Poll()
	require.True(t, changed)
	require.Equal(t, SubAgentCompleted, agents[0].Status)
	require.Empty(t, agents[0].Activity)
	require.Equal(t, "done", agents[0].LastMessage)

	appendLines(t, subPath,
		codexRecord(start.Add(8*time.Second), "event_msg", `{"type":"task_started"}`),
		codexRecord(start.Add(9*time.Second), "event_msg", `{"type":"turn_aborted","reason":"interrupted"}`),
	)
	agents, _ = tracker.Poll()
	require.Equal(t, SubAgentInterrupted, agents[0].Status)
}
