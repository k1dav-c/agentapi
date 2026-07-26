package jsonlwatcher

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SessionEvent is a normalized event suitable for one-record-per-line JSONL
// export. Optional fields are omitted to match the timeline interchange format.
type SessionEvent struct {
	EventID   int             `json:"id" doc:"Monotonic event identifier within this AgentAPI run"`
	Kind      string          `json:"kind" doc:"Event kind: system, text, thinking, tool_call, or tool_result"`
	Role      *string         `json:"role,omitempty" doc:"Message role when applicable"`
	EventTime time.Time       `json:"time" doc:"Timestamp recorded by the agent session"`
	Content   *string         `json:"content,omitempty" doc:"Text or tool result content"`
	SessionID *string         `json:"session_id,omitempty" doc:"Agent session identifier"`
	SourceID  *string         `json:"source_id,omitempty" doc:"Original agent event or message identifier"`
	ToolName  *string         `json:"tool_name,omitempty" doc:"Tool name for tool_call events"`
	ToolInput json.RawMessage `json:"tool_input,omitempty" doc:"Tool input for tool_call events"`
	ToolUseID *string         `json:"tool_use_id,omitempty" doc:"Identifier pairing a tool call with its result"`
}

// SessionEventParser converts agent-specific JSONL records to SessionEvents.
type SessionEventParser interface {
	ParseSessionEvents(line []byte) ([]SessionEvent, error)
}

type CodexSessionEventParser struct {
	sessionID string
}

func NewCodexSessionEventParser() *CodexSessionEventParser {
	return &CodexSessionEventParser{}
}

func (p *CodexSessionEventParser) ParseSessionEvents(line []byte) ([]SessionEvent, error) {
	var entry codexLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	var payload codexPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return nil, err
	}
	eventTime, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)

	if entry.Type == "session_meta" {
		var meta struct {
			SessionID  string `json:"session_id"`
			CWD        string `json:"cwd"`
			CLIVersion string `json:"cli_version"`
		}
		if err := json.Unmarshal(entry.Payload, &meta); err != nil {
			return nil, err
		}
		p.sessionID = meta.SessionID
		content := fmt.Sprintf("session started: %s (codex %s)", meta.CWD, meta.CLIVersion)
		return []SessionEvent{newSessionEvent("system", strptr("system"), &content, eventTime, strptr(meta.SessionID), strptr(meta.SessionID))}, nil
	}

	sessionID := optionalString(p.sessionID)
	sourceID := optionalString(payload.ID)
	switch entry.Type {
	case "response_item":
		switch payload.Type {
		case "message":
			if payload.Role != "user" && payload.Role != "assistant" {
				return nil, nil
			}
			var blocks []codexContentBlock
			if err := json.Unmarshal(payload.Content, &blocks); err != nil {
				return nil, err
			}
			role := optionalString(payload.Role)
			var textParts []string
			for _, block := range blocks {
				if block.Text == "" {
					continue
				}
				if payload.Role == "user" && strings.HasPrefix(block.Text, "<") {
					continue
				}
				textParts = append(textParts, block.Text)
			}
			if len(textParts) == 0 {
				return nil, nil
			}
			text := strings.Join(textParts, "\n")
			return []SessionEvent{newSessionEvent("text", role, &text, eventTime, sessionID, nil)}, nil
		case "function_call", "custom_tool_call":
			input := payload.Args
			if payload.Type == "custom_tool_call" {
				input = payload.Input
			}
			event := newSessionEvent("tool_call", strptr("assistant"), nil, eventTime, sessionID, sourceID)
			event.ToolName = optionalString(payload.Name)
			event.ToolUseID = optionalString(payload.CallID)
			event.ToolInput = normalizeRawJSON(input)
			return []SessionEvent{event}, nil
		case "function_call_output", "custom_tool_call_output":
			content := parseCodexOutput(payload.Output)
			event := newSessionEvent("tool_result", nil, &content, eventTime, sessionID, nil)
			event.ToolUseID = optionalString(payload.CallID)
			return []SessionEvent{event}, nil
		}
	}
	return nil, nil
}

type ClaudeSessionEventParser struct {
	sessionID string
}

func NewClaudeSessionEventParser() *ClaudeSessionEventParser {
	return &ClaudeSessionEventParser{}
}

func (p *ClaudeSessionEventParser) ParseSessionEvents(line []byte) ([]SessionEvent, error) {
	var entry JSONLLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.SessionID != "" {
		p.sessionID = entry.SessionID
	}
	if entry.Message == nil {
		return nil, nil
	}
	eventTime, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	sessionID := optionalString(p.sessionID)
	sourceID := optionalString(entry.UUID)

	var text string
	if err := json.Unmarshal(entry.Message.Content, &text); err == nil {
		return []SessionEvent{newSessionEvent("text", optionalString(entry.Message.Role), &text, eventTime, sessionID, nil)}, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(entry.Message.Content, &blocks); err == nil {
		events := make([]SessionEvent, 0, len(blocks))
		for _, block := range blocks {
			switch block.Type {
			case "text":
				content := block.Text
				events = append(events, newSessionEvent("text", optionalString(entry.Message.Role), &content, eventTime, sessionID, nil))
			case "thinking":
				if block.Thinking != "" {
					content := block.Thinking
					events = append(events, newSessionEvent("thinking", optionalString(entry.Message.Role), &content, eventTime, sessionID, sourceID))
				}
			case "tool_use":
				event := newSessionEvent("tool_call", strptr("assistant"), nil, eventTime, sessionID, sourceID)
				event.ToolName = optionalString(block.Name)
				event.ToolUseID = optionalString(block.ID)
				event.ToolInput = normalizeRawJSON(block.Input)
				events = append(events, event)
			}
		}
		if len(events) > 0 {
			return events, nil
		}
	}

	var results []ToolResultBlock
	if err := json.Unmarshal(entry.Message.Content, &results); err == nil {
		events := make([]SessionEvent, 0, len(results))
		for _, result := range results {
			content := parseToolResultContent(result.Content)
			event := newSessionEvent("tool_result", nil, &content, eventTime, sessionID, nil)
			event.ToolUseID = optionalString(result.ToolUseID)
			events = append(events, event)
		}
		return events, nil
	}
	return nil, nil
}

func newSessionEvent(kind string, role, content *string, eventTime time.Time, sessionID, sourceID *string) SessionEvent {
	return SessionEvent{
		Kind:      kind,
		Role:      role,
		Content:   content,
		SessionID: sessionID,
		SourceID:  sourceID,
		EventTime: eventTime,
	}
}

func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		encoded, _ := json.Marshal(string(raw))
		return encoded
	}
	if text, ok := value.(string); ok {
		var nested any
		if json.Unmarshal([]byte(text), &nested) == nil {
			encoded, _ := json.Marshal(nested)
			return encoded
		}
	}
	return raw
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func strptr(value string) *string {
	return &value
}
