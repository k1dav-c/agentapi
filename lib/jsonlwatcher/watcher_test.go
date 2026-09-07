package jsonlwatcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// staticResolver is a test helper that returns a fixed path.
type staticResolver struct {
	path string
}

func (r *staticResolver) Resolve() (string, error) {
	return r.path, nil
}

type switchingResolver struct {
	mu   sync.RWMutex
	path string
}

func (r *switchingResolver) Resolve() (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path, nil
}

func (r *switchingResolver) setPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.path = path
}

func TestWatcherSwitchesSessionFiles(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "foreground.jsonl")
	secondPath := filepath.Join(tmpDir, "parked.jsonl")
	firstLine := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-05T00:00:00Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"file_path":"one"}}],"stop_reason":"tool_use"}}` + "\n"
	secondLine := `{"type":"assistant","uuid":"a2","timestamp":"2026-09-05T00:00:01Z","message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"tool_2","name":"Write","input":{"file_path":"two"}}],"stop_reason":"tool_use"}}` + "\n"
	if err := os.WriteFile(firstPath, []byte(firstLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(secondLine), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := &switchingResolver{path: firstPath}
	var mu sync.Mutex
	var messages []RichMessage
	lineCount := 0
	w := New(Config{
		Resolver: resolver,
		Parser:   NewClaudeParser(),
		OnMessage: func(message RichMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, message)
		},
		OnLine: func([]byte) {
			mu.Lock()
			defer mu.Unlock()
			lineCount++
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	waitFor := func(condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			done := condition()
			mu.Unlock()
			if done {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("timed out waiting for watcher")
	}

	waitFor(func() bool { return lineCount == 1 && len(messages) >= 1 })
	resolver.setPath(secondPath)
	waitFor(func() bool { return lineCount == 2 && len(messages) >= 2 })

	if got := messages[len(messages)-1].Content[0].ToolUseID; got != "tool_2" {
		t.Fatalf("last tool use ID = %q, want tool_2", got)
	}
}

func TestClaudeParser_ContentBlocks(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected []ContentBlock
	}{
		{
			name: "text block",
			raw:  `[{"type":"text","text":"hello world"}]`,
			expected: []ContentBlock{
				{Type: "text", Text: "hello world"},
			},
		},
		{
			name: "thinking block",
			raw:  `[{"type":"thinking","thinking":"let me think","signature":"sig-1"}]`,
			expected: []ContentBlock{
				{Type: "thinking", Thinking: "let me think", Signature: "sig-1"},
			},
		},
		{
			name: "tool_use block",
			raw:  `[{"type":"tool_use","id":"toolu_001","name":"Bash","input":{"command":"ls"}}]`,
			expected: []ContentBlock{
				{Type: "tool_use", ID: "toolu_001", Name: "Bash"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var blocks []ContentBlock
			if err := json.Unmarshal([]byte(tt.raw), &blocks); err != nil {
				t.Fatal(err)
			}
			if len(blocks) != len(tt.expected) {
				t.Fatalf("expected %d blocks, got %d", len(tt.expected), len(blocks))
			}
			for i, block := range blocks {
				if block.Type != tt.expected[i].Type {
					t.Errorf("block[%d].Type = %q, want %q", i, block.Type, tt.expected[i].Type)
				}
			}
		})
	}
}

func TestContentBlockToRich(t *testing.T) {
	tests := []struct {
		name     string
		input    ContentBlock
		expected RichContentBlock
	}{
		{
			name:     "text",
			input:    ContentBlock{Type: "text", Text: "hello"},
			expected: RichContentBlock{Type: "text", Text: "hello"},
		},
		{
			name:     "thinking",
			input:    ContentBlock{Type: "thinking", Thinking: "hmm"},
			expected: RichContentBlock{Type: "thinking", Thinking: "hmm"},
		},
		{
			name:     "tool_use",
			input:    ContentBlock{Type: "tool_use", ID: "t1", Name: "Bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			expected: RichContentBlock{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contentBlockToRich(tt.input)
			if got.Type != tt.expected.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.expected.Type)
			}
			if got.Text != tt.expected.Text {
				t.Errorf("Text = %q, want %q", got.Text, tt.expected.Text)
			}
			if got.Thinking != tt.expected.Thinking {
				t.Errorf("Thinking = %q, want %q", got.Thinking, tt.expected.Thinking)
			}
			if got.ToolUseID != tt.expected.ToolUseID {
				t.Errorf("ToolUseID = %q, want %q", got.ToolUseID, tt.expected.ToolUseID)
			}
			if got.ToolName != tt.expected.ToolName {
				t.Errorf("ToolName = %q, want %q", got.ToolName, tt.expected.ToolName)
			}
		})
	}
}

func TestParseToolResultContent(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "string content", raw: `"hello"`, expected: "hello"},
		{name: "array content", raw: `[{"type":"text","text":"output here"}]`, expected: "output here"},
		{name: "empty", raw: ``, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseToolResultContent(json.RawMessage(tt.raw))
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestClaudeParser_AssistantGrouping(t *testing.T) {
	parser := NewClaudeParser()

	// Three assistant lines with same message.id (one turn)
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"thinking","thinking":"let me think"}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-07-24T09:35:26.100Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"Hello!"}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-07-24T09:35:26.200Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
	}

	var allCompleted []RichMessage
	for _, line := range lines {
		completed, _ := parser.ParseLine([]byte(line))
		allCompleted = append(allCompleted, completed...)
	}

	// Nothing finalized yet (all same message.id, no trigger)
	if len(allCompleted) != 0 {
		t.Fatalf("expected 0 completed during same message.id, got %d", len(allCompleted))
	}

	// A user message triggers finalization
	completed, _ := parser.ParseLine([]byte(`{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:27.000Z","message":{"role":"user","content":"thanks"}}`))

	if len(completed) != 2 { // 1 assistant (finalized) + 1 user
		t.Fatalf("expected 2 messages after user, got %d", len(completed))
	}

	assistantMsg := completed[0]
	if assistantMsg.MessageID != "msg_001" {
		t.Errorf("MessageID = %q, want msg_001", assistantMsg.MessageID)
	}
	if len(assistantMsg.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(assistantMsg.Content))
	}
	if assistantMsg.Content[0].Type != "thinking" {
		t.Errorf("content[0].Type = %q, want thinking", assistantMsg.Content[0].Type)
	}
	if assistantMsg.Content[1].Type != "text" {
		t.Errorf("content[1].Type = %q, want text", assistantMsg.Content[1].Type)
	}
	if assistantMsg.Content[2].Type != "tool_use" {
		t.Errorf("content[2].Type = %q, want tool_use", assistantMsg.Content[2].Type)
	}
}

