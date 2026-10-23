// Package transcript has the types of a conversation's structured
// transcript that the transports share: rich messages with their content
// blocks, as an agent's protocol (native) or its session log (PTY) reports
// them, and the timeline's events.
package transcript

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"
)

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
	Images    []RichImage     `json:"images,omitempty" doc:"Images in a tool result (for example an image file the agent read)"`
}

// RichImage is an image in a tool result. The parser fills Data; the
// server stores it and replaces it with URL, so clients never get the
// image inline.
type RichImage struct {
	MediaType string `json:"media_type" doc:"MIME type of the image"`
	URL       string `json:"url,omitempty" doc:"Server path that serves the image, relative to the API root (e.g. /uploads/<checksum>/image.png)"`
	Data      []byte `json:"-"`
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

// Clone returns a copy of m that shares nothing with it but the image
// data, which nothing changes once it is in a message.
func (m RichMessage) Clone() RichMessage {
	if m.Content != nil {
		content := make([]RichContentBlock, len(m.Content))
		for i, block := range m.Content {
			content[i] = block.clone()
		}
		m.Content = content
	}
	if m.Usage != nil {
		usage := *m.Usage
		m.Usage = &usage
	}
	return m
}

func (b RichContentBlock) clone() RichContentBlock {
	b.ToolInput = bytes.Clone(b.ToolInput)
	b.Images = slices.Clone(b.Images)
	if b.IsError != nil {
		isError := *b.IsError
		b.IsError = &isError
	}
	return b
}

// Usage is the tokens of a model call. Whichever agent made the call, the
// fields count as Anthropic's API does: the input read from or written to
// the prompt cache is in the cache fields and not in InputTokens, so the
// four add up to the call's tokens. Codex counts the cached input in its
// input too; its usage is converted.
type Usage struct {
	// InputTokens is the input neither read from nor written to the
	// prompt cache.
	InputTokens int `json:"input_tokens" doc:"Input tokens neither read from nor written to the prompt cache."`
	// OutputTokens is the output, reasoning included.
	OutputTokens int `json:"output_tokens" doc:"Output tokens, reasoning included."`
	// CacheCreationInputTokens is the input written to the prompt cache.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens" doc:"Input tokens written to the prompt cache."`
	// CacheReadInputTokens is the input read from the prompt cache.
	CacheReadInputTokens int `json:"cache_read_input_tokens" doc:"Input tokens read from the prompt cache."`
}

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
	// SubagentID and ParentToolUseID are set on the events of a subagent
	// the agent ran (GET /timeline?subagents=true).
	SubagentID      *string `json:"subagent_id,omitempty" doc:"The subagent the event belongs to; missing for the agent's own events"`
	ParentToolUseID *string `json:"parent_tool_use_id,omitempty" doc:"The agent's tool call that started the subagent (Claude Code's Agent tool), when known"`
}
