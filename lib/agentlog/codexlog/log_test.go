package codexlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexSessionEventParser(t *testing.T) {
	parser := NewEventParser()

	events, err := parser.ParseSessionEvents([]byte(`{"type":"session_meta","timestamp":"2026-07-24T10:35:26.383Z","payload":{"session_id":"session-1","cwd":"/work","cli_version":"0.144.6"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "system" {
		t.Fatalf("unexpected session event: %#v", events)
	}
	if events[0].SessionID == nil || *events[0].SessionID != "session-1" {
		t.Fatalf("SessionID = %#v, want session-1", events[0].SessionID)
	}

	events, err = parser.ParseSessionEvents([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.300Z","payload":{"type":"function_call","id":"fc_1","name":"exec_command","call_id":"call_1","arguments":"{\"cmd\":\"ls\"}"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "tool_call" {
		t.Fatalf("unexpected tool event: %#v", events)
	}
	if events[0].ToolName == nil || *events[0].ToolName != "exec_command" {
		t.Fatalf("ToolName = %#v", events[0].ToolName)
	}
	var input map[string]string
	if err := json.Unmarshal(events[0].ToolInput, &input); err != nil {
		t.Fatal(err)
	}
	if input["cmd"] != "ls" {
		t.Fatalf("ToolInput = %#v", input)
	}
	if events[0].SourceID == nil || *events[0].SourceID != "fc_1" {
		t.Fatalf("SourceID = %#v", events[0].SourceID)
	}

	events, err = parser.ParseSessionEvents([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:29.000Z","payload":{"type":"function_call_output","id":"out_1","call_id":"call_1","output":"main.go"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "tool_result" {
		t.Fatalf("unexpected result event: %#v", events)
	}
	if events[0].ToolUseID == nil || *events[0].ToolUseID != "call_1" {
		t.Fatalf("ToolUseID = %#v", events[0].ToolUseID)
	}
	if events[0].Role != nil || len(events[0].ToolInput) != 0 || events[0].SourceID != nil {
		t.Fatalf("tool result optional fields should be absent: %#v", events[0])
	}
}

func TestCodexSessionName(t *testing.T) {
	index := filepath.Join(t.TempDir(), "session_index.jsonl")
	n := &Namer{indexPath: index}
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
}

func TestResolverHonorsTheAgentsHome(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	root, err := SessionsRoot("")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(codexHome, "sessions"), root)
}