func TestClaudeParser_UserToolResult(t *testing.T) {
	parser := NewClaudeParser()

	line := `{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:27.000Z","sourceToolAssistantUUID":"a3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_001","is_error":null,"content":[{"type":"text","text":"hi\n"}]}]},"toolUseResult":{"stdout":"hi\n"}}`
	completed, _ := parser.ParseLine([]byte(line))

	if len(completed) != 1 {
		t.Fatalf("expected 1 message, got %d", len(completed))
	}
	if completed[0].Content[0].Type != "tool_result" {
		t.Errorf("content[0].Type = %q, want tool_result", completed[0].Content[0].Type)
	}
	if completed[0].Content[0].ToolUseID != "toolu_001" {
		t.Errorf("content[0].ToolUseID = %q, want toolu_001", completed[0].Content[0].ToolUseID)
	}
}

func TestClaudeParser_DifferentMessageIDs(t *testing.T) {
	parser := NewClaudeParser()

	completed1, _ := parser.ParseLine([]byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"first turn"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10}}}`))
	if len(completed1) != 0 {
		t.Fatalf("expected 0 completed from first line, got %d", len(completed1))
	}

	completed2, _ := parser.ParseLine([]byte(`{"type":"assistant","uuid":"a2","timestamp":"2026-07-24T09:35:28.000Z","message":{"id":"msg_002","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"second turn"}],"stop_reason":"end_turn","usage":{"input_tokens":200,"output_tokens":20}}}`))

	// msg_001 should be finalized when msg_002 arrived
	if len(completed2) != 1 {
		t.Fatalf("expected 1 completed (msg_001 finalized), got %d", len(completed2))
	}
	if completed2[0].MessageID != "msg_001" {
		t.Errorf("MessageID = %q, want msg_001", completed2[0].MessageID)
	}
}

func TestTailFile(t *testing.T) {
	tmpDir := t.TempDir()
	jsonlPath := filepath.Join(tmpDir, "test.jsonl")

	// Collect emitted messages, upserting by (MessageID, Role) the same
	// way the EventEmitter deduplicates downstream.
	var emitted []RichMessage
	var mu sync.Mutex
	collect := func(msg RichMessage) {
		mu.Lock()
		defer mu.Unlock()
		for i := len(emitted) - 1; i >= 0; i-- {
			if emitted[i].MessageID == msg.MessageID && emitted[i].Role == msg.Role {
				emitted[i] = msg
				return
			}
		}
		emitted = append(emitted, msg)
	}
	messages := func() []RichMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]RichMessage(nil), emitted...)
	}

	w := New(Config{
		Resolver:  &staticResolver{path: jsonlPath},
		Parser:    NewClaudeParser(),
		OnMessage: collect,
	})

	// Write initial content
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:19.000Z","message":{"role":"user","content":"hello"}}` + "\n")
	f.Sync()
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.tailFile(ctx, jsonlPath)
	time.Sleep(500 * time.Millisecond)

	msgs := messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after initial read, got %d", len(msgs))
	}

	// Append more data
	f, _ = os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10}}}` + "\n")
	f.Sync()
	f.Close()
	time.Sleep(500 * time.Millisecond)

	// Trigger finalization with another user message
	f, _ = os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"type":"user","uuid":"u2","timestamp":"2026-07-24T09:35:27.000Z","message":{"role":"user","content":"thanks"}}` + "\n")
	f.Sync()
	f.Close()
	time.Sleep(500 * time.Millisecond)

	msgs = messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after append, got %d", len(msgs))
	}

	cancel()
}

