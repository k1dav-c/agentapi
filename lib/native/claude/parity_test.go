package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/transcript"
)

// sessionLogMessages parses a session log the way the JSONL watcher does.
func sessionLogMessages(t *testing.T, name string) []transcript.RichMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".session.jsonl"))
	require.NoError(t, err)
	parser := claudelog.NewParser()
	sink := nativetest.NewSink(t)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		msgs, err := parser.ParseLine(scanner.Bytes())
		require.NoError(t, err)
		msgs = append(msgs, parser.FlushCompleted()...)
		for _, m := range msgs {
			sink.RichMessage(m)
		}
	}
	for _, m := range parser.Flush() {
		sink.RichMessage(m)
	}
	return sink.Final()
}

// comparable drops what legitimately differs: timestamps (the adapter
// stamps arrival time) and tool status (the session-log parser sets none),
// and normalizes tool input JSON.
func comparable(t *testing.T, m transcript.RichMessage) transcript.RichMessage {
	t.Helper()
	m.Timestamp = time.Time{}
	m = m.Clone()
	for i := range m.Content {
		m.Content[i].Status = ""
		if len(m.Content[i].ToolInput) > 0 {
			var v any
			require.NoError(t, json.Unmarshal(m.Content[i].ToolInput, &v))
			m.Content[i].ToolInput, _ = json.Marshal(v)
		}
	}
	return m
}

func TestParityWithSessionLog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  int
		// replayed: the capture echoed prompts (--replay-user-messages);
		// otherwise prompts are only in the session log.
		replayed bool
		// onlyStream are prompts the session log doesn't keep as user
		// lines (a steered message is logged as a queued command).
		onlyStream []string
		hooks      replayHooks
	}{
		{name: "simple_text"},
		{name: "tool_approval"},
		{
			name: "steer", replayed: true,
			onlyStream: []string{"b5b8fbc5-f2ed-4f9f-bd44-d08bb64eaeb0"},
			hooks:      replayHooks{steer: func(n int, busy bool) bool { return n == 1 && busy }},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newReplay(t, Config{}, "", "")
			r.run(loadRuns(t, tc.name)[tc.run], tc.hooks)
			require.NoError(t, r.finish(nil))

			ours := map[string]transcript.RichMessage{}
			for _, m := range r.sink.Final() {
				ours[m.Role+"/"+m.MessageID] = comparable(t, m)
			}
			logged := sessionLogMessages(t, tc.name)
			require.NotEmpty(t, logged)
			matched := 0
			for _, want := range logged {
				key := want.Role + "/" + want.MessageID
				got, ok := ours[key]
				if !ok && want.Role == "user" && !tc.replayed {
					continue
				}
				require.True(t, ok, "missing %s", key)
				assert.Equal(t, comparable(t, want), got, key)
				delete(ours, key)
				matched++
			}
			assert.NotZero(t, matched)
			var extra []string
			for key := range ours {
				extra = append(extra, key)
			}
			var wantExtra []string
			for _, id := range tc.onlyStream {
				wantExtra = append(wantExtra, "user/"+id)
			}
			assert.ElementsMatch(t, wantExtra, extra)
		})
	}
}
