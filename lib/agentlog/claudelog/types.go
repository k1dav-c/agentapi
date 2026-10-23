// Package claudelog reads Claude Code's session logs: the rich messages,
// timeline events, prompts and session name their records hold, and where
// Claude Code keeps them.
package claudelog

import (
	"encoding/json"

	"github.com/coder/agentapi/lib/transcript"
)

// Line is the envelope of a line of a Claude Code session log.
type Line struct {
	Type      string   `json:"type"`
	UUID      string   `json:"uuid,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
	SessionID string   `json:"sessionId,omitempty"`
	Message   *Message `json:"message,omitempty"`
}

// Message represents the message field within a line.
type Message struct {
	ID         string            `json:"id"`
	Role       string            `json:"role"`
	Model      string            `json:"model,omitempty"`
	Content    json.RawMessage   `json:"content"` // string (user prompt) or []ContentBlock
	StopReason *string           `json:"stop_reason,omitempty"`
	Usage      *transcript.Usage `json:"usage,omitempty"`
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
