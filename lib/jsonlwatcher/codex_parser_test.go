package jsonlwatcher

import (
	"os"
	"testing"
)

// applyRich mimics the watcher's upsert-by-(MessageID, Role) behavior so
// tests can assert on the assembled message list. The Codex parser
// re-emits the current turn as content accumulates.
func applyRich(list []RichMessage, msgs []RichMessage) []RichMessage {
	for _, msg := range msgs {
		idx := -1
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].MessageID == msg.MessageID && list[i].Role == msg.Role {
				idx = i
				break
			}
		}
		if idx >= 0 {
			list[idx] = msg
		} else {
			list = append(list, msg)
		}
	}
	return list
}

func TestCodexParser_BasicTurn(t *testing.T) {
	parser := NewCodexParser()
	var all []RichMessage

	// task_started
	completed, _ := parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:27.000Z","payload":{"type":"task_started"}}`))
	all = applyRich(all, completed)

	// user message (with env context to skip)
	completed, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:27.200Z","payload":{"type":"message","id":"user-1","role":"user","content":[{"type":"input_text","text":"<env>system</env>"},{"type":"input_text","text":"List files"}]}}`))
	all = applyRich(all, completed)

	if len(all) != 1 {
		t.Fatalf("expected 1 user message, got %d", len(all))
	}
	if all[0].Role != "user" {
		t.Errorf("Role = %q, want user", all[0].Role)
	}
	if all[0].Content[0].Text != "List files" {
		t.Errorf("user text = %q, want 'List files'", all[0].Content[0].Text)
	}

	// reasoning (encrypted) — starts the turn and emits an update
	completed, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.000Z","payload":{"type":"reasoning","id":"rs_001","encrypted_content":"gAAA"}}`))
	all = applyRich(all, completed)

	// assistant message — updates the same turn
	completed, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.200Z","payload":{"type":"message","id":"msg_001","role":"assistant","content":[{"type":"output_text","text":"Let me check."}]}}`))
	all = applyRich(all, completed)

	// function_call — updates the same turn with a tool_use block
	completed, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.300Z","payload":{"type":"function_call","id":"fc_001","name":"exec_command","call_id":"call_abc","arguments":"{\"cmd\":\"ls\"}"}}`))
	all = applyRich(all, completed)

	// The turn is visible incrementally (before task_complete)
	if len(all) != 2 { // user + in-progress assistant turn
		t.Fatalf("expected 2 messages before tool output, got %d", len(all))
	}

	// function_call_output — emits the tool_result immediately
	completed, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:29.000Z","payload":{"type":"function_call_output","id":"fco_001","call_id":"call_abc","output":"main.go"}}`))
	all = applyRich(all, completed)

	if len(all) != 3 { // user + assistant turn + tool_result
		t.Fatalf("expected 3 messages after tool output, got %d", len(all))
	}

	// token_count
	completed, _ = parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:29.100Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":500,"cached_input_tokens":200,"output_tokens":100}}}}`))
	all = applyRich(all, completed)

	// task_complete — re-emits the final turn (upserted, no new entry)
	completed, _ = parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:31.000Z","payload":{"type":"task_complete"}}`))
	all = applyRich(all, completed)

	// Expected order: [0] user, [1] assistant (thinking+text+tool_use), [2] tool_result
	if len(all) != 3 {
		t.Fatalf("expected 3 messages total, got %d", len(all))
	}

	// [0] user message
	if all[0].Role != "user" {
		t.Errorf("all[0].Role = %q, want user", all[0].Role)
	}

	// [1] assistant turn — should come BEFORE tool_result
	assistantMsg := all[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("all[1].Role = %q, want assistant", assistantMsg.Role)
	}
	if len(assistantMsg.Content) != 3 { // reasoning + text + tool_use
		t.Fatalf("expected 3 content blocks in assistant, got %d", len(assistantMsg.Content))
	}
	if assistantMsg.Content[0].Type != "thinking" {
		t.Errorf("content[0].Type = %q, want thinking", assistantMsg.Content[0].Type)
	}
	if assistantMsg.Content[1].Type != "text" {
		t.Errorf("content[1].Type = %q, want text", assistantMsg.Content[1].Type)
	}
	if assistantMsg.Content[1].Text != "Let me check." {
		t.Errorf("content[1].Text = %q, want 'Let me check.'", assistantMsg.Content[1].Text)
	}
	if assistantMsg.Content[2].Type != "tool_use" {
		t.Errorf("content[2].Type = %q, want tool_use", assistantMsg.Content[2].Type)
	}
	if assistantMsg.Content[2].ToolName != "exec_command" {
		t.Errorf("content[2].ToolName = %q, want exec_command", assistantMsg.Content[2].ToolName)
	}
	if assistantMsg.Content[2].Status != "running" {
		t.Errorf("content[2].Status = %q, want running", assistantMsg.Content[2].Status)
	}
	// Check usage was attached at finalization
	if assistantMsg.Usage == nil {
		t.Fatal("expected usage to be set")
	}
	if assistantMsg.Usage.InputTokens != 500 {
		t.Errorf("Usage.InputTokens = %d, want 500", assistantMsg.Usage.InputTokens)
	}

	// [2] tool_result — should come AFTER assistant
	if all[2].Role != "user" {
		t.Errorf("all[2].Role = %q, want user", all[2].Role)
	}
	if all[2].Content[0].Type != "tool_result" {
		t.Errorf("all[2].Content[0].Type = %q, want tool_result", all[2].Content[0].Type)
	}
	if all[2].Content[0].ToolUseID != "call_abc" {
		t.Errorf("ToolUseID = %q, want call_abc", all[2].Content[0].ToolUseID)
	}
	if all[2].Content[0].Text != "main.go" {
		t.Errorf("tool_result text = %q, want 'main.go'", all[2].Content[0].Text)
	}
	if all[2].Content[0].Status != "completed" {
		t.Errorf("tool_result status = %q, want completed", all[2].Content[0].Status)
	}
	if all[2].Content[0].IsError == nil || *all[2].Content[0].IsError {
		t.Error("completed tool_result should have is_error=false")
	}
}

