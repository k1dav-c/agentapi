package jsonlwatcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CodexParser parses Codex JSONL session lines into RichMessages.
//
// Codex uses a different format from Claude:
//   - Lines have {type, timestamp, payload} envelope
//   - response_item/message with role=assistant contains output_text
//   - response_item/function_call and function_call_output for tool calls
//   - response_item/custom_tool_call and custom_tool_call_output for
//     freeform tools (e.g. the exec tool)
//   - response_item/reasoning for thinking (content is encrypted)
//   - event_msg/token_count for usage info
//   - Turns are bounded by event_msg/task_started and task_complete
//
// A Codex task can run for minutes with many tool calls before
// task_complete, so the parser emits incrementally: every appended
// content block re-emits the current turn (same MessageID, consumers
// upsert by ID), and tool results are emitted as soon as their output
// line appears.
type CodexParser struct {
	// inTurn tracks whether we're inside a task_started..task_complete boundary.
	inTurn bool
	// currentTurn accumulates content blocks for the current assistant turn.
	currentTurn *RichMessage
	// lastUsage holds the most recent token usage from token_count events.
	lastUsage *Usage
}

// NewCodexParser creates a new CodexParser.
func NewCodexParser() *CodexParser {
	return &CodexParser{}
}

// codexLine is the top-level envelope for Codex JSONL.
type codexLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// codexPayload is the shared payload structure.
type codexPayload struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Name    string          `json:"name"`      // function_call name
	CallID  string          `json:"call_id"`   // function_call/output call_id
	Content json.RawMessage `json:"content"`   // message content blocks
	Output  json.RawMessage `json:"output"`    // function_call_output
	Args    json.RawMessage `json:"arguments"` // function_call arguments
	Input   json.RawMessage `json:"input"`     // custom_tool_call input
	Status  string          `json:"status"`    // optional tool lifecycle status
	Error   json.RawMessage `json:"error"`     // optional structured tool error
	Info    *codexTokenInfo `json:"info"`      // token_count info
}

type codexTokenInfo struct {
	LastTokenUsage *codexTokenUsage `json:"last_token_usage"`
}

type codexTokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	ReasoningTokens   int `json:"reasoning_output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

// codexContentBlock is a content block in a Codex message.
type codexContentBlock struct {
	Type string `json:"type"` // input_text, output_text
	Text string `json:"text"`
}

// ParseLine processes a single Codex JSONL line.
func (p *CodexParser) ParseLine(line []byte) ([]RichMessage, error) {
	var entry codexLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}

	var payload codexPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return nil, err
	}

	switch entry.Type {
	case "event_msg":
		return p.handleEventMsg(&payload, entry.Timestamp), nil
	case "response_item":
		return p.handleResponseItem(&payload, entry.Timestamp), nil
	default:
		return nil, nil
	}
}

// FlushCompleted finalizes only completed pending turns.
// Codex turns are always complete when in pending state (each event
// is self-contained), so this behaves the same as Flush.
func (p *CodexParser) FlushCompleted() []RichMessage {
	return p.finalizeTurn()
}

// Flush finalizes any pending turn.
func (p *CodexParser) Flush() []RichMessage {
	return p.finalizeTurn()
}

func (p *CodexParser) handleEventMsg(payload *codexPayload, timestamp string) []RichMessage {
	switch payload.Type {
	case "task_started":
		// Finalize any previous turn, start a new one
		completed := p.finalizeTurn()
		p.inTurn = true
		return completed

	case "task_complete":
		// Finalize the current turn
		completed := p.finalizeTurn()
		p.inTurn = false
		return completed

	case "token_count":
		if payload.Info != nil && payload.Info.LastTokenUsage != nil {
			u := payload.Info.LastTokenUsage
			p.lastUsage = &Usage{
				InputTokens:          u.InputTokens,
				OutputTokens:         u.OutputTokens,
				CacheReadInputTokens: u.CachedInputTokens,
			}
			// Attach usage to current turn if exists
			if p.currentTurn != nil {
				p.currentTurn.Usage = p.lastUsage
			}
		}
		return nil

	default:
		return nil
	}
}

func (p *CodexParser) handleResponseItem(payload *codexPayload, timestamp string) []RichMessage {
	switch payload.Type {
	case "message":
		return p.handleMessage(payload, timestamp)
	case "function_call":
		return p.handleFunctionCall(payload, timestamp, payload.Args)
	case "custom_tool_call":
		return p.handleFunctionCall(payload, timestamp, payload.Input)
	case "function_call_output", "custom_tool_call_output":
		return p.handleFunctionCallOutput(payload, timestamp)
	case "reasoning":
		return p.handleReasoning(payload, timestamp)
	default:
		return nil
	}
}

func (p *CodexParser) handleMessage(payload *codexPayload, timestamp string) []RichMessage {
	switch payload.Role {
	case "assistant":
		// Parse content blocks to extract output_text
		var blocks []codexContentBlock
		if err := json.Unmarshal(payload.Content, &blocks); err != nil {
			return nil
		}

		var textParts []string
		for _, b := range blocks {
			if b.Type == "output_text" && b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		}
		if len(textParts) == 0 {
			return nil
		}

		text := textParts[0]
		for i := 1; i < len(textParts); i++ {
			text += "\n" + textParts[i]
		}

		p.ensureCurrentTurn(payload.ID, timestamp)
		p.currentTurn.Content = append(p.currentTurn.Content, RichContentBlock{
			Type: "text",
			Text: text,
		})
		return p.turnSnapshot()

	case "user":
		// Extract user prompt text from input_text blocks, skip system/env context
		var blocks []codexContentBlock
		if err := json.Unmarshal(payload.Content, &blocks); err != nil {
			return nil
		}

		// Find actual user input (not system context wrapped in XML tags)
		var userText string
		for _, b := range blocks {
			if b.Type == "input_text" && b.Text != "" {
				// Skip system context blocks (they start with XML-like tags)
				if len(b.Text) > 0 && b.Text[0] == '<' {
					continue
				}
				userText = b.Text
			}
		}

		if userText == "" {
			return nil
		}

		// User response_items often have no ID. Fall back to the line
		// timestamp so consumers upserting by MessageID don't collapse
		// distinct user prompts into one.
		msgID := payload.ID
		if msgID == "" {
			msgID = fmt.Sprintf("user-%s", timestamp)
		}

		ts, _ := time.Parse(time.RFC3339Nano, timestamp)
		msg := RichMessage{
			MessageID: msgID,
			Role:      "user",
			Timestamp: ts,
			Content: []RichContentBlock{
				{Type: "text", Text: userText},
			},
		}
		return []RichMessage{msg}

	default:
		// Skip developer (system prompt) and other roles
		return nil
	}
}

func (p *CodexParser) handleFunctionCall(payload *codexPayload, timestamp string, input json.RawMessage) []RichMessage {
	p.ensureCurrentTurn(fmt.Sprintf("turn-%s", payload.CallID), timestamp)

	p.currentTurn.Content = append(p.currentTurn.Content, RichContentBlock{
		Type:      "tool_use",
		ToolUseID: payload.CallID,
		ToolName:  payload.Name,
		ToolInput: input,
		Status:    "running",
	})
	return p.turnSnapshot()
}

func (p *CodexParser) handleFunctionCallOutput(payload *codexPayload, timestamp string) []RichMessage {
	text := parseCodexOutput(payload.Output)
	status, isError := codexToolResultStatus(payload)

	ts, _ := time.Parse(time.RFC3339Nano, timestamp)
	// Emit immediately: the corresponding tool_use block was already
	// emitted when the function_call line was parsed.
	return []RichMessage{{
		MessageID: fmt.Sprintf("result-%s", payload.CallID),
		Role:      "user",
		Timestamp: ts,
		Content: []RichContentBlock{
			{
				Type:      "tool_result",
				ToolUseID: payload.CallID,
				Text:      text,
				Status:    status,
				IsError:   &isError,
			},
		},
	}}
}

func codexToolResultStatus(payload *codexPayload) (string, bool) {
	switch payload.Status {
	case "failed", "incomplete", "cancelled":
		return "failed", true
	}
	if len(payload.Error) > 0 && string(payload.Error) != "null" {
		return "failed", true
	}
	return "completed", false
}

func (p *CodexParser) handleReasoning(payload *codexPayload, timestamp string) []RichMessage {
	p.ensureCurrentTurn(payload.ID, timestamp)

	// Codex reasoning content is encrypted, so we just record its existence
	p.currentTurn.Content = append(p.currentTurn.Content, RichContentBlock{
		Type:     "thinking",
		Thinking: "(encrypted)",
	})
	return p.turnSnapshot()
}

// ensureCurrentTurn creates a new assistant turn if none exists.
func (p *CodexParser) ensureCurrentTurn(msgID string, timestamp string) {
	if p.currentTurn == nil {
		ts, _ := time.Parse(time.RFC3339Nano, timestamp)
		p.currentTurn = &RichMessage{
			MessageID: msgID,
			Role:      "assistant",
			Timestamp: ts,
		}
	}
}

// turnSnapshot returns the current turn as a single-element update.
// The same MessageID is re-emitted as content accumulates; consumers
// upsert by (MessageID, Role).
func (p *CodexParser) turnSnapshot() []RichMessage {
	if p.currentTurn == nil || len(p.currentTurn.Content) == 0 {
		return nil
	}
	msg := *p.currentTurn
	if msg.Usage == nil && p.lastUsage != nil {
		msg.Usage = p.lastUsage
	}
	return []RichMessage{msg}
}

// finalizeTurn emits the final state of the current turn and resets it.
func (p *CodexParser) finalizeTurn() []RichMessage {
	result := p.turnSnapshot()
	p.currentTurn = nil
	return result
}

// parseCodexOutput extracts text from a function_call_output's or
// custom_tool_call_output's output field. The field is usually a JSON
// string; for custom tools the string itself may contain a serialized
// array of {type, text} content blocks.
func parseCodexOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if text, ok := parseCodexOutputBlocks([]byte(s)); ok {
			return text
		}
		return s
	}

	if text, ok := parseCodexOutputBlocks(raw); ok {
		return text
	}

	return string(raw)
}

// parseCodexOutputBlocks parses a serialized array of {type, text}
// content blocks and joins their text.
func parseCodexOutputBlocks(raw []byte) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return "", false
	}
	var blocks []codexContentBlock
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return "", false
	}
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n"), true
}
