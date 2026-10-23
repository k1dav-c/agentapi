package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/sessions"
	"github.com/coder/agentapi/lib/transcript"
)

// parseRollout runs the existing rollout parser (the PTY transport's
// sidecar) over a rollout file and returns the final state of each message
// in first-seen order, as the event emitter stores them.
func parseRollout(t *testing.T, name string) []transcript.RichMessage {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	p := codexlog.NewParser()
	var all []transcript.RichMessage
	add := func(msgs []transcript.RichMessage) { all = append(all, msgs...) }
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		msgs, err := p.ParseLine(sc.Bytes())
		require.NoError(t, err)
		add(msgs)
		// As the watcher does, after a read that may end at any line.
		add(p.FlushCompleted())
	}
	add(p.Flush())
	return nativetest.Final(all)
}

// normalized drops what legitimately differs between the two paths:
// message and tool IDs (the rollout has Responses API IDs, app-server
// item IDs), timestamps, the model and stop reason (the rollout parser
// doesn't set them for Codex), and the tool status in tool_use blocks (the
// rollout parser leaves them "running"; the chat takes the status from the
// result). The command tool is "exec" with the code-mode script in the
// rollout and "exec_command" with the command's directory here; both carry
// the command as cmd.
func normalized(t *testing.T, msgs []transcript.RichMessage) []transcript.RichMessage {
	t.Helper()
	out := make([]transcript.RichMessage, 0, len(msgs))
	for _, m := range msgs {
		n := transcript.RichMessage{Role: m.Role, Usage: m.Usage}
		for _, b := range m.Content {
			b.ToolUseID = ""
			if b.Type == "tool_use" {
				b.Status = ""
				var in map[string]any
				require.NoError(t, json.Unmarshal(b.ToolInput, &in))
				if b.ToolName == "exec" || b.ToolName == "exec_command" {
					b.ToolName = "command"
					in = map[string]any{"cmd": in["cmd"]}
				}
				b.ToolInput = mustJSON(in)
			}
			n.Content = append(n.Content, b)
		}
		out = append(out, n)
	}
	return out
}

// dropEmpty drops messages without content blocks, which neither path
// shows.
func dropEmpty(msgs []transcript.RichMessage) []transcript.RichMessage {
	var out []transcript.RichMessage
	for _, m := range msgs {
		if len(m.Content) > 0 {
			out = append(out, m)
		}
	}
	return out
}

func TestParityWithRolloutParser(t *testing.T) {
	for _, tt := range []struct{ fixture, rollout string }{
		{"simple_text", "rollout_simple_text.jsonl"},
		{"command_approval_accept", "rollout_command_approval_accept.jsonl"},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			h := runFixture(t, tt.fixture, "", nil)
			want := normalized(t, dropEmpty(parseRollout(t, tt.rollout)))
			got := normalized(t, dropEmpty(h.rec.Final()))
			assert.Equal(t, want, got)
		})
	}
}

// What a resumed session shows is what the adapter showed while it ran,
// and what its rollout says the last call cost is the usage the adapter
// reported for its turn.
func TestParityWithSessionStore(t *testing.T) {
	for _, tt := range []struct{ fixture, rollout string }{
		{"simple_text", "rollout_simple_text.jsonl"},
		{"command_approval_accept", "rollout_command_approval_accept.jsonl"},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			h := runFixture(t, tt.fixture, "", nil)
			var live []transcript.RichMessage
			var usage *transcript.Usage
			for _, m := range dropEmpty(h.rec.Final()) {
				if m.Role == "user" && !slices.ContainsFunc(m.Content, func(b transcript.RichContentBlock) bool { return b.Type == "tool_result" }) {
					// A resumed session's prompts are its messages, not rich ones.
					continue
				}
				live = append(live, m)
				if m.Usage != nil {
					usage = m.Usage
				}
			}

			data, err := os.ReadFile(filepath.Join("testdata", tt.rollout))
			require.NoError(t, err)
			var meta struct {
				Payload struct {
					ID  string `json:"id"`
					CWD string `json:"cwd"`
				} `json:"payload"`
			}
			require.NoError(t, json.Unmarshal(bytes.SplitN(data, []byte("\n"), 2)[0], &meta))
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			day := filepath.Join(home, "sessions", "2026", "10", "16")
			require.NoError(t, os.MkdirAll(day, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(day, "rollout-2026-10-16T07-53-23-"+meta.Payload.ID+".jsonl"), data, 0o600))
			store, err := sessions.New("codex", meta.Payload.CWD, sessions.Config{})
			require.NoError(t, err)

			history, err := store.ReadHistory(meta.Payload.ID)
			require.NoError(t, err)
			assert.Equal(t, normalized(t, live), normalized(t, dropEmpty(history.Rich)))

			costs, err := store.Costs()
			require.NoError(t, err)
			calls := costs.Sessions[meta.Payload.ID]
			require.NotEmpty(t, calls)
			require.NotNil(t, usage)
			last := calls[len(calls)-1].Tokens
			assert.Equal(t, *usage, transcript.Usage{
				InputTokens: last.Input, OutputTokens: last.Output,
				CacheReadInputTokens: last.CacheRead, CacheCreationInputTokens: last.CacheWrite,
			})
		})
	}
}
