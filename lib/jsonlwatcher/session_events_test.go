package jsonlwatcher

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCodexSessionEventParser(t *testing.T) {
	parser := NewCodexSessionEventParser()

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

func TestSessionEventJSONLShape(t *testing.T) {
	event := SessionEvent{
		EventID:   17,
		Kind:      "system",
		Role:      strptr("system"),
		Content:   strptr("session switched"),
		EventTime: mustParseTime(t, "2026-07-17T03:23:14.380896751Z"),
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":17,"kind":"system","role":"system","time":"2026-07-17T03:23:14.380896751Z","content":"session switched"}`
	if string(data) != want {
		t.Fatalf("event JSON = %s, want %s", data, want)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestClaudeSessionEventParser(t *testing.T) {
	parser := NewClaudeSessionEventParser()
	events, err := parser.ParseSessionEvents([]byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26Z","sessionId":"session-1","message":{"id":"msg","role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"ls"}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "tool_call" {
		t.Fatalf("unexpected event: %#v", events)
	}
	if events[0].ToolUseID == nil || *events[0].ToolUseID != "tool_1" {
		t.Fatalf("ToolUseID = %#v", events[0].ToolUseID)
	}
}
