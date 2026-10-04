package jsonlwatcher

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PiParser converts Pi coding agent session lines into RichMessages.
//
// Pi appends each message to the session file once it is complete, one
// "message" entry per line, so every line maps to a finished message and
// nothing is held back. Assistant messages carry text, thinking and toolCall
// blocks; tool output arrives as separate "toolResult" messages, which are
// emitted as user messages with tool_result blocks like Claude Code's.
type PiParser struct{}

func NewPiParser() *PiParser {
	return &PiParser{}
}

// piEntry is one line of a Pi session file.
type piEntry struct {
	Type      string     `json:"type"`
	ID        string     `json:"id"`
	Timestamp string     `json:"timestamp"`
	CWD       string     `json:"cwd"`
	Message   *piMessage `json:"message"`
}

type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Model        string          `json:"model"`
	Provider     string          `json:"provider"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *piUsage        `json:"usage"`
	Timestamp    int64           `json:"timestamp"`

	// toolResult
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`

	// bashExecution (commands the user runs with "!")
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exitCode"`
}

type piUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

type piContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Redacted  bool            `json:"redacted"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// piStopReasons maps Pi's stop reasons to the Anthropic names the chat UI
// understands.
var piStopReasons = map[string]string{
	"stop":    "end_turn",
	"length":  "max_tokens",
	"toolUse": "tool_use",
	"error":   "error",
	"aborted": "aborted",
}

func (p *PiParser) ParseLine(line []byte) ([]RichMessage, error) {
	var entry piEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.Type != "message" || entry.Message == nil {
		return nil, nil
	}
	msg := entry.Message
	ts := piTimestamp(entry.Timestamp, msg.Timestamp)

	switch msg.Role {
	case "user":
		text := piText(msg.Content)
		if text == "" {
			return nil, nil
		}
		return []RichMessage{{
			MessageID: entry.ID,
			Role:      "user",
			Timestamp: ts,
			Content:   []RichContentBlock{{Type: "text", Text: text}},
		}}, nil

	case "assistant":
		rich := RichMessage{
			MessageID:  entry.ID,
			Role:       "assistant",
			Model:      msg.Model,
			StopReason: piStopReasons[msg.StopReason],
			Timestamp:  ts,
		}
		if msg.Usage != nil {
			rich.Usage = &Usage{
				InputTokens:              msg.Usage.Input,
				OutputTokens:             msg.Usage.Output,
				CacheReadInputTokens:     msg.Usage.CacheRead,
				CacheCreationInputTokens: msg.Usage.CacheWrite,
			}
		}
		var blocks []piContentBlock
		_ = json.Unmarshal(msg.Content, &blocks)
		for _, block := range blocks {
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) != "" {
					rich.Content = append(rich.Content, RichContentBlock{Type: "text", Text: block.Text})
				}
			case "thinking":
				// Redacted thinking is encrypted; there is nothing to show.
				if !block.Redacted && strings.TrimSpace(block.Thinking) != "" {
					rich.Content = append(rich.Content, RichContentBlock{Type: "thinking", Thinking: block.Thinking})
				}
			case "toolCall":
				rich.Content = append(rich.Content, RichContentBlock{
					Type:      "tool_use",
					ToolUseID: block.ID,
					ToolName:  block.Name,
					ToolInput: block.Arguments,
				})
			}
		}
		if msg.StopReason == "error" && msg.ErrorMessage != "" {
			rich.Content = append(rich.Content, RichContentBlock{Type: "text", Text: "Error: " + msg.ErrorMessage})
		}
		if len(rich.Content) == 0 {
			return nil, nil
		}
		return []RichMessage{rich}, nil

	case "toolResult":
		isError := msg.IsError
		status := "completed"
		if isError {
			status = "failed"
		}
		return []RichMessage{{
			MessageID: entry.ID,
			Role:      "user",
			Timestamp: ts,
			Content: []RichContentBlock{{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Text:      piText(msg.Content),
				Status:    status,
				IsError:   &isError,
			}},
		}}, nil

	case "bashExecution":
		// A command the user ran with "!": show it as a bash call and its
		// output, like a tool call the agent made.
		callID := "bash-" + entry.ID
		input, _ := json.Marshal(map[string]string{"command": msg.Command})
		isError := msg.ExitCode != nil && *msg.ExitCode != 0
		status := "completed"
		if isError {
			status = "failed"
		}
		output := strings.TrimRight(msg.Output, "\n")
		if isError {
			output = strings.TrimLeft(fmt.Sprintf("%s\nExit code %d", output, *msg.ExitCode), "\n")
		}
		return []RichMessage{
			{
				MessageID: entry.ID,
				Role:      "assistant",
				Timestamp: ts,
				Content:   []RichContentBlock{{Type: "tool_use", ToolUseID: callID, ToolName: "bash", ToolInput: input}},
			},
			{
				MessageID: "result-" + entry.ID,
				Role:      "user",
				Timestamp: ts,
				Content: []RichContentBlock{{
					Type: "tool_result", ToolUseID: callID, Text: output, Status: status, IsError: &isError,
				}},
			},
		}, nil
	}
	return nil, nil
}

// FlushCompleted has nothing to do: Pi writes complete messages.
func (p *PiParser) FlushCompleted() []RichMessage { return nil }

// Flush has nothing to do: Pi writes complete messages.
func (p *PiParser) Flush() []RichMessage { return nil }

// piText joins the text of a Pi content field, which is either a string or
// an array of content blocks (images are skipped).
func piText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []piContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// piTimestamp prefers the entry's ISO timestamp and falls back to the
// message's Unix milliseconds.
func piTimestamp(iso string, unixMs int64) time.Time {
	if ts, err := time.Parse(time.RFC3339Nano, iso); err == nil {
		return ts
	}
	if unixMs > 0 {
		return time.UnixMilli(unixMs).UTC()
	}
	return time.Time{}
}

// PiSessionEventParser converts Pi session lines to SessionEvents.
type PiSessionEventParser struct {
	sessionID string
	parser    PiParser
}

func NewPiSessionEventParser() *PiSessionEventParser {
	return &PiSessionEventParser{}
}

func (p *PiSessionEventParser) ParseSessionEvents(line []byte) ([]SessionEvent, error) {
	var entry piEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.Type == "session" {
		p.sessionID = entry.ID
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		content := fmt.Sprintf("session started: %s (pi)", entry.CWD)
		return []SessionEvent{newSessionEvent("system", strptr("system"), &content, ts, strptr(entry.ID), strptr(entry.ID))}, nil
	}

	messages, err := p.parser.ParseLine(line)
	if err != nil {
		return nil, err
	}
	sessionID := optionalString(p.sessionID)
	var events []SessionEvent
	for _, msg := range messages {
		role := strptr(msg.Role)
		sourceID := optionalString(msg.MessageID)
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				events = append(events, newSessionEvent("text", role, strptr(block.Text), msg.Timestamp, sessionID, sourceID))
			case "thinking":
				events = append(events, newSessionEvent("thinking", role, strptr(block.Thinking), msg.Timestamp, sessionID, sourceID))
			case "tool_use":
				event := newSessionEvent("tool_call", role, nil, msg.Timestamp, sessionID, sourceID)
				event.ToolName = optionalString(block.ToolName)
				event.ToolInput = normalizeRawJSON(block.ToolInput)
				event.ToolUseID = optionalString(block.ToolUseID)
				events = append(events, event)
			case "tool_result":
				event := newSessionEvent("tool_result", role, strptr(block.Text), msg.Timestamp, sessionID, sourceID)
				event.ToolUseID = optionalString(block.ToolUseID)
				events = append(events, event)
			}
		}
	}
	return events, nil
}