func TestCodexParser_FailedToolResult(t *testing.T) {
	parser := NewCodexParser()

	msgs, err := parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:29.000Z","payload":{"type":"function_call_output","call_id":"call_failed","status":"failed","error":{"message":"permission denied"},"output":"permission denied"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || len(msgs[0].Content) != 1 {
		t.Fatalf("expected one tool result, got %#v", msgs)
	}
	result := msgs[0].Content[0]
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
	if result.IsError == nil || !*result.IsError {
		t.Error("failed tool_result should have is_error=true")
	}
}

func TestCodexParser_SkipsDeveloperRole(t *testing.T) {
	parser := NewCodexParser()

	completed, _ := parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:27.100Z","payload":{"type":"message","id":"sys-1","role":"developer","content":[{"type":"input_text","text":"You are Codex."}]}}`))

	if len(completed) != 0 {
		t.Fatalf("expected 0 messages for developer role, got %d", len(completed))
	}
}

func TestCodexParser_CustomToolCall(t *testing.T) {
	parser := NewCodexParser()
	var all []RichMessage

	c, _ := parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:27.000Z","payload":{"type":"task_started"}}`))
	all = applyRich(all, c)

	// custom_tool_call (e.g. the exec tool) uses "input" instead of "arguments"
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.000Z","payload":{"type":"custom_tool_call","id":"ct_1","name":"exec","call_id":"call_custom","input":"const r = await tools.exec_command({cmd:\"ls\"})"}}`))
	all = applyRich(all, c)

	if len(all) != 1 {
		t.Fatalf("expected 1 message (assistant turn), got %d", len(all))
	}
	if all[0].Content[0].Type != "tool_use" {
		t.Fatalf("content[0].Type = %q, want tool_use", all[0].Content[0].Type)
	}
	if all[0].Content[0].ToolName != "exec" {
		t.Errorf("ToolName = %q, want exec", all[0].Content[0].ToolName)
	}
	if string(all[0].Content[0].ToolInput) == "" {
		t.Error("expected ToolInput to be set from the input field")
	}

	// custom_tool_call_output's output is a string containing a
	// serialized array of content blocks
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:29.000Z","payload":{"type":"custom_tool_call_output","id":"cto_1","call_id":"call_custom","output":"[{\"type\":\"input_text\",\"text\":\"Script completed\\nOutput:\\n\"},{\"type\":\"input_text\",\"text\":\"file1.go\"}]"}}`))
	all = applyRich(all, c)

	if len(all) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(all))
	}
	result := all[1]
	if result.Content[0].Type != "tool_result" {
		t.Fatalf("content[0].Type = %q, want tool_result", result.Content[0].Type)
	}
	if result.Content[0].ToolUseID != "call_custom" {
		t.Errorf("ToolUseID = %q, want call_custom", result.Content[0].ToolUseID)
	}
	want := "Script completed\nOutput:\n\nfile1.go"
	if result.Content[0].Text != want {
		t.Errorf("tool_result text = %q, want %q", result.Content[0].Text, want)
	}
}

