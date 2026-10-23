package agentlog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/agentapi/lib/transcript"
)

func TestSessionEventJSONLShape(t *testing.T) {
	event := transcript.SessionEvent{
		EventID:   17,
		Kind:      "system",
		Role:      Ptr("system"),
		Content:   Ptr("session switched"),
		EventTime: mustParseTime(t, "2026-07-17T03:23:14.380896751Z"),
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":17,"kind":"system","role":"system","time":"2026-07-17T03:23:14.380896751Z","content":"session switched"}`
	if string(data) != want {
		t.Fatalf("event JSON = %s, want %s", data, want)
	}
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
