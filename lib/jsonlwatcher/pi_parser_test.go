package jsonlwatcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parsePiLines(t *testing.T, lines ...string) []RichMessage {
	t.Helper()
	p := NewPiParser()
	var out []RichMessage
	for _, line := range lines {
		msgs, err := p.ParseLine([]byte(line))
		require.NoError(t, err)
		out = append(out, msgs...)
	}
	return out
}

func TestPiParserTurn(t *testing.T) {
	msgs := parsePiLines(t,
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-04T15:00:00.000Z","cwd":"/home/me/proj"}`,
		`{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-10-04T15:00:00.100Z","provider":"openai-codex","modelId":"gpt-5.5"}`,
		`{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-10-04T15:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"List the files"}],"timestamp":1791126001000}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-04T15:00:03.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"**Listing**\n\nI'll run ls."},{"type":"thinking","thinking":"","redacted":true},{"type":"toolCall","id":"call_1","name":"bash","arguments":{"command":"ls"}}],"provider":"openai-codex","model":"gpt-5.5","usage":{"input":1200,"output":40,"cacheRead":800,"cacheWrite":0,"totalTokens":2040},"stopReason":"toolUse","timestamp":1791126003000}}`,
		`{"type":"message","id":"r1","parentId":"a1","timestamp":"2026-10-04T15:00:04.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"bash","content":[{"type":"text","text":"a.go\nb.go"}],"isError":false,"timestamp":1791126004000}}`,
		`{"type":"message","id":"a2","parentId":"r1","timestamp":"2026-10-04T15:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"There are two files."}],"provider":"openai-codex","model":"gpt-5.5","usage":{"input":1300,"output":8,"cacheRead":1200,"cacheWrite":0},"stopReason":"stop","timestamp":1791126005000}}`,
	)
	require.Len(t, msgs, 4)

	assert.Equal(t, "u1", msgs[0].MessageID)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, []RichContentBlock{{Type: "text", Text: "List the files"}}, msgs[0].Content)
	assert.Equal(t, time.Date(2026, 10, 4, 15, 0, 1, 0, time.UTC), msgs[0].Timestamp)

	call := msgs[1]
	assert.Equal(t, "assistant", call.Role)
	assert.Equal(t, "gpt-5.5", call.Model)
	assert.Equal(t, "tool_use", call.StopReason)
	assert.Equal(t, &Usage{InputTokens: 1200, OutputTokens: 40, CacheReadInputTokens: 800}, call.Usage)
	require.Len(t, call.Content, 2, "redacted thinking is dropped")
	assert.Equal(t, RichContentBlock{Type: "thinking", Thinking: "**Listing**\n\nI'll run ls."}, call.Content[0])
	assert.Equal(t, "tool_use", call.Content[1].Type)
	assert.Equal(t, "call_1", call.Content[1].ToolUseID)
	assert.Equal(t, "bash", call.Content[1].ToolName)
	assert.JSONEq(t, `{"command":"ls"}`, string(call.Content[1].ToolInput))

	result := msgs[2]
	assert.Equal(t, "user", result.Role)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "tool_result", result.Content[0].Type)
	assert.Equal(t, "call_1", result.Content[0].ToolUseID)
	assert.Equal(t, "a.go\nb.go", result.Content[0].Text)
	assert.Equal(t, "completed", result.Content[0].Status)
	require.NotNil(t, result.Content[0].IsError)
	assert.False(t, *result.Content[0].IsError)

	assert.Equal(t, "end_turn", msgs[3].StopReason)
	assert.Equal(t, []RichContentBlock{{Type: "text", Text: "There are two files."}}, msgs[3].Content)
}

func TestPiParserErrorsAndUserBash(t *testing.T) {
	msgs := parsePiLines(t,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-10-04T15:00:01.000Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-10-04T15:00:02.000Z","message":{"role":"assistant","content":[],"model":"gpt-5.5","stopReason":"error","errorMessage":"429 rate limited"}}`,
		`{"type":"message","id":"b1","parentId":"a1","timestamp":"2026-10-04T15:00:03.000Z","message":{"role":"bashExecution","command":"false","output":"","exitCode":1,"cancelled":false,"truncated":false}}`,
		`{"type":"compaction","id":"c1","parentId":"b1","timestamp":"2026-10-04T15:00:04.000Z","summary":"...","firstKeptEntryId":"u1","tokensBefore":5000}`,
	)
	require.Len(t, msgs, 4)
	assert.Equal(t, []RichContentBlock{{Type: "text", Text: "hi"}}, msgs[0].Content)
	assert.Equal(t, "error", msgs[1].StopReason)
	assert.Equal(t, []RichContentBlock{{Type: "text", Text: "Error: 429 rate limited"}}, msgs[1].Content)

	assert.Equal(t, "tool_use", msgs[2].Content[0].Type)
	assert.Equal(t, "bash", msgs[2].Content[0].ToolName)
	assert.JSONEq(t, `{"command":"false"}`, string(msgs[2].Content[0].ToolInput))
	assert.Equal(t, msgs[2].Content[0].ToolUseID, msgs[3].Content[0].ToolUseID)
	assert.Equal(t, "failed", msgs[3].Content[0].Status)
	assert.Equal(t, "Exit code 1", msgs[3].Content[0].Text)
}

