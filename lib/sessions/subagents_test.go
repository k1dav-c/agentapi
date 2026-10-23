package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeSubagentEvents(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w", "s1", "subagents")
	// As Claude Code 2.1.302 keeps them: a log and a meta file per
	// subagent; the second one started first.
	writeLog(t, filepath.Join(dir, "agent-b.jsonl"), time.Now(),
		`{"isSidechain":true,"agentId":"b","type":"user","timestamp":"2026-10-18T10:00:05.000Z","sessionId":"s1","message":{"role":"user","content":"Review the code"}}`,
		`{"type":"api-request-blob","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>injected</system-reminder>"}]}}`,
		`{"isSidechain":true,"agentId":"b","type":"assistant","timestamp":"2026-10-18T10:00:07.000Z","sessionId":"s1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"toolu_sub","name":"Read","input":{"file_path":"main.go"}}]}}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-b.meta.json"), []byte(`{"agentType":"general-purpose","description":"Review","toolUseId":"toolu_parent_b"}`), 0o600))
	writeLog(t, filepath.Join(dir, "agent-a.jsonl"), time.Now(),
		`{"isSidechain":true,"agentId":"a","type":"user","timestamp":"2026-10-18T10:00:01.000Z","sessionId":"s1","message":{"role":"user","content":"Find the bug"}}`)

	events, err := SubagentEvents("claude", "/w", "s1")
	require.NoError(t, err)
	require.Len(t, events, 5)
	assert.Equal(t, "system", events[0].Kind)
	assert.Equal(t, "subagent started", *events[0].Content, "no meta file")
	assert.Equal(t, "a", *events[0].SubagentID)
	assert.Nil(t, events[0].ParentToolUseID)
	assert.Equal(t, "Find the bug", *events[1].Content)

	assert.Equal(t, "subagent started: Review (general-purpose)", *events[2].Content)
	for _, e := range events[2:] {
		assert.Equal(t, "b", *e.SubagentID)
		assert.Equal(t, "toolu_parent_b", *e.ParentToolUseID)
	}
	assert.Equal(t, "tool_call", events[4].Kind)
	assert.Equal(t, "Read", *events[4].ToolName)

	none, err := SubagentEvents("claude", "/w", "other")
	require.NoError(t, err)
	assert.Empty(t, none)
	_, err = SubagentEvents("claude", "/w", "../s1")
	assert.Error(t, err)
	none, err = SubagentEvents("pi", "/w", "s1")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestCodexSubagentEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	day := filepath.Join(home, "sessions", "2026", "10", "18")
	writeLog(t, filepath.Join(day, "rollout-1-t1.jsonl"), time.Now(),
		`{"timestamp":"2026-10-18T10:00:00.000Z","type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w"}}`)
	// As Codex 0.163.0 starts a subagent's log: its meta, a copy of the
	// parent's lines, then its own from subagent_history_start_ordinal.
	writeLog(t, filepath.Join(day, "rollout-2-sub.jsonl"), time.Now(),
		`{"timestamp":"2026-10-18T10:00:03.000Z","ordinal":0,"type":"session_meta","payload":{"id":"sub","session_id":"t1","parent_thread_id":"t1","cwd":"/w","agent_path":"/root/list_files","agent_nickname":"Maxwell","subagent_history_start_ordinal":3}}`,
		`{"timestamp":"2026-10-18T10:00:00.000Z","ordinal":1,"type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/w"}}`,
		`{"timestamp":"2026-10-18T10:00:01.000Z","ordinal":2,"type":"response_item","payload":{"type":"message","id":"p1","role":"assistant","content":[{"type":"output_text","text":"The parent's."}]}}`,
		`{"timestamp":"2026-10-18T10:00:03.500Z","ordinal":3,"type":"event_msg","payload":{"type":"task_started"}}`,
		`{"timestamp":"2026-10-18T10:00:04.000Z","ordinal":4,"type":"response_item","payload":{"type":"message","id":"s1","role":"assistant","content":[{"type":"output_text","text":"Checked."}]}}`,
		`{"timestamp":"2026-10-18T10:00:05.000Z","ordinal":5,"type":"event_msg","payload":{"type":"task_complete"}}`)
	// Another thread's subagent.
	writeLog(t, filepath.Join(day, "rollout-3-other.jsonl"), time.Now(),
		`{"timestamp":"2026-10-18T10:00:05.000Z","type":"session_meta","payload":{"id":"x","session_id":"t9","parent_thread_id":"t9","cwd":"/w"}}`,
		`{"timestamp":"2026-10-18T10:00:06.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Other."}]}}`)

	events, err := SubagentEvents("codex", "/w", "t1")
	require.NoError(t, err)
	var texts []string
	for _, e := range events {
		assert.Equal(t, "sub", *e.SubagentID)
		if e.Kind == "text" {
			texts = append(texts, *e.Content)
		}
	}
	assert.Equal(t, []string{"Checked."}, texts, "not the parent's copied lines")

	subs, err := Subagents("codex", "/w", "t1")
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, "list files", subs[0].Description)
	assert.Equal(t, "Maxwell", subs[0].AgentType)
	assert.Equal(t, "completed", subs[0].LogStatus)
	_, run, err := ReadSubagent("codex", "/w", "t1", "sub")
	require.NoError(t, err)
	require.Len(t, run.Messages, 1)
	assert.Equal(t, "Checked.", run.Messages[0].Content[0].Text)
}
