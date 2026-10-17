package jsonlwatcher

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeContentBlocks(t *testing.T) {
	t.Parallel()
	blocks := ClaudeContentBlocks(json.RawMessage(`[
		{"type":"thinking","thinking":"hmm","signature":"sig"},
		{"type":"text","text":"hello"},
		{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}},
		{"type":"redacted_thinking","data":"x"}
	]`))
	require.Len(t, blocks, 4)
	assert.Equal(t, RichContentBlock{Type: "thinking", Thinking: "hmm"}, blocks[0])
	assert.Equal(t, RichContentBlock{Type: "text", Text: "hello"}, blocks[1])
	assert.Equal(t, "tool_use", blocks[2].Type)
	assert.Equal(t, "t1", blocks[2].ToolUseID)
	assert.Equal(t, "Bash", blocks[2].ToolName)
	assert.JSONEq(t, `{"command":"ls"}`, string(blocks[2].ToolInput))
	assert.Equal(t, RichContentBlock{Type: "redacted_thinking"}, blocks[3])

	assert.Nil(t, ClaudeContentBlocks(json.RawMessage(`"plain text"`)))
	assert.Nil(t, ClaudeContentBlocks(nil))
}

func TestClaudeToolResult(t *testing.T) {
	t.Parallel()
	isError := true
	block := ClaudeToolResult(ToolResultBlock{
		Type: "tool_result", ToolUseID: "t1", IsError: &isError,
		Content: json.RawMessage(`[{"type":"text","text":"see the image"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]`),
	})
	assert.Equal(t, "tool_result", block.Type)
	assert.Equal(t, "t1", block.ToolUseID)
	require.NotNil(t, block.IsError)
	assert.True(t, *block.IsError)
	assert.Equal(t, "see the image", block.Text)
	require.Len(t, block.Images, 1)
	assert.Equal(t, RichImage{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}, block.Images[0])

	block = ClaudeToolResult(ToolResultBlock{ToolUseID: "t2", Content: json.RawMessage(`"done"`)})
	assert.Equal(t, RichContentBlock{Type: "tool_result", ToolUseID: "t2", Text: "done"}, block)
}

func TestClaudeImage(t *testing.T) {
	t.Parallel()
	image, ok := ClaudeImage(&ImageSource{Type: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="})
	require.True(t, ok)
	assert.Equal(t, RichImage{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n")}, image)

	for name, source := range map[string]*ImageSource{
		"no source":    nil,
		"url":          {Type: "url", MediaType: "image/png", Data: "https://example.com/a.png"},
		"not base64":   {Type: "base64", MediaType: "image/png", Data: "not base64!"},
		"empty base64": {Type: "base64", MediaType: "image/png"},
	} {
		_, ok := ClaudeImage(source)
		assert.False(t, ok, name)
	}
}
