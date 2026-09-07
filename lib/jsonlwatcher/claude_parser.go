package jsonlwatcher

import (
	"encoding/json"
	"time"
)

// ClaudeParser parses Claude Code JSONL session lines into RichMessages.
//
// Claude's JSONL format splits assistant turns across multiple lines,
// each containing one content block but sharing the same message.id.
// This parser groups them by message.id and finalizes when a new
// message.id or user line appears.
type ClaudeParser struct {
	pending       map[string]*RichMessage
	lastPendingID string
	// flushed keeps track of messages that were emitted by FlushCompleted
	// but may still receive additional content blocks. Claude stamps
	// stop_reason on every JSONL line of a turn, so FlushCompleted can
	// fire between blocks of the same message.id. When a new block
	// arrives for a flushed message, we re-attach it to pending with
	// its accumulated content intact.
	flushed map[string]*RichMessage
}

// NewClaudeParser creates a new ClaudeParser.
func NewClaudeParser() *ClaudeParser {
	return &ClaudeParser{
		pending: make(map[string]*RichMessage),
		flushed: make(map[string]*RichMessage),
	}
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

// FlushCompleted finalizes only pending messages that have a terminal stop_reason.
// Flushed messages are kept in p.flushed so that additional content blocks
// arriving for the same message.id can be appended (see handleAssistant).
func (p *ClaudeParser) FlushCompleted() []RichMessage {
	var result []RichMessage
	for id, msg := range p.pending {
		if msg.StopReason != "" {
			result = append(result, *msg)
			p.flushed[id] = msg
			delete(p.pending, id)
			if p.lastPendingID == id {
				p.lastPendingID = ""
			}
		}
	}
	return result
}

// Flush finalizes all pending assistant messages regardless of state.
func (p *ClaudeParser) Flush() []RichMessage {
	result := p.finalizePending()
	// Clear flushed map — on shutdown everything is done.
	clear(p.flushed)
	return result
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

	// If we see a new message.id, finalize the previous pending message
	// and clear any flushed entries for the old ID (they are fully done).
	if p.lastPendingID != "" && p.lastPendingID != msgID {
		completed = p.finalizePending()
		// A new message.id means the previous turn is truly complete.
		// Clear its flushed entry so it won't be re-attached.
		delete(p.flushed, p.lastPendingID)
	}

	// Get or create the pending message for this message.id.
	// Check flushed first: if this message was already emitted by
	// FlushCompleted, re-attach it so new blocks are appended to
	// the existing content rather than starting from empty.
	rich, exists := p.pending[msgID]
	if !exists {
		if prev, wasFlushed := p.flushed[msgID]; wasFlushed {
			rich = prev
			delete(p.flushed, msgID)
		} else {
			ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
			rich = &RichMessage{
				MessageID: msgID,
				Role:      "assistant",
				Model:     entry.Message.Model,
				Timestamp: ts,
			}
		}
		p.pending[msgID] = rich
	}

	// Update stop_reason and usage from the latest line
	if entry.Message.StopReason != nil {
		rich.StopReason = *entry.Message.StopReason
	}
	if entry.Message.Usage != nil {
		rich.Usage = entry.Message.Usage
	}

	// Parse the content blocks (usually one per line)
	var blocks []ContentBlock
	if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
		for _, block := range blocks {
			rich.Content = append(rich.Content, contentBlockToRich(block))
		}
	}

	p.lastPendingID = msgID
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
				block := RichContentBlock{
					Type:      "tool_result",
					ToolUseID: tr.ToolUseID,
					IsError:   tr.IsError,
					Text:      parseToolResultContent(tr.Content),
				}
				rich.Content = append(rich.Content, block)
			}
		}
	}

	return append(completed, rich)
}

func (p *ClaudeParser) finalizePending() []RichMessage {
	if len(p.pending) == 0 {
		return nil
	}
	var result []RichMessage
	for id, msg := range p.pending {
		result = append(result, *msg)
		delete(p.pending, id)
	}
	p.lastPendingID = ""
	return result
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
