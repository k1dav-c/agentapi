package claudelog

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/coder/agentapi/lib/transcript"
)

// userBlock is a block of a user message's content: a tool result, text
// or an image.
type userBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// ToolUseID, IsError and Content are a tool_result's.
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	// Source is an image's.
	Source *ImageSource `json:"source,omitempty"`
}

// rich converts b to a rich content block.
func (b userBlock) rich() transcript.RichContentBlock {
	switch b.Type {
	case "tool_result":
		return ToolResult(ToolResultBlock{Type: b.Type, ToolUseID: b.ToolUseID, IsError: b.IsError, Content: b.Content})
	case "text":
		return transcript.RichContentBlock{Type: "text", Text: b.Text}
	case "image":
		block := transcript.RichContentBlock{Type: "image"}
		if image, ok := Image(b.Source); ok {
			block.Images = []transcript.RichImage{image}
		}
		return block
	default:
		return transcript.RichContentBlock{Type: b.Type, Text: b.Text}
	}
}

// UserBlocks converts the content of a user message to rich content
// blocks: a string is a text block, and an array's blocks are converted
// each by its type. A user message holds the results of the agent's tool
// calls, or what the user sent (text, and images), or a note of Claude
// Code's (an interrupted turn); only the tool results are tool_result
// blocks. It returns nil for content that is neither.
func UserBlocks(content json.RawMessage) []transcript.RichContentBlock {
	switch {
	case len(content) == 0:
		return nil
	case content[0] == '"':
		var text string
		if json.Unmarshal(content, &text) != nil {
			return nil
		}
		return []transcript.RichContentBlock{{Type: "text", Text: text}}
	}
	var blocks []userBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	rich := make([]transcript.RichContentBlock, 0, len(blocks))
	for _, b := range blocks {
		rich = append(rich, b.rich())
	}
	return rich
}

// UserBlock converts one block of a user message's content, as UserBlocks
// does. It returns false for a block that doesn't decode.
func UserBlock(raw json.RawMessage) (transcript.RichContentBlock, bool) {
	var b userBlock
	if err := json.Unmarshal(raw, &b); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return transcript.RichContentBlock{}, false
		}
	}
	return b.rich(), true
}

// ContentBlocks converts the content of a Claude assistant message
// (an array of content blocks) to rich content blocks. It returns nil for
// content that is not such an array.
func ContentBlocks(content json.RawMessage) []transcript.RichContentBlock {
	var blocks []ContentBlock
	if len(content) == 0 || content[0] != '[' || json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	rich := make([]transcript.RichContentBlock, 0, len(blocks))
	for _, block := range blocks {
		rich = append(rich, contentBlockToRich(block))
	}
	return rich
}

// ToolResult converts a tool_result block of a Claude user message to
// a rich content block with its text and images.
func ToolResult(tr ToolResultBlock) transcript.RichContentBlock {
	return transcript.RichContentBlock{
		Type:      "tool_result",
		ToolUseID: tr.ToolUseID,
		IsError:   tr.IsError,
		Text:      ToolResultText(tr.Content),
		Images:    parseToolResultImages(tr.Content),
	}
}

// Image decodes the image of an image content block. It returns false
// for a source that isn't base64 data and for data that doesn't decode.
func Image(source *ImageSource) (transcript.RichImage, bool) {
	if source == nil || source.Type != "base64" {
		return transcript.RichImage{}, false
	}
	data, err := base64.StdEncoding.DecodeString(source.Data)
	if err != nil || len(data) == 0 {
		return transcript.RichImage{}, false
	}
	return transcript.RichImage{MediaType: source.MediaType, Data: data}, true
}

// contentBlockToRich converts a parsed ContentBlock to a rich content block.
func contentBlockToRich(block ContentBlock) transcript.RichContentBlock {
	switch block.Type {
	case "text":
		return transcript.RichContentBlock{Type: "text", Text: block.Text}
	case "thinking":
		return transcript.RichContentBlock{Type: "thinking", Thinking: block.Thinking}
	case "tool_use":
		return transcript.RichContentBlock{
			Type:      "tool_use",
			ToolUseID: block.ID,
			ToolName:  block.Name,
			ToolInput: block.Input,
		}
	default:
		return transcript.RichContentBlock{Type: block.Type, Text: block.Text}
	}
}

// parseToolResultImages returns the base64 images in a tool_result
// content array (Claude Code's Read returns an image file this way).
// Images that don't decode are skipped.
func parseToolResultImages(raw json.RawMessage) []transcript.RichImage {
	var blocks []ToolResultContent
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var images []transcript.RichImage
	for _, b := range blocks {
		if b.Type != "image" {
			continue
		}
		if image, ok := Image(b.Source); ok {
			images = append(images, image)
		}
	}
	return images
}

// ToolResultText extracts text from a tool_result content field,
// which can be a string or an array of {type, text} objects.
func ToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	var blocks []ToolResultContent
	if raw[0] == '[' && json.Unmarshal(raw, &blocks) == nil {
		var texts []string
		for _, b := range blocks {
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return string(raw)
}