func TestPiSessionEvents(t *testing.T) {
	p := NewPiSessionEventParser()
	events, err := p.ParseSessionEvents([]byte(`{"type":"session","version":3,"id":"s1","timestamp":"2026-10-04T15:00:00.000Z","cwd":"/home/me/proj"}`))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "system", events[0].Kind)
	assert.Equal(t, "session started: /home/me/proj (pi)", *events[0].Content)

	events, err = p.ParseSessionEvents([]byte(`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-10-04T15:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"a.go"}}],"stopReason":"toolUse"}}`))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "text", events[0].Kind)
	assert.Equal(t, "tool_call", events[1].Kind)
	assert.Equal(t, "read", *events[1].ToolName)
	assert.Equal(t, "s1", *events[1].SessionID)
}

func TestPiSessionDirName(t *testing.T) {
	assert.Equal(t, "--home-k1dave6412--", piSessionDirName("/home/k1dave6412"))
	assert.Equal(t, "--home-me-my.proj--", piSessionDirName("/home/me/my.proj"))
	assert.Equal(t, "--C--Users-me--", piSessionDirName(`C:\Users\me`))
}

func TestPiResolver(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
	write := func(name, cwd string, startedAt time.Time) string {
		path := filepath.Join(dir, name)
		header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": name, "timestamp": startedAt.Format(time.RFC3339Nano), "cwd": cwd})
		require.NoError(t, os.WriteFile(path, append(header, '\n'), 0o600))
		require.NoError(t, os.Chtimes(path, startedAt, startedAt))
		return path
	}
	write("old.jsonl", "/proj", start.Add(-time.Hour))
	ours := write("ours.jsonl", "/proj", start.Add(2*time.Second))
	write("other-dir.jsonl", "/elsewhere", start.Add(3*time.Second))
	// A second Pi process started later in the same directory.
	write("later.jsonl", "/proj", start.Add(10*time.Second))

	r := &PiResolver{CWD: "/proj", NotBefore: start, SessionsDir: dir}
	got, err := r.Resolve()
	require.NoError(t, err)
	assert.Equal(t, ours, got)

	// Another process's older session that is still being written to is
	// not ours, however recent the write.
	r = &PiResolver{CWD: "/proj", NotBefore: start.Add(time.Minute), SessionsDir: dir}
	busy := filepath.Join(dir, "old.jsonl")
	require.NoError(t, os.Chtimes(busy, start.Add(2*time.Minute), start.Add(2*time.Minute)))
	_, err = r.Resolve()
	assert.Error(t, err)
}

func TestPiResolverRegisteredSession(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	// A session for the same directory that started after ours, written by
	// a Pi process started without our ID.
	other := filepath.Join(dir, "2026-10-04T15-00-01-000Z_other.jsonl")
	require.NoError(t, os.WriteFile(other, []byte(`{"type":"session","version":3,"id":"other","timestamp":"`+start.Add(time.Second).UTC().Format(time.RFC3339Nano)+`","cwd":"/proj"}`+"\n"), 0o600))

	const pid = 1 << 30
	RegisterPiSession(pid, PiSession{ID: "agentapi-abc123", Dir: dir})
	r := &PiResolver{PID: pid, CWD: "/proj", NotBefore: start}
	_, err := r.Resolve()
	assert.Error(t, err, "our session file doesn't exist until the first message")

	ours := filepath.Join(dir, "2026-10-04T15-00-00-000Z_agentapi-abc123.jsonl")
	require.NoError(t, os.WriteFile(ours, []byte(`{"type":"session","version":3,"id":"agentapi-abc123","timestamp":"`+start.UTC().Format(time.RFC3339Nano)+`","cwd":"/proj"}`+"\n"), 0o600))
	got, err := r.Resolve()
	require.NoError(t, err)
	assert.Equal(t, ours, got)
}
