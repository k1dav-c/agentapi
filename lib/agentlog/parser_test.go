package agentlog_test

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/transcript"
)

// finalMessages parses lines the way the watcher does, upserting by
// (MessageID, Role) as the emitter does, with a FlushCompleted after every
// line (a read between any two lines) or only after the last one.
func finalMessages(t *testing.T, p agentlog.LineParser, lines []string, flushEveryLine bool) []transcript.RichMessage {
	t.Helper()
	type key struct{ id, role string }
	var order []key
	final := map[key]transcript.RichMessage{}
	add := func(msgs []transcript.RichMessage) {
		for _, m := range msgs {
			k := key{m.MessageID, m.Role}
			if _, ok := final[k]; !ok {
				order = append(order, k)
			}
			final[k] = m
		}
	}
	for _, line := range lines {
		msgs, err := p.ParseLine([]byte(line))
		require.NoError(t, err)
		add(msgs)
		if flushEveryLine {
			add(p.FlushCompleted())
		}
	}
	add(p.FlushCompleted())
	add(p.Flush())
	out := make([]transcript.RichMessage, 0, len(order))
	for _, k := range order {
		out = append(out, final[k])
	}
	return out
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}
	require.NoError(t, sc.Err())
	return lines
}

// The messages a session log gives must not depend on when the watcher
// happened to read it.
func TestMessagesDontDependOnReads(t *testing.T) {
	codexTurn := []string{
		`{"type":"event_msg","timestamp":"2025-01-01T00:00:00Z","payload":{"type":"task_started"}}`,
		`{"type":"response_item","timestamp":"2025-01-01T00:00:01Z","payload":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}]}}`,
		`{"type":"response_item","timestamp":"2025-01-01T00:00:02Z","payload":{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}}`,
		`{"type":"response_item","timestamp":"2025-01-01T00:00:03Z","payload":{"type":"message","role":"assistant","id":"m1","content":[{"type":"output_text","text":"done"}]}}`,
		`{"type":"event_msg","timestamp":"2025-01-01T00:00:04Z","payload":{"type":"task_complete"}}`,
	}
	for _, tc := range []struct {
		name   string
		parser func() agentlog.LineParser
		lines  []string
	}{
		{"claude sample", func() agentlog.LineParser { return claudelog.NewParser() }, readLines(t, "claudelog/testdata/sample.jsonl")},
		{"codex sample", func() agentlog.LineParser { return codexlog.NewParser() }, readLines(t, "codexlog/testdata/codex_sample.jsonl")},
		{"codex turn", func() agentlog.LineParser { return codexlog.NewParser() }, codexTurn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			once := finalMessages(t, tc.parser(), tc.lines, false)
			everyLine := finalMessages(t, tc.parser(), tc.lines, true)
			require.Equal(t, once, everyLine)
		})
	}
}
