package codexlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/transcript"
)

// EventParser converts Codex session-log records to timeline events.
type EventParser struct {
	sessionID string
}

// NewEventParser returns an EventParser.
func NewEventParser() *EventParser {
	return &EventParser{}
}

// ParseSessionEvents returns the timeline events of a session-log line.
func (p *EventParser) ParseSessionEvents(line []byte) ([]transcript.SessionEvent, error) {
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
		return []transcript.SessionEvent{agentlog.NewEvent("system", agentlog.Ptr("system"), &content, eventTime, agentlog.Ptr(meta.SessionID), agentlog.Ptr(meta.SessionID))}, nil
	}

	sessionID := agentlog.Optional(p.sessionID)
	sourceID := agentlog.Optional(payload.ID)
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
			role := agentlog.Optional(payload.Role)
			var text string
			if payload.Role == "user" {
				text = userText(blocks)
			} else {
				var textParts []string
				for _, block := range blocks {
					if block.Text != "" {
						textParts = append(textParts, block.Text)
					}
				}
				text = strings.Join(textParts, "\n")
			}
			if text == "" {
				return nil, nil
			}
			return []transcript.SessionEvent{agentlog.NewEvent("text", role, &text, eventTime, sessionID, nil)}, nil
		case "function_call", "custom_tool_call":
			input := payload.Args
			if payload.Type == "custom_tool_call" {
				input = codexExecInput(payload.Name, payload.Input)
			}
			event := agentlog.NewEvent("tool_call", agentlog.Ptr("assistant"), nil, eventTime, sessionID, sourceID)
			event.ToolName = agentlog.Optional(payload.Name)
			event.ToolUseID = agentlog.Optional(payload.CallID)
			event.ToolInput = agentlog.NormalizeJSON(input)
			return []transcript.SessionEvent{event}, nil
		case "function_call_output", "custom_tool_call_output":
			content := parseCodexOutput(payload.Output)
			if output, _, ok := codexExecOutput(content); ok {
				content = output
			}
			event := agentlog.NewEvent("tool_result", nil, &content, eventTime, sessionID, nil)
			event.ToolUseID = agentlog.Optional(payload.CallID)
			return []transcript.SessionEvent{event}, nil
		}
	}
	return nil, nil
}
