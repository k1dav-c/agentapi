package convstate_test

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/transcript"
)

// BenchmarkStreamRichMessagePersisted streams one rich message through
// 2000 updates, each adding a block, into an emitter that stores the
// conversation in a state database. The adapter only appends to the
// content it sent, as the Codex log parser does. Each update used to copy the whole
// message for the database under the emitter's lock, so the stream took
// time in proportion to the square of its length.
func BenchmarkStreamRichMessagePersisted(b *testing.B) {
	const updates = 2000
	input := json.RawMessage(`{"command":"` + strings.Repeat("x", 240) + `"}`)
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		db, err := statedb.Open(filepath.Join(b.TempDir(), "state.db"), nil)
		if err != nil {
			b.Fatal(err)
		}
		emitter := convstate.NewEventEmitter(convstate.WithPersister(db))
		var content []transcript.RichContentBlock
		b.StartTimer()
		for i := range updates {
			content = append(content, transcript.RichContentBlock{
				Type: "tool_use", ToolUseID: strconv.Itoa(i), ToolName: "Bash", ToolInput: input,
			})
			emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m", Role: "assistant", Content: content})
		}
		b.StopTimer()
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// The state database encodes the rich messages the emitter keeps, without
// a copy, while the stream goes on: run with -race.
func TestStreamRichMessagePersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := statedb.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	emitter := convstate.NewEventEmitter(convstate.WithPersister(db))
	var content []transcript.RichContentBlock
	const updates = 40
	for i := range updates {
		content = append(content, transcript.RichContentBlock{Type: "text", Text: strconv.Itoa(i)})
		emitter.EmitRichMessageSnapshot(transcript.RichMessage{MessageID: "m", Role: "assistant", Content: content})
		// Let the writer encode some versions mid-stream.
		time.Sleep(10 * time.Millisecond)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = statedb.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	snap, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.RichMessages) != 1 || len(snap.RichMessages[0].Message.Content) != updates {
		t.Fatalf("stored %+v, want one message of %d blocks", snap.RichMessages, updates)
	}
}