func TestProcessSampleFile(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	parser := NewClaudeParser()
	var allMessages []RichMessage

	start := 0
	for i := range data {
		if data[i] == '\n' {
			if i > start {
				completed, _ := parser.ParseLine(data[start:i])
				allMessages = append(allMessages, completed...)
			}
			start = i + 1
		}
	}
	allMessages = append(allMessages, parser.Flush()...)

	// Expected: user, assistant(3 blocks grouped), user(tool_result), assistant
	if len(allMessages) != 4 {
		t.Fatalf("expected 4 messages from sample, got %d", len(allMessages))
	}

	if allMessages[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user", allMessages[0].Role)
	}
	if allMessages[1].Role != "assistant" {
		t.Errorf("msgs[1].Role = %q, want assistant", allMessages[1].Role)
	}
	if allMessages[1].MessageID != "msg_001" {
		t.Errorf("msgs[1].MessageID = %q, want msg_001", allMessages[1].MessageID)
	}
	if len(allMessages[1].Content) != 3 {
		t.Fatalf("msgs[1] expected 3 content blocks, got %d", len(allMessages[1].Content))
	}
	if allMessages[2].Content[0].Type != "tool_result" {
		t.Errorf("msgs[2].Content[0].Type = %q, want tool_result", allMessages[2].Content[0].Type)
	}
	if allMessages[3].StopReason != "end_turn" {
		t.Errorf("msgs[3].StopReason = %q, want end_turn", allMessages[3].StopReason)
	}
}

func TestEncodeCWD(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/home/k1dave6412", "-home-k1dave6412"},
		{"/", "-"},
		{"/home/user/projects/myapp", "-home-user-projects-myapp"},
	}

	for _, tt := range tests {
		got := encodeCWD(tt.input)
		if got != tt.expected {
			t.Errorf("encodeCWD(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
