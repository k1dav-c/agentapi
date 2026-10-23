package pilog

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/transcript"
)

// EventParser converts Pi session lines to timeline events.
type EventParser struct {
	sessionID string
	parser    Parser
}

// NewEventParser returns an EventParser.
func NewEventParser() *EventParser {
	return &EventParser{}
}

// ParseSessionEvents returns the timeline events of a session line.
func (p *EventParser) ParseSessionEvents(line []byte) ([]transcript.SessionEvent, error) {
	var entry piEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.Type == "session" {
		p.sessionID = entry.ID
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		content := fmt.Sprintf("session started: %s (pi)", entry.CWD)
		return []transcript.SessionEvent{agentlog.NewEvent("system", agentlog.Ptr("system"), &content, ts, agentlog.Ptr(entry.ID), agentlog.Ptr(entry.ID))}, nil
	}

	messages, err := p.parser.ParseLine(line)
	if err != nil {
		return nil, err
	}
	return p.MessageEvents(messages), nil
}

// MessageEvents returns the timeline events of the messages Parser made
// from a line of the session, for callers that parse the line themselves.
func (p *EventParser) MessageEvents(messages []transcript.RichMessage) []transcript.SessionEvent {
	sessionID := agentlog.Optional(p.sessionID)
	var events []transcript.SessionEvent
	for _, msg := range messages {
		role := agentlog.Ptr(msg.Role)
		sourceID := agentlog.Optional(msg.MessageID)
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				events = append(events, agentlog.NewEvent("text", role, agentlog.Ptr(block.Text), msg.Timestamp, sessionID, sourceID))
			case "thinking":
				events = append(events, agentlog.NewEvent("thinking", role, agentlog.Ptr(block.Thinking), msg.Timestamp, sessionID, sourceID))
			case "tool_use":
				event := agentlog.NewEvent("tool_call", role, nil, msg.Timestamp, sessionID, sourceID)
				event.ToolName = agentlog.Optional(block.ToolName)
				event.ToolInput = agentlog.NormalizeJSON(block.ToolInput)
				event.ToolUseID = agentlog.Optional(block.ToolUseID)
				events = append(events, event)
			case "tool_result":
				event := agentlog.NewEvent("tool_result", role, agentlog.Ptr(block.Text), msg.Timestamp, sessionID, sourceID)
				event.ToolUseID = agentlog.Optional(block.ToolUseID)
				events = append(events, event)
			}
		}
	}
	return events
}
