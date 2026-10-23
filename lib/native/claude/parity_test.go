package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/sessions"
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

// usageTokens are the tokens of a usage, the cache writes of either
// lifetime together.
func usageTokens(u transcript.Usage) pricing.Tokens {
	return pricing.Tokens{Input: u.InputTokens, CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens, Output: u.OutputTokens}
}

// What a resumed session shows is what the adapter showed while it ran,
// and what its log says it cost is the usage the adapter reported: the
// session store reads the same log the session-log parser does.
func TestParityWithSessionStore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hooks replayHooks
	}{
		{name: "simple_text"},
		{name: "tool_approval"},
		{name: "steer", hooks: replayHooks{steer: func(n int, busy bool) bool { return n == 1 && busy }}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReplay(t, Config{}, "", "")
			r.run(loadRuns(t, tc.name)[0], tc.hooks)
			require.NoError(t, r.finish(nil))
			live := map[string]transcript.RichMessage{}
			var usage []pricing.Tokens
			for _, m := range r.sink.Final() {
				live[m.Role+"/"+m.MessageID] = comparable(t, m)
				if m.Role == "assistant" && m.Usage != nil {
					usage = append(usage, usageTokens(*m.Usage))
				}
			}

			data, err := os.ReadFile(filepath.Join("testdata", tc.name+".session.jsonl"))
			require.NoError(t, err)
			var head struct {
				SessionID string `json:"sessionId"`
			}
			require.NoError(t, json.Unmarshal(bytes.SplitN(data, []byte("\n"), 2)[0], &head))
			config := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", config)
			dir := filepath.Join(config, "projects", claudelog.ProjectDirName("/work/app"))
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, head.SessionID+".jsonl"), data, 0o600))
			store, err := sessions.New("claude", "/work/app", sessions.Config{})
			require.NoError(t, err)

			history, err := store.ReadHistory(head.SessionID)
			require.NoError(t, err)
			require.NotEmpty(t, history.Rich)
			for _, m := range history.Rich {
				m.MessageID = strings.TrimPrefix(m.MessageID, "history-")
				key := m.Role + "/" + m.MessageID
				require.Contains(t, live, key)
				assert.Equal(t, live[key], comparable(t, m), key)
			}

			costs, err := store.Costs()
			require.NoError(t, err)
			var logged []pricing.Tokens
			for _, call := range costs.Sessions[head.SessionID] {
				tokens := call.Tokens
				tokens.CacheWrite += tokens.CacheWrite1h
				tokens.CacheWrite1h = 0
				logged = append(logged, tokens)
			}
			assert.Equal(t, usage, logged)
		})
	}
}
