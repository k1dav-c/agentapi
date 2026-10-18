package jsonlwatcher

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"
)

// ClaudeParser parses Claude Code JSONL session lines into RichMessages.
//
// Claude's JSONL format splits assistant turns across multiple lines,
// each containing one content block but sharing the same message.id.
// This parser groups them by message.id and finalizes when a new
// message.id or user line appears.
type ClaudeParser struct {
	// current is the assistant message being put together.
	current *RichMessage
	// emitted says FlushCompleted emitted current and it hasn't changed
	// since. Claude stamps stop_reason on every line of a turn, so a flush
	// can come between two blocks of the same message: the message stays
	// current, and is emitted again only once it changed.
	emitted bool
}

// NewClaudeParser creates a new ClaudeParser.
func NewClaudeParser() *ClaudeParser {
	return &ClaudeParser{}
}

// ParseLine processes a single Claude JSONL line.
func (p *ClaudeParser) ParseLine(line []byte) ([]RichMessage, error) {
	var entry JSONLLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}

	switch entry.Type {
	case "assistant":
		return p.handleAssistant(&entry), nil
	case "user":
		return p.handleUser(&entry), nil
	default:
		return nil, nil
	}
}

// FlushCompleted emits the current message once it has a stop_reason. It
// stays current, so blocks that follow are added to it.
func (p *ClaudeParser) FlushCompleted() []RichMessage {
	if p.current == nil || p.current.StopReason == "" || p.emitted {
		return nil
	}
	p.emitted = true
	return []RichMessage{p.snapshot()}
}

// Flush finalizes the current message regardless of state.
func (p *ClaudeParser) Flush() []RichMessage {
	return p.finalizePending()
}

// snapshot is a copy of the current message whose content the parser
// doesn't share (consumers rewrite it, e.g. to store images).
func (p *ClaudeParser) snapshot() RichMessage {
	msg := *p.current
	msg.Content = slices.Clone(msg.Content)
	return msg
}

func (p *ClaudeParser) handleAssistant(entry *JSONLLine) []RichMessage {
	if entry.Message == nil {
		return nil
	}
	msgID := entry.Message.ID
	if msgID == "" {
		return nil
	}

	var completed []RichMessage

	// A new message.id means the previous message is complete.
	if p.current != nil && p.current.MessageID != msgID {
		completed = p.finalizePending()
	}
	if p.current == nil {
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		p.current = &RichMessage{
			MessageID: msgID,
			Role:      "assistant",
			Model:     entry.Message.Model,
			Timestamp: ts,
		}
	}
	rich := p.current
	p.emitted = false

	// Update stop_reason and usage from the latest line
	if entry.Message.StopReason != nil {
		rich.StopReason = *entry.Message.StopReason
	}
	if entry.Message.Usage != nil {
		rich.Usage = entry.Message.Usage
	}

	// Parse the content blocks (usually one per line)
	rich.Content = append(rich.Content, ClaudeContentBlocks(entry.Message.Content)...)
	return completed
}

func (p *ClaudeParser) handleUser(entry *JSONLLine) []RichMessage {
	if entry.Message == nil {
		return nil
	}

	// Finalize any pending assistant message first
	completed := p.finalizePending()

	ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	rich := RichMessage{
		MessageID: entry.UUID,
		Role:      "user",
		Timestamp: ts,
	}

	content := entry.Message.Content

	// Try to parse as a string first (human prompt)
	var textContent string
	if err := json.Unmarshal(content, &textContent); err == nil {
		rich.Content = []RichContentBlock{
			{Type: "text", Text: textContent},
		}
	} else {
		// Try to parse as array of tool_result blocks
		var toolResults []ToolResultBlock
		if err := json.Unmarshal(content, &toolResults); err == nil {
			for _, tr := range toolResults {
				rich.Content = append(rich.Content, ClaudeToolResult(tr))
			}
		}
	}

	return append(completed, rich)
}

// finalizePending emits the current message, unless FlushCompleted did and
// it didn't change since, and starts a new one.
func (p *ClaudeParser) finalizePending() []RichMessage {
	if p.current == nil {
		return nil
	}
	var result []RichMessage
	if !p.emitted {
		result = []RichMessage{p.snapshot()}
	}
	p.current, p.emitted = nil, false
	return result
}

// ClaudeContentBlocks converts the content of a Claude assistant message
// (an array of content blocks) to rich content blocks. It returns nil for
// content that is not such an array.
func ClaudeContentBlocks(content json.RawMessage) []RichContentBlock {
	var blocks []ContentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	rich := make([]RichContentBlock, 0, len(blocks))
	for _, block := range blocks {
		rich = append(rich, contentBlockToRich(block))
	}
	return rich
}

// ClaudeToolResult converts a tool_result block of a Claude user message to
// a rich content block with its text and images.
func ClaudeToolResult(tr ToolResultBlock) RichContentBlock {
	return RichContentBlock{
		Type:      "tool_result",
		ToolUseID: tr.ToolUseID,
		IsError:   tr.IsError,
		Text:      parseToolResultContent(tr.Content),
		Images:    parseToolResultImages(tr.Content),
	}
}

// ClaudeImage decodes the image of an image content block. It returns false
// for a source that isn't base64 data and for data that doesn't decode.
func ClaudeImage(source *ImageSource) (RichImage, bool) {
	if source == nil || source.Type != "base64" {
		return RichImage{}, false
	}
	data, err := base64.StdEncoding.DecodeString(source.Data)
	if err != nil || len(data) == 0 {
		return RichImage{}, false
	}
	return RichImage{MediaType: source.MediaType, Data: data}, true
}

// contentBlockToRich converts a parsed ContentBlock to a RichContentBlock.
func contentBlockToRich(block ContentBlock) RichContentBlock {
	switch block.Type {
	case "text":
		return RichContentBlock{Type: "text", Text: block.Text}
	case "thinking":
		return RichContentBlock{Type: "thinking", Thinking: block.Thinking}
	case "tool_use":
		return RichContentBlock{
			Type:      "tool_use",
			ToolUseID: block.ID,
			ToolName:  block.Name,
			ToolInput: block.Input,
		}
	default:
		return RichContentBlock{Type: block.Type, Text: block.Text}
	}
}

// parseToolResultImages returns the base64 images in a tool_result
// content array (Claude Code's Read returns an image file this way).
// Images that don't decode are skipped.
func parseToolResultImages(raw json.RawMessage) []RichImage {
	var blocks []ToolResultContent
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var images []RichImage
	for _, b := range blocks {
		if b.Type != "image" {
			continue
		}
		if image, ok := ClaudeImage(b.Source); ok {
			images = append(images, image)
		}
	}
	return images
}

// parseToolResultContent extracts text from a tool_result content field,
// which can be a string or an array of {type, text} objects.
func parseToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	var blocks []ToolResultContent
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var texts []string
		for _, b := range blocks {
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		if len(texts) == 1 {
			return texts[0]
		}
		result := ""
		for i, t := range texts {
			if i > 0 {
				result += "\n"
			}
			result += t
		}
		return result
	}

	return string(raw)
}
