package claudelog

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/transcript"
)

// Parser parses Claude Code JSONL session lines into rich messages.
//
// Claude's JSONL format splits assistant turns across multiple lines,
// each containing one content block but sharing the same message.id.
// This parser groups them by message.id and finalizes when a new
// message.id or user line appears.
type Parser struct {
	// current is the assistant message being put together.
	current *transcript.RichMessage
	// emitted says FlushCompleted emitted current and it hasn't changed
	// since. Claude stamps stop_reason on every line of a turn, so a flush
	// can come between two blocks of the same message: the message stays
	// current, and is emitted again only once it changed.
	emitted bool
}

// NewParser creates a new Parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseLine processes a single Claude JSONL line.
func (p *Parser) ParseLine(line []byte) ([]transcript.RichMessage, error) {
	var entry Line
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
func (p *Parser) FlushCompleted() []transcript.RichMessage {
	if p.current == nil || p.current.StopReason == "" || p.emitted {
		return nil
	}
	p.emitted = true
	return []transcript.RichMessage{p.snapshot()}
}

// Flush finalizes the current message regardless of state.
func (p *Parser) Flush() []transcript.RichMessage {
	return p.finalizePending()
}

// snapshot is a copy of the current message whose content the parser
// doesn't share (consumers rewrite it, e.g. to store images).
func (p *Parser) snapshot() transcript.RichMessage {
	msg := *p.current
	msg.Content = slices.Clone(msg.Content)
	return msg
}

func (p *Parser) handleAssistant(entry *Line) []transcript.RichMessage {
	if entry.Message == nil {
		return nil
	}
	msgID := entry.Message.ID
	if msgID == "" {
		return nil
	}

	var completed []transcript.RichMessage

	// A new message.id means the previous message is complete.
	if p.current != nil && p.current.MessageID != msgID {
		completed = p.finalizePending()
	}
	if p.current == nil {
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		p.current = &transcript.RichMessage{
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
	rich.Content = append(rich.Content, ContentBlocks(entry.Message.Content)...)
	return completed
}

func (p *Parser) handleUser(entry *Line) []transcript.RichMessage {
	if entry.Message == nil {
		return nil
	}

	// Finalize any pending assistant message first
	completed := p.finalizePending()

	ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	rich := transcript.RichMessage{
		MessageID: entry.UUID,
		Role:      "user",
		Timestamp: ts,
	}

	content := entry.Message.Content

	// Try to parse as a string first (human prompt)
	var textContent string
	if err := json.Unmarshal(content, &textContent); err == nil {
		rich.Content = []transcript.RichContentBlock{
			{Type: "text", Text: textContent},
		}
	} else {
		// Try to parse as array of tool_result blocks
		var toolResults []ToolResultBlock
		if err := json.Unmarshal(content, &toolResults); err == nil {
			for _, tr := range toolResults {
				rich.Content = append(rich.Content, ToolResult(tr))
			}
		}
	}

	return append(completed, rich)
}

// finalizePending emits the current message, unless FlushCompleted did and
// it didn't change since, and starts a new one.
func (p *Parser) finalizePending() []transcript.RichMessage {
	if p.current == nil {
		return nil
	}
	var result []transcript.RichMessage
	if !p.emitted {
		result = []transcript.RichMessage{p.snapshot()}
	}
	p.current, p.emitted = nil, false
	return result
}

// ContentBlocks converts the content of a Claude assistant message
// (an array of content blocks) to rich content blocks. It returns nil for
// content that is not such an array.
func ContentBlocks(content json.RawMessage) []transcript.RichContentBlock {
	var blocks []ContentBlock
	if json.Unmarshal(content, &blocks) != nil {
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
