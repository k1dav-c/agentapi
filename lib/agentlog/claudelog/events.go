package claudelog

import (
	"encoding/json"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/transcript"
)

// EventParser converts Claude Code session-log records to timeline events.
type EventParser struct {
	sessionID string
}

// NewEventParser returns an EventParser.
func NewEventParser() *EventParser {
	return &EventParser{}
}

// ParseSessionEvents returns the timeline events of a session-log line.
func (p *EventParser) ParseSessionEvents(line []byte) ([]transcript.SessionEvent, error) {
	var entry Line
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		// Claude Code also logs the API requests it makes
		// (api-request-blob), whose message is what it sent the model:
		// system reminders and tool lists, not the conversation.
		return nil, nil
	}
	return p.Events(entry), nil
}

// Events returns the timeline events of one session-log entry. The native
// Claude adapter calls it with the entries of its stream.
func (p *EventParser) Events(entry Line) []transcript.SessionEvent {
	if entry.SessionID != "" {
		p.sessionID = entry.SessionID
	}
	if entry.Message == nil {
		return nil
	}
	eventTime, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	sessionID := agentlog.Optional(p.sessionID)
	sourceID := agentlog.Optional(entry.UUID)

	var text string
	if err := json.Unmarshal(entry.Message.Content, &text); err == nil {
		return []transcript.SessionEvent{agentlog.NewEvent("text", agentlog.Optional(entry.Message.Role), &text, eventTime, sessionID, nil)}
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
		events := make([]transcript.SessionEvent, 0, len(blocks))
		for _, block := range blocks {
			switch block.Type {
			case "text":
				content := block.Text
				events = append(events, agentlog.NewEvent("text", agentlog.Optional(entry.Message.Role), &content, eventTime, sessionID, nil))
			case "thinking":
				if block.Thinking != "" {
					content := block.Thinking
					events = append(events, agentlog.NewEvent("thinking", agentlog.Optional(entry.Message.Role), &content, eventTime, sessionID, sourceID))
				}
			case "tool_use":
				event := agentlog.NewEvent("tool_call", agentlog.Ptr("assistant"), nil, eventTime, sessionID, sourceID)
				event.ToolName = agentlog.Optional(block.Name)
				event.ToolUseID = agentlog.Optional(block.ID)
				event.ToolInput = agentlog.NormalizeJSON(block.Input)
				events = append(events, event)
			}
		}
		if len(events) > 0 {
			return events
		}
	}

	var results []ToolResultBlock
	if err := json.Unmarshal(entry.Message.Content, &results); err == nil {
		events := make([]transcript.SessionEvent, 0, len(results))
		for _, result := range results {
			content := ToolResultText(result.Content)
			event := agentlog.NewEvent("tool_result", nil, &content, eventTime, sessionID, nil)
			event.ToolUseID = agentlog.Optional(result.ToolUseID)
			events = append(events, event)
		}
		return events
	}
	return nil
}