func TestCodexParser_SampleFile(t *testing.T) {
	data, err := os.ReadFile("testdata/codex_sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	parser := NewCodexParser()
	var allMessages []RichMessage

	start := 0
	for i := range data {
		if data[i] == '\n' {
			if i > start {
				completed, _ := parser.ParseLine(data[start:i])
				allMessages = applyRich(allMessages, completed)
			}
			start = i + 1
		}
	}
	allMessages = applyRich(allMessages, parser.Flush())

	// Verify ordering: assistant turns appear before their tool_results
	assistantIdx := make(map[string]int) // tool_use_id -> index of assistant turn
	for i, m := range allMessages {
		if m.Role == "assistant" {
			for _, c := range m.Content {
				if c.Type == "tool_use" {
					assistantIdx[c.ToolUseID] = i
				}
			}
		}
		if m.Role == "user" {
			for _, c := range m.Content {
				if c.Type != "tool_result" {
					continue
				}
				aIdx, ok := assistantIdx[c.ToolUseID]
				if !ok {
					t.Errorf("tool_result %q has no preceding tool_use", c.ToolUseID)
					continue
				}
				if i < aIdx {
					t.Errorf("tool_result at index %d appears before its assistant turn at index %d", i, aIdx)
				}
			}
		}
	}

	// Verify we have both user and assistant messages
	var userCount, assistantCount, toolResultCount int
	for _, m := range allMessages {
		switch m.Role {
		case "user":
			for _, c := range m.Content {
				if c.Type == "tool_result" {
					toolResultCount++
				}
			}
			userCount++
		case "assistant":
			assistantCount++
		}
	}

	if userCount < 1 {
		t.Errorf("expected at least 1 user message, got %d", userCount)
	}
	if assistantCount < 1 {
		t.Errorf("expected at least 1 assistant turn, got %d", assistantCount)
	}
	if toolResultCount < 1 {
		t.Errorf("expected at least 1 tool_result, got %d", toolResultCount)
	}

	// Assistant turn should have usage
	for _, m := range allMessages {
		if m.Role == "assistant" && m.Usage == nil {
			t.Error("assistant turn has no usage")
			break
		}
	}
}

func TestCodexParser_Flush(t *testing.T) {
	parser := NewCodexParser()
	var all []RichMessage

	// Start a turn but don't complete it
	c, _ := parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:27.000Z","payload":{"type":"task_started"}}`))
	all = applyRich(all, c)
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.200Z","payload":{"type":"message","id":"msg_001","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`))
	all = applyRich(all, c)

	// Flush should finalize the pending turn
	all = applyRich(all, parser.Flush())
	if len(all) != 1 {
		t.Fatalf("expected 1 message, got %d", len(all))
	}
	if all[0].Content[0].Text != "Hello" {
		t.Errorf("flushed text = %q, want Hello", all[0].Content[0].Text)
	}
}

func TestCodexParser_MultipleToolCalls(t *testing.T) {
	parser := NewCodexParser()
	var all []RichMessage

	// Start turn
	c, _ := parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:27.000Z","payload":{"type":"task_started"}}`))
	all = applyRich(all, c)

	// Assistant text
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.000Z","payload":{"type":"message","id":"msg_001","role":"assistant","content":[{"type":"output_text","text":"Running commands..."}]}}`))
	all = applyRich(all, c)

	// Two function calls and their outputs
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.100Z","payload":{"type":"function_call","id":"fc_1","name":"exec_command","call_id":"call_1","arguments":"{\"cmd\":\"ls\"}"}}`))
	all = applyRich(all, c)
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.200Z","payload":{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":"file1.go"}}`))
	all = applyRich(all, c)
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.300Z","payload":{"type":"function_call","id":"fc_2","name":"exec_command","call_id":"call_2","arguments":"{\"cmd\":\"cat file1.go\"}"}}`))
	all = applyRich(all, c)
	c, _ = parser.ParseLine([]byte(`{"type":"response_item","timestamp":"2026-07-24T10:35:28.400Z","payload":{"type":"function_call_output","id":"fco_2","call_id":"call_2","output":"package main"}}`))
	all = applyRich(all, c)

	// task_complete
	c, _ = parser.ParseLine([]byte(`{"type":"event_msg","timestamp":"2026-07-24T10:35:29.000Z","payload":{"type":"task_complete"}}`))
	all = applyRich(all, c)

	// Expected: [0] assistant (text + tool_use + tool_use), [1] tool_result call_1, [2] tool_result call_2
	if len(all) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(all))
	}

	// Assistant should have text + 2 tool_use blocks
	if all[0].Role != "assistant" {
		t.Errorf("all[0].Role = %q, want assistant", all[0].Role)
	}
	if len(all[0].Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(all[0].Content))
	}
	if all[0].Content[0].Type != "text" {
		t.Errorf("content[0].Type = %q, want text", all[0].Content[0].Type)
	}
	if all[0].Content[1].Type != "tool_use" {
		t.Errorf("content[1].Type = %q, want tool_use", all[0].Content[1].Type)
	}
	if all[0].Content[2].Type != "tool_use" {
		t.Errorf("content[2].Type = %q, want tool_use", all[0].Content[2].Type)
	}

	// Tool results should follow
	if all[1].Content[0].ToolUseID != "call_1" {
		t.Errorf("all[1] ToolUseID = %q, want call_1", all[1].Content[0].ToolUseID)
	}
	if all[2].Content[0].ToolUseID != "call_2" {
		t.Errorf("all[2] ToolUseID = %q, want call_2", all[2].Content[0].ToolUseID)
	}
}
