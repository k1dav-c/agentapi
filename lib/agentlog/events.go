package agentlog

import (
	"encoding/json"
	"time"

	"github.com/coder/agentapi/lib/transcript"
)

// NewEvent returns a timeline event.
func NewEvent(kind string, role, content *string, eventTime time.Time, sessionID, sourceID *string) transcript.SessionEvent {
	return transcript.SessionEvent{
		Kind:      kind,
		Role:      role,
		Content:   content,
		SessionID: sessionID,
		SourceID:  sourceID,
		EventTime: eventTime,
	}
}

// NormalizeJSON returns a tool input as a JSON value: JSON as it is, a
// string that holds JSON as that JSON, anything else as a JSON string.
func NormalizeJSON(raw json.RawMessage) json.RawMessage {
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

// Optional returns a pointer to value, or nil for an empty one.
func Optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// Ptr returns a pointer to value.
func Ptr(value string) *string {
	return &value
}
