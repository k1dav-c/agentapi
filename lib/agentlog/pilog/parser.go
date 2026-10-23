// Package pilog reads Pi's session logs: the rich messages, timeline
// events, prompts and session name their records hold, and where Pi keeps
// them.
package pilog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/transcript"
)

// Parser converts Pi coding agent session lines into rich messages.
//
// Pi appends each message to the session file once it is complete, one
// "message" entry per line, so every line maps to a finished message and
// nothing is held back. Assistant messages carry text, thinking and toolCall
// blocks; tool output arrives as separate "toolResult" messages, which are
// emitted as user messages with tool_result blocks like Claude Code's.
type Parser struct{}

// NewParser returns a Parser.
func NewParser() *Parser {
	return &Parser{}
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

// ParseLine converts a session line into rich messages.
func (p *Parser) ParseLine(line []byte) ([]transcript.RichMessage, error) {
	var entry piEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.Type != "message" || entry.Message == nil {
		return nil, nil
	}
	msg := entry.Message
	ts := Timestamp(entry.Timestamp, msg.Timestamp)

	switch msg.Role {
	case "user":
		text := Text(msg.Content)
		if text == "" {
			return nil, nil
		}
		return []transcript.RichMessage{{
			MessageID: entry.ID,
			Role:      "user",
			Timestamp: ts,
			Content:   []transcript.RichContentBlock{{Type: "text", Text: text}},
		}}, nil

	case "assistant":
		rich := transcript.RichMessage{
			MessageID:  entry.ID,
			Role:       "assistant",
			Model:      msg.Model,
			StopReason: piStopReasons[msg.StopReason],
			Timestamp:  ts,
		}
		if msg.Usage != nil {
			rich.Usage = &transcript.Usage{
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
					rich.Content = append(rich.Content, transcript.RichContentBlock{Type: "text", Text: block.Text})
				}
			case "thinking":
				// Redacted thinking is encrypted; there is nothing to show.
				if !block.Redacted && strings.TrimSpace(block.Thinking) != "" {
					rich.Content = append(rich.Content, transcript.RichContentBlock{Type: "thinking", Thinking: block.Thinking})
				}
			case "toolCall":
				rich.Content = append(rich.Content, transcript.RichContentBlock{
					Type:      "tool_use",
					ToolUseID: block.ID,
					ToolName:  block.Name,
					ToolInput: block.Arguments,
				})
			}
		}
		if msg.StopReason == "error" && msg.ErrorMessage != "" {
			rich.Content = append(rich.Content, transcript.RichContentBlock{Type: "text", Text: "Error: " + msg.ErrorMessage})
		}
		if len(rich.Content) == 0 {
			return nil, nil
		}
		return []transcript.RichMessage{rich}, nil

	case "toolResult":
		isError := msg.IsError
		status := "completed"
		if isError {
			status = "failed"
		}
		return []transcript.RichMessage{{
			MessageID: entry.ID,
			Role:      "user",
			Timestamp: ts,
			Content: []transcript.RichContentBlock{{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Text:      Text(msg.Content),
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
		return []transcript.RichMessage{
			{
				MessageID: entry.ID,
				Role:      "assistant",
				Timestamp: ts,
				Content:   []transcript.RichContentBlock{{Type: "tool_use", ToolUseID: callID, ToolName: "bash", ToolInput: input}},
			},
			{
				MessageID: "result-" + entry.ID,
				Role:      "user",
				Timestamp: ts,
				Content: []transcript.RichContentBlock{{
					Type: "tool_result", ToolUseID: callID, Text: output, Status: status, IsError: &isError,
				}},
			},
		}, nil
	}
	return nil, nil
}

// FlushCompleted has nothing to do: Pi writes complete messages.
func (p *Parser) FlushCompleted() []transcript.RichMessage { return nil }

// Flush has nothing to do: Pi writes complete messages.
func (p *Parser) Flush() []transcript.RichMessage { return nil }

// Text joins the text of a Pi content field, which is either a string or
// an array of content blocks (images are skipped).
func Text(raw json.RawMessage) string {
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

// Timestamp prefers the entry's ISO timestamp and falls back to the
// message's Unix milliseconds.
func Timestamp(iso string, unixMs int64) time.Time {
	if ts, err := time.Parse(time.RFC3339Nano, iso); err == nil {
		return ts
	}
	if unixMs > 0 {
		return time.UnixMilli(unixMs).UTC()
	}
	return time.Time{}
}
