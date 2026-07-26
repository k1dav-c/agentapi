package jsonlwatcher

import (
	"encoding/json"
	"time"
)

// --- JSONL line parsing types (input from Claude Code session files) ---

// JSONLLine is the raw envelope parsed from each line of the JSONL file.
type JSONLLine struct {
	Type       string        `json:"type"`
	UUID       string        `json:"uuid,omitempty"`
	ParentUUID *string       `json:"parentUuid,omitempty"`
	Timestamp  string        `json:"timestamp,omitempty"`
	SessionID  string        `json:"sessionId,omitempty"`
	Message    *JSONLMessage `json:"message,omitempty"`

	// For user messages with tool results
	ToolUseResult           json.RawMessage `json:"toolUseResult,omitempty"`
	SourceToolAssistantUUID string          `json:"sourceToolAssistantUUID,omitempty"`

	// For user messages from humans
	PromptSource string       `json:"promptSource,omitempty"`
	Origin       *JSONLOrigin `json:"origin,omitempty"`
}

// JSONLOrigin indicates the source of a user message.
type JSONLOrigin struct {
	Kind string `json:"kind"` // "human" for typed prompts
}

// JSONLMessage represents the message field within a JSONL line.
type JSONLMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Model      string          `json:"model,omitempty"`
	Content    json.RawMessage `json:"content"` // string (user prompt) or []ContentBlock
	StopReason *string         `json:"stop_reason,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}

// ContentBlock represents a single content block in an assistant message.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`    // tool_use ID
	Name      string          `json:"name,omitempty"`  // tool_use name
	Input     json.RawMessage `json:"input,omitempty"` // tool_use input
}

// ToolResultContent represents a content block inside a tool_result.
type ToolResultContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ToolResultBlock represents a tool_result in a user message.
type ToolResultBlock struct {
	Type      string          `json:"type"` // "tool_result"
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or []ToolResultContent
}

// Usage captures token usage information.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// --- Interfaces ---

// LineParser parses agent-specific JSONL lines into RichMessages.
type LineParser interface {
	// ParseLine processes a single JSONL line.
	// Returns completed (fully assembled) messages ready to emit.
	ParseLine(line []byte) (completed []RichMessage, err error)
	// Flush finalizes any pending incomplete messages (e.g., on shutdown).
	Flush() []RichMessage
}

// SessionResolver finds the JSONL file path for a given agent process.
type SessionResolver interface {
	Resolve() (string, error)
}

// --- Rich message output types (exposed via API) ---

// RichContentBlock is a single content block in a rich message.
type RichContentBlock struct {
	Type      string          `json:"type" doc:"Content block type: text, thinking, tool_use, or tool_result"`
	Text      string          `json:"text,omitempty" doc:"Text content (for text and tool_result blocks)"`
	Thinking  string          `json:"thinking,omitempty" doc:"Thinking/reasoning content"`
	ToolUseID string          `json:"tool_use_id,omitempty" doc:"Tool use identifier"`
	ToolName  string          `json:"tool_name,omitempty" doc:"Name of the tool being called"`
	ToolInput json.RawMessage `json:"tool_input,omitempty" doc:"Tool call input parameters"`
	Status    string          `json:"status,omitempty" doc:"Tool lifecycle status: running, completed, or failed"`
	IsError   *bool           `json:"is_error,omitempty" doc:"Whether the tool result is an error"`
}

// RichMessage is a fully assembled message with all its content blocks.
type RichMessage struct {
	MessageID  string             `json:"message_id" doc:"Agent's internal message ID"`
	Role       string             `json:"role" doc:"Role of the message author (user or assistant)"`
	Content    []RichContentBlock `json:"content" doc:"Structured content blocks"`
	Model      string             `json:"model,omitempty" doc:"Model that generated this message"`
	StopReason string             `json:"stop_reason,omitempty" doc:"Why the model stopped generating"`
	Usage      *Usage             `json:"usage,omitempty" doc:"Token usage information"`
	Timestamp  time.Time          `json:"timestamp" doc:"Timestamp of the message"`
}
