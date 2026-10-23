package claudelog

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
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

// ParseLine processes a single Claude JSONL line. Tool output fills most
// of a long log, so the line isn't decoded whole: its message is, into the
// blocks it becomes, and of the rest the fields the parser reads.
func (p *Parser) ParseLine(line []byte) ([]transcript.RichMessage, error) {
	typ, ok := agentlog.StringField(line, "type")
	if !ok {
		// Say what is wrong with the line.
		var entry Line
		return nil, json.Unmarshal(line, &entry)
	}
	if typ != "assistant" && typ != "user" {
		return nil, nil
	}
	message, ok := agentlog.Field(line, "message")
	if !ok || message[0] != '{' {
		return nil, nil
	}
	uuid, _ := agentlog.StringField(line, "uuid")
	timestamp, _ := agentlog.StringField(line, "timestamp")
	if typ == "assistant" {
		var m assistantMessage
		if err := decode(message, &m); err != nil {
			return nil, err
		}
		return p.handleAssistant(timestamp, &m), nil
	}
	content, _ := agentlog.Field(message, "content")
	blocks, err := userContent(content)
	if err != nil {
		return nil, err
	}
	return p.handleUser(uuid, timestamp, blocks), nil
}

// assistantMessage is the message of an assistant line.
type assistantMessage struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	Content    []ContentBlock    `json:"content"`
	StopReason *string           `json:"stop_reason"`
	Usage      *transcript.Usage `json:"usage"`
}

// decode is json.Unmarshal, which goes on past a value of another type
// than the field's (content that is a string where blocks were expected),
// and doesn't fail for one.
func decode(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return nil
	}
	return err
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

func (p *Parser) handleAssistant(timestamp string, message *assistantMessage) []transcript.RichMessage {
	msgID := message.ID
	if msgID == "" {
		return nil
	}

	var completed []transcript.RichMessage

	// A new message.id means the previous message is complete.
	if p.current != nil && p.current.MessageID != msgID {
		completed = p.finalizePending()
	}
	if p.current == nil {
		ts, _ := time.Parse(time.RFC3339Nano, timestamp)
		p.current = &transcript.RichMessage{
			MessageID: msgID,
			Role:      "assistant",
			Model:     message.Model,
			Timestamp: ts,
		}
	}
	rich := p.current
	p.emitted = false

	// Update stop_reason and usage from the latest line
	if message.StopReason != nil {
		rich.StopReason = *message.StopReason
	}
	if message.Usage != nil {
		rich.Usage = message.Usage
	}

	// The content blocks (usually one per line)
	for _, block := range message.Content {
		rich.Content = append(rich.Content, contentBlockToRich(block))
	}
	return completed
}

func (p *Parser) handleUser(uuid, timestamp string, blocks []transcript.RichContentBlock) []transcript.RichMessage {
	// Finalize any pending assistant message first
	completed := p.finalizePending()

	ts, _ := time.Parse(time.RFC3339Nano, timestamp)
	rich := transcript.RichMessage{
		MessageID: uuid,
		Role:      "user",
		Timestamp: ts,
		Content:   blocks,
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
