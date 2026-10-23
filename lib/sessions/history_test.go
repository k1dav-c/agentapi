package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	st "github.com/coder/agentapi/lib/screentracker"
)

// writeLog writes a session log of lines, last changed at mod.
func writeLog(t *testing.T, path string, mod time.Time, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	require.NoError(t, os.Chtimes(path, mod, mod))
}

// newStore returns a Store of the sessions an agent of agentType had in
// cwd.
func newStore(t testing.TB, agentType, cwd string) *Store {
	t.Helper()
	s, err := New(agentType, cwd, Config{})
	require.NoError(t, err)
	return s
}

func roles(msgs []st.ConversationMessage) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, string(m.Role)+": "+m.Message)
	}
	return out
}

func TestClaudeSessionHistory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	cwd := "/work/my_app"
	dir := filepath.Join(config, "projects", "-work-my-app")
	now := time.Now()
	// As Claude Code 2.1.302 logs a session: a queue operation first, a
	// slash command the user ran, a tool call, and the title.
	writeLog(t, filepath.Join(dir, "s1.jsonl"), now.Add(-time.Hour),
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-17T14:10:11.834Z","sessionId":"s1","content":"Fix the bug"}`,
		`{"type":"user","isSidechain":false,"timestamp":"2026-10-17T14:10:12.000Z","sessionId":"s1","message":{"role":"user","content":"Fix the bug"}}`,
		`{"type":"assistant","timestamp":"2026-10-17T14:10:14.000Z","sessionId":"s1","message":{"id":"msg_1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use"}}`,
		`{"type":"user","timestamp":"2026-10-17T14:10:15.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"main.go"}]}}`,
		`{"type":"assistant","timestamp":"2026-10-17T14:10:16.000Z","sessionId":"s1","message":{"id":"msg_2","role":"assistant","content":[{"type":"text","text":"Fixed."}],"stop_reason":"end_turn"}}`,
		`{"type":"ai-title","aiTitle":"Fix the bug","sessionId":"s1"}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-10-17T14:11:00.000Z","sessionId":"s1","message":{"role":"user","content":"<local-command-caveat>Caveat</local-command-caveat>"}}`,
		`{"type":"user","timestamp":"2026-10-17T14:11:00.000Z","sessionId":"s1","message":{"role":"user","content":"<command-name>/model</command-name>"}}`,
		`{"type":"user","timestamp":"2026-10-17T14:11:30.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"user","timestamp":"2026-10-17T14:12:00.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"text","text":"Thanks"}]}}`,
		`{"type":"custom-title","customTitle":"Bug hunt","sessionId":"s1"}`,
		`{"type":"user","timestamp":"2026-10-17T14:13:00.000Z","sessionId":"s1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"text","text":"And this?"}]}}`,
	)
	// A session in which the user sent nothing.
	writeLog(t, filepath.Join(dir, "s2.jsonl"), now,
		`{"type":"queue-operation","operation":"dequeue","timestamp":"2026-10-17T15:00:00.000Z","sessionId":"s2"}`)
	// Another directory's.
	writeLog(t, filepath.Join(config, "projects", "-other", "s3.jsonl"), now,
		`{"type":"user","timestamp":"2026-10-17T15:00:00.000Z","sessionId":"s3","message":{"role":"user","content":"elsewhere"}}`)

	store := newStore(t, "claude", cwd)
	sessions, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	s := sessions[0]
	assert.Equal(t, "s1", s.ID)
	assert.Equal(t, "Bug hunt", s.Title, "the custom title wins")
	assert.Equal(t, "Fix the bug", s.FirstPrompt)
	assert.Equal(t, 3, s.Prompts)
	assert.Equal(t, time.Date(2026, 10, 17, 14, 10, 11, 834e6, time.UTC), s.CreatedAt)

	h, err := store.ReadHistory("s1")
	require.NoError(t, err)
	assert.Equal(t, "Bug hunt", h.Title)
	assert.Equal(t, []string{"user: Fix the bug", "agent: Looking.\n\nFixed.", "user: Thanks", "user: And this?"}, roles(h.Messages))
	for i, m := range h.Messages {
		assert.Equal(t, i, m.Id)
	}
	var ids []string
	for _, m := range h.Rich {
		ids = append(ids, m.MessageID+"/"+m.Role)
		if m.Role == "user" {
			// The note of the interrupted turn and the prompts aren't tool
			// results.
			for _, b := range m.Content {
				assert.Equal(t, "tool_result", b.Type)
				assert.NotEmpty(t, b.ToolUseID)
			}
		}
	}
	assert.Contains(t, ids, "history-msg_1/assistant")
	assert.Contains(t, ids, "history-msg_2/assistant")

	_, err = store.ReadHistory("s3")
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestCodexSessionHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cwd := "/work/app"
	day := filepath.Join(home, "sessions", "2026", "10", "18")
	meta := func(id, cwd string) string {
		return `{"timestamp":"2026-10-18T15:01:41.013Z","type":"session_meta","payload":{"id":"` + id + `","session_id":"` + id + `","cwd":"` + cwd + `","cli_version":"0.163.0"}}`
	}
	writeLog(t, filepath.Join(day, "rollout-2026-10-18T15-01-41-t1.jsonl"), time.Now(),
		meta("t1", cwd),
		`{"timestamp":"2026-10-18T15:01:41.411Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>"}]}}`,
		`{"timestamp":"2026-10-18T15:01:41.412Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n</environment_context>"}]}}`,
		`{"timestamp":"2026-10-18T15:01:41.413Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Why review code?"}]}}`,
		`{"timestamp":"2026-10-18T15:01:52.000Z","type":"response_item","payload":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"To catch bugs."}]}}`,
		`{"timestamp":"2026-10-18T15:01:52.115Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	)
	// A subagent's thread, and another directory's.
	writeLog(t, filepath.Join(day, "rollout-2026-10-18T15-02-00-t2.jsonl"), time.Now(),
		`{"timestamp":"2026-10-18T15:02:00.000Z","type":"session_meta","payload":{"id":"t2","session_id":"t1","cwd":"`+cwd+`"}}`,
		`{"timestamp":"2026-10-18T15:02:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"sub"}]}}`)
	writeLog(t, filepath.Join(day, "rollout-2026-10-18T15-03-00-t3.jsonl"), time.Now(),
		meta("t3", "/elsewhere"),
		`{"timestamp":"2026-10-18T15:03:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"other"}]}}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(`{"id":"t1","thread_name":"Code review","updated_at":"2026-10-18T15:02:00Z"}`+"\n"), 0o600))

	store := newStore(t, "codex", cwd)
	sessions, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "t1", sessions[0].ID)
	assert.Equal(t, "Code review", sessions[0].Title)
	assert.Equal(t, "Why review code?", sessions[0].FirstPrompt)

	h, err := store.ReadHistory("t1")
	require.NoError(t, err)
	assert.Equal(t, []string{"user: Why review code?", "agent: To catch bugs."}, roles(h.Messages))
}

func TestPiSessionHistory(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	cwd := "/work/app"
	dir := filepath.Join(agentDir, "sessions", "--work-app--")
	writeLog(t, filepath.Join(dir, "2026-10-18T08-15-17-792Z_agentapi-1.jsonl"), time.Now(),
		`{"type":"session","version":3,"id":"agentapi-1","timestamp":"2026-10-18T08:15:17.792Z","cwd":"/work/app"}`,
		`{"type":"message","id":"a","timestamp":"2026-10-18T08:15:26.023Z","message":{"role":"user","content":[{"type":"text","text":"Say pong"}]}}`,
		`{"type":"message","id":"b","timestamp":"2026-10-18T08:15:28.399Z","message":{"role":"assistant","content":[{"type":"text","text":"pong"}],"stopReason":"stop"}}`,
		`{"type":"session_info","name":"Ping"}`,
	)

	store := newStore(t, "pi", cwd)
	sessions, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "agentapi-1", sessions[0].ID)
	assert.Equal(t, "Ping", sessions[0].Title)

	h, err := store.ReadHistory("agentapi-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"user: Say pong", "agent: pong"}, roles(h.Messages))
}

func TestListSessionsLimit(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "projects", "-w")
	now := time.Now()
	for i, id := range []string{"a", "b", "c"} {
		writeLog(t, filepath.Join(dir, id+".jsonl"), now.Add(time.Duration(i)*time.Minute),
			`{"type":"user","timestamp":"2026-10-17T14:10:12.000Z","sessionId":"`+id+`","message":{"role":"user","content":"hi `+id+`"}}`)
	}
	sessions, err := newStore(t, "claude", "/w").List(2)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	assert.Equal(t, "c", sessions[0].ID, "the latest first")
	assert.Equal(t, "b", sessions[1].ID)

	_, err = New("aider", "/w", Config{})
	assert.Error(t, err)
	assert.False(t, Supported("aider"))
}

func TestCodexForkHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cwd := "/work/app"
	day := filepath.Join(home, "sessions", "2026", "10", "18")
	user := func(ord int, at, text string) string {
		return fmt.Sprintf(`{"timestamp":"2026-10-18T%s.000Z","ordinal":%d,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, at, ord, text)
	}
	reply := func(ord int, at, id, text string) string {
		return fmt.Sprintf(`{"timestamp":"2026-10-18T%s.000Z","ordinal":%d,"type":"response_item","payload":{"type":"message","id":%q,"role":"assistant","content":[{"type":"output_text","text":%q}]}}`, at, ord, id, text)
	}
	// As Codex 0.163.0 forks: the fork's log refers to the thread's lines
	// before an ordinal instead of copying them.
	writeLog(t, filepath.Join(day, "rollout-2026-10-18T10-00-00-t1.jsonl"), time.Now().Add(-time.Hour),
		`{"timestamp":"2026-10-18T10:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"t1","session_id":"t1","cwd":"/work/app"}}`,
		user(1, "10:00:01", "first"), reply(2, "10:00:02", "m1", "one"),
		user(3, "10:00:03", "second"), reply(4, "10:00:04", "m2", "two"),
		user(5, "10:05:00", "after the fork"))
	writeLog(t, filepath.Join(day, "rollout-2026-10-18T10-01-00-t2.jsonl"), time.Now(),
		`{"timestamp":"2026-10-18T10:01:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"t2","session_id":"t2","forked_from_id":"t1","forked_from_ordinal_exclusive":5,"cwd":"/work/app"}}`,
		user(1, "10:01:01", "in the fork"), reply(2, "10:01:02", "m3", "forked"))

	store := newStore(t, "codex", cwd)
	sessions, err := store.List(10)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	assert.Equal(t, "t2", sessions[0].ID)
	assert.Equal(t, "first", sessions[0].FirstPrompt)
	assert.Equal(t, 3, sessions[0].Prompts, "the thread's two before the fork, and its own")

	h, err := store.ReadHistory("t2")
	require.NoError(t, err)
	assert.Equal(t, []string{"user: first", "agent: one", "user: second", "agent: two", "user: in the fork", "agent: forked"}, roles(h.Messages))
}
