package claudelog

import (
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

	rich.Content = UserBlocks(entry.Message.Content)
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
