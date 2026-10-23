package claudelog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/transcript"
)

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
		expected transcript.RichContentBlock
	}{
		{
			name:     "text",
			input:    ContentBlock{Type: "text", Text: "hello"},
			expected: transcript.RichContentBlock{Type: "text", Text: "hello"},
		},
		{
			name:     "thinking",
			input:    ContentBlock{Type: "thinking", Thinking: "hmm"},
			expected: transcript.RichContentBlock{Type: "thinking", Thinking: "hmm"},
		},
		{
			name:     "tool_use",
			input:    ContentBlock{Type: "tool_use", ID: "t1", Name: "Bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
			expected: transcript.RichContentBlock{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash"},
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
			got := ToolResultText(json.RawMessage(tt.raw))
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestClaudeParser_AssistantGrouping(t *testing.T) {
	parser := NewParser()

	// Three assistant lines with same message.id (one turn)
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"thinking","thinking":"let me think"}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-07-24T09:35:26.100Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"Hello!"}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-07-24T09:35:26.200Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}}`,
	}

	var allCompleted []transcript.RichMessage
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
	parser := NewParser()

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
	parser := NewParser()

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

func TestProcessSampleFile(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	parser := NewParser()
	var allMessages []transcript.RichMessage

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
		{"/home/k1dave6412/.cache/rich-proj", "-home-k1dave6412--cache-rich-proj"},
		{"/home/user/cpt_sft baseline", "-home-user-cpt-sft-baseline"},
	}

	for _, tt := range tests {
		got := EncodeCWD(tt.input)
		if got != tt.expected {
			t.Errorf("EncodeCWD(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestClaudeParser_ToolResultImage(t *testing.T) {
	parser := NewParser()

	// What Claude Code logs when Read opens an image file.
	line := `{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:27.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_img","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"not base64!"}}]}]}}`
	completed, _ := parser.ParseLine([]byte(line))

	if len(completed) != 1 || len(completed[0].Content) != 1 {
		t.Fatalf("expected 1 message with 1 block, got %+v", completed)
	}
	block := completed[0].Content[0]
	if block.Text != "" {
		t.Errorf("Text = %q, want empty", block.Text)
	}
	if len(block.Images) != 1 {
		t.Fatalf("expected 1 image (the undecodable one skipped), got %d", len(block.Images))
	}
	if block.Images[0].MediaType != "image/png" || string(block.Images[0].Data) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("image = %q %q", block.Images[0].MediaType, block.Images[0].Data)
	}
}

func TestClaudeParserHoldsOneMessage(t *testing.T) {
	p := NewParser()
	for i := range 100 {
		line := fmt.Sprintf(`{"type":"assistant","uuid":"u%d","timestamp":"2025-01-01T00:00:00Z","message":{"id":"msg_%d","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"hello %d"}]}}`, i, i, i)
		_, err := p.ParseLine([]byte(line))
		require.NoError(t, err)
		p.FlushCompleted() // after every read, as the watcher does
	}
	require.NotNil(t, p.current)
	require.Equal(t, "msg_99", p.current.MessageID, "only the message being put together is kept")
}

func TestClaudeSessionEventParser(t *testing.T) {
	parser := NewEventParser()
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

func TestClaudeSessionName(t *testing.T) {
	n := NewNamer()
	name, changed := n.Name([]byte(`{"type":"user","message":{"content":"hi"}}`))
	assert.False(t, changed)
	assert.Empty(t, name)

	name, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Agentapi 實作調整","sessionId":"s1"}`))
	assert.True(t, changed)
	assert.Equal(t, "Agentapi 實作調整", name)
	_, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Agentapi 實作調整","sessionId":"s1"}`))
	assert.False(t, changed, "the same title again")

	name, changed = n.Name([]byte(`{"type":"custom-title","customTitle":"Release prep","sessionId":"s1"}`))
	assert.True(t, changed)
	assert.Equal(t, "Release prep", name)
	name, changed = n.Name([]byte(`{"type":"ai-title","aiTitle":"Something else","sessionId":"s1"}`))
	assert.False(t, changed, "a /rename title wins over generated ones")
	assert.Equal(t, "Release prep", name)
}

func TestResolverHonorsTheAgentsHome(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "sessions"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "sessions", "4242.json"), []byte(`{"cwd":"/work/app","sessionId":"s-1"}`), 0o644))
	project := filepath.Join(configDir, "projects", EncodeCWD("/work/app"))
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "s-1.jsonl"), nil, 0o644))
	path, err := (&Resolver{PID: 4242}).Resolve()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(project, "s-1.jsonl"), path)
}

// The names Claude Code 2.1.302 gives these directories' project
// directories, from its own code.
func TestProjectDirName(t *testing.T) {
	t.Parallel()
	for cwd, want := range map[string]string{
		"/home/me/work":                "-home-me-work",
		"/" + strings.Repeat("a", 250): "-" + strings.Repeat("a", 199) + "-feo44x",
		"/home/me/" + strings.Repeat("projects/very-long-directory-name_", 8) + "end": "-home-me-" + strings.Repeat("projects-very-long-directory-name-", 5) + "projects-very-long-di-1iwkgz",
		"/tmp/😀/" + strings.Repeat("x", 210):                                          "-tmp----" + strings.Repeat("x", 192) + "-nwb7yz",
		"/home/me/日本語/" + strings.Repeat("y", 205):                                    "-home-me-----" + strings.Repeat("y", 187) + "-pfvhxx",
	} {
		assert.Equal(t, want, ProjectDirName(cwd), cwd)
	}
	assert.Equal(t, "-tmp----x", EncodeCWD("/tmp/😀/x"), "two dashes for a character outside the BMP, as in UTF-16")
}

func TestProjectDirsAndLogsByID(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	long := "/" + strings.Repeat("a", 250)
	dirs, err := ProjectDirs(long)
	require.NoError(t, err)
	require.Len(t, dirs, 2)
	assert.Equal(t, filepath.Join(config, "projects", ProjectDirName(long)), dirs[0])
	assert.Equal(t, filepath.Join(config, "projects", EncodeCWD(long)), dirs[1], "an older Claude Code's whole name")
	dirs, err = ProjectDirs("/" + strings.Repeat("a", 300))
	require.NoError(t, err)
	assert.Len(t, dirs, 1, "a name too long for a file system")

	// Another version's hash.
	other := filepath.Join(config, "projects", EncodeCWD(long)[:200]+"-zzz")
	require.NoError(t, os.MkdirAll(other, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(other, "s1.jsonl"), []byte("{}\n"), 0o600))
	dirs, err = ProjectDirs(long)
	require.NoError(t, err)
	assert.Contains(t, dirs, other)
	dirs, err = ProjectDirs("/work")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(config, "projects", "-work")}, dirs)

	assert.Equal(t, []string{filepath.Join(other, "s1.jsonl")}, LogsByID("s1"))
	assert.Empty(t, LogsByID("s2"))
	assert.Empty(t, LogsByID("../s1"))
	assert.Empty(t, LogsByID("*"))
}
