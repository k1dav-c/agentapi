package claudelog

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/transcript"
)

func TestContentBlocks(t *testing.T) {
	t.Parallel()
	blocks := ContentBlocks(json.RawMessage(`[
		{"type":"thinking","thinking":"hmm","signature":"sig"},
		{"type":"text","text":"hello"},
		{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}},
		{"type":"redacted_thinking","data":"x"}
	]`))
	require.Len(t, blocks, 4)
	assert.Equal(t, transcript.RichContentBlock{Type: "thinking", Thinking: "hmm"}, blocks[0])
	assert.Equal(t, transcript.RichContentBlock{Type: "text", Text: "hello"}, blocks[1])
	assert.Equal(t, "tool_use", blocks[2].Type)
	assert.Equal(t, "t1", blocks[2].ToolUseID)
	assert.Equal(t, "Bash", blocks[2].ToolName)
	assert.JSONEq(t, `{"command":"ls"}`, string(blocks[2].ToolInput))
	assert.Equal(t, transcript.RichContentBlock{Type: "redacted_thinking"}, blocks[3])

	assert.Nil(t, ContentBlocks(json.RawMessage(`"plain text"`)))
	assert.Nil(t, ContentBlocks(nil))
}

func TestToolResult(t *testing.T) {
	t.Parallel()
	isError := true
	block := ToolResult(ToolResultBlock{
		Type: "tool_result", ToolUseID: "t1", IsError: &isError,
		Content: json.RawMessage(`[{"type":"text","text":"see the image"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]`),
	})
	assert.Equal(t, "tool_result", block.Type)
	assert.Equal(t, "t1", block.ToolUseID)
	require.NotNil(t, block.IsError)
	assert.True(t, *block.IsError)
	assert.Equal(t, "see the image", block.Text)
	require.Len(t, block.Images, 1)
	assert.Equal(t, transcript.RichImage{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}, block.Images[0])

	block = ToolResult(ToolResultBlock{ToolUseID: "t2", Content: json.RawMessage(`"done"`)})
	assert.Equal(t, transcript.RichContentBlock{Type: "tool_result", ToolUseID: "t2", Text: "done"}, block)
}

func TestImage(t *testing.T) {
	t.Parallel()
	image, ok := Image(&ImageSource{Type: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="})
	require.True(t, ok)
	assert.Equal(t, transcript.RichImage{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}, image)

	for name, source := range map[string]*ImageSource{
		"no source":    nil,
		"url":          {Type: "url", MediaType: "image/png", Data: "https://example.com/a.png"},
		"not base64":   {Type: "base64", MediaType: "image/png", Data: "not base64!"},
		"empty base64": {Type: "base64", MediaType: "image/png"},
	} {
		_, ok := Image(source)
		assert.False(t, ok, name)
	}
}

// A user entry holds tool results, or what the user sent, or Claude
// Code's note of an interrupted turn: only tool results are tool_result
// blocks, wherever the entry is read.
func TestUserContent(t *testing.T) {
	t.Parallel()
	const png = "iVBORw0KGgo="
	lines := map[string]string{
		"interrupted":     `{"type":"user","uuid":"u1","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		"image prompt":    `{"type":"user","uuid":"u2","timestamp":"2026-10-20T10:00:01.000Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + png + `"}},{"type":"text","text":"What is this?"}]}}`,
		"result and text": `{"type":"user","uuid":"u3","timestamp":"2026-10-20T10:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"done"},{"type":"text","text":"and then"}]}}`,
	}
	want := map[string][]transcript.RichContentBlock{
		"interrupted": {{Type: "text", Text: "[Request interrupted by user for tool use]"}},
		"image prompt": {
			{Type: "image", Images: []transcript.RichImage{{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}}},
			{Type: "text", Text: "What is this?"},
		},
		"result and text": {{Type: "tool_result", ToolUseID: "toolu_1", Text: "done"}, {Type: "text", Text: "and then"}},
	}
	for name, line := range lines {
		msgs, err := NewParser().ParseLine([]byte(line))
		require.NoError(t, err, name)
		require.Len(t, msgs, 1, name)
		assert.Equal(t, want[name], msgs[0].Content, name)

		events, err := NewEventParser().ParseSessionEvents([]byte(line))
		require.NoError(t, err, name)
		var kinds []string
		for _, e := range events {
			kinds = append(kinds, e.Kind)
		}
		wantKinds := map[string][]string{"interrupted": {"text"}, "image prompt": {"text"}, "result and text": {"tool_result", "text"}}[name]
		assert.Equal(t, wantKinds, kinds, name)
	}

	block, ok := UserBlock(json.RawMessage(`{"type":"tool_result","tool_use_id":"toolu_2","is_error":true,"content":[{"type":"text","text":"failed"}]}`))
	require.True(t, ok)
	assert.Equal(t, "tool_result", block.Type)
	assert.Equal(t, "failed", block.Text)
	require.NotNil(t, block.IsError)
	assert.True(t, *block.IsError)
	_, ok = UserBlock(json.RawMessage(`{"type":`))
	assert.False(t, ok)
}

func TestPrompt(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":"Fix the bug"}}`:                                                                                                            "Fix the bug",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"x"}},{"type":"text","text":"What is this?"}]}}`: "What is this?",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`:                                                                 "",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Fix the bug"}]}}`:                                                      "",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","isMeta":true,"message":{"role":"user","content":"Skill text"}}`:                                                                                               "",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","origin":{"kind":"task-notification"},"promptSource":"system","message":{"role":"user","content":"<task-notification>done</task-notification>"}}`:              "",
		`{"type":"user","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"user","content":"<bash-input>ls</bash-input>"}}`:                                                                                            "",
		`{"type":"assistant","timestamp":"2026-10-20T10:00:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Fixed"}]}}`:                                                                               "",
	} {
		text, _, ok := Prompt([]byte(line))
		assert.Equal(t, want, text, line)
		assert.Equal(t, want != "", ok, line)
	}
}
