package jsonlwatcher

import (
	"encoding/json"

	"github.com/coder/agentapi/lib/transcript"
)

// --- JSONL line parsing types (input from Claude Code session files) ---

// JSONLLine is the raw envelope parsed from each line of the JSONL file.
type JSONLLine struct {
	Type      string        `json:"type"`
	UUID      string        `json:"uuid,omitempty"`
	Timestamp string        `json:"timestamp,omitempty"`
	SessionID string        `json:"sessionId,omitempty"`
	Message   *JSONLMessage `json:"message,omitempty"`
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
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"` // for type "image"
}

// ImageSource is the image of an "image" content block, such as the one
// Claude Code's Read tool returns for an image file.
type ImageSource struct {
	Type      string `json:"type"` // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// ToolResultBlock represents a tool_result in a user message.
type ToolResultBlock struct {
	Type      string          `json:"type"` // "tool_result"
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or []ToolResultContent
}

// --- Interfaces ---

// LineParser parses agent-specific JSONL lines into RichMessages.
type LineParser interface {
	// ParseLine processes a single JSONL line.
	// Returns completed (fully assembled) messages ready to emit.
	ParseLine(line []byte) (completed []RichMessage, err error)
	// FlushCompleted finalizes only pending messages that have a terminal
	// stop_reason (e.g. "end_turn", "max_tokens"). Messages still being
	// assembled (no stop_reason yet) are left in pending. This is safe to
	// call on every poll cycle without risk of splitting an in-progress turn.
	FlushCompleted() []RichMessage
	// Flush finalizes all pending messages regardless of state (e.g., on shutdown).
	Flush() []RichMessage
}

// SessionResolver finds the JSONL file path for a given agent process.
type SessionResolver interface {
	Resolve() (string, error)
}

// --- Rich message output types ---

// The types of the structured transcript are in lib/transcript, which the
// transports share; this package's parsers use them by these names.
type (
	// Deprecated: use transcript.RichMessage.
	RichMessage = transcript.RichMessage
	// Deprecated: use transcript.RichContentBlock.
	RichContentBlock = transcript.RichContentBlock
	// Deprecated: use transcript.RichImage.
	RichImage = transcript.RichImage
	// Deprecated: use transcript.Usage.
	Usage = transcript.Usage
	// Deprecated: use transcript.SessionEvent.
	SessionEvent = transcript.SessionEvent
)
