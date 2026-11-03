package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var claudeSubBench struct {
	once   sync.Once
	config string
}

// claudeSubagentsBench writes a Claude Code session that ran 20 subagents,
// each of a log of about 1 MB: turns of a tool call and its 8 KB result.
func claudeSubagentsBench(b *testing.B) string {
	b.Helper()
	claudeSubBench.once.Do(func() {
		config := filepath.Join(benchDir(b), "claude-subagents")
		dir := filepath.Join(config, "projects", "-bench-work")
		subDir := filepath.Join(dir, "parent", "subagents")
		if err := os.MkdirAll(subDir, 0o700); err != nil {
			b.Fatal(err)
		}
		writeBenchFile(b, filepath.Join(dir, "parent.jsonl"),
			fmt.Sprintf(`{"type":"user","timestamp":"2026-10-01T00:00:00Z","sessionId":"parent","cwd":%q,"message":{"role":"user","content":"Look around"}}`+"\n", benchCWD))
		output := strings.Repeat("output line of a command the subagent ran\n", 200)
		start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		for s := range 20 {
			var sb strings.Builder
			for turn := 0; sb.Len() < 1<<20; turn++ {
				at := func(sec int) string {
					return start.Add(time.Duration(s*3600+turn*10+sec) * time.Second).Format(time.RFC3339Nano)
				}
				fmt.Fprintf(&sb, `{"isSidechain":true,"agentId":"a%d","type":"assistant","timestamp":%q,"sessionId":"parent","message":{"id":"msg_%d_%d","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"tool_use","id":"toolu_%d_%d","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20}}}`+"\n", s, at(0), s, turn, s, turn)
				fmt.Fprintf(&sb, `{"isSidechain":true,"agentId":"a%d","type":"user","timestamp":%q,"sessionId":"parent","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d_%d","content":%q}]}}`+"\n", s, at(1), s, turn, output)
			}
			writeBenchFile(b, filepath.Join(subDir, fmt.Sprintf("agent-a%d.jsonl", s)), sb.String())
		}
		claudeSubBench.config = config
	})
	return claudeSubBench.config
}

func writeBenchFile(b *testing.B, path, data string) {
	b.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkClaudeSubagentEvents is GET /timeline?subagents=true of a
// session that ran 20 subagents (20 MB of logs), after a first request.
func BenchmarkClaudeSubagentEvents(b *testing.B) {
	b.Setenv("CLAUDE_CONFIG_DIR", claudeSubagentsBench(b))
	store := newStore(b, "claude", benchCWD)
	if _, err := store.SubagentEvents("parent"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		events, err := store.SubagentEvents("parent")
		if err != nil || len(events) == 0 {
			b.Fatal("no events read", err)
		}
	}
}

// BenchmarkClaudeReadSubagent is GET /subagents/{id} of a 1 MB subagent
// log, after a first request.
func BenchmarkClaudeReadSubagent(b *testing.B) {
	b.Setenv("CLAUDE_CONFIG_DIR", claudeSubagentsBench(b))
	store := newStore(b, "claude", benchCWD)
	if _, _, err := store.ReadSubagent("parent", "a7"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, run, err := store.ReadSubagent("parent", "a7")
		if err != nil || len(run.Messages) == 0 {
			b.Fatal("no messages read", err)
		}
	}
}

var codexSubBench struct {
	once sync.Once
	home string
}

// codexSubagentsBench writes a Codex thread that ran 10 subagents, each
// of a log that starts with a 1 MB copy of the parent's lines.
func codexSubagentsBench(b *testing.B) string {
	b.Helper()
	codexSubBench.once.Do(func() {
		home := filepath.Join(benchDir(b), "codex-subagents")
		dir := filepath.Join(home, "sessions", "2026", "10", "01")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		parent := "019b0000-0000-7000-8000-000000000000"
		at := "2026-10-01T00:00:00Z"
		writeBenchFile(b, filepath.Join(dir, "rollout-2026-10-01T00-00-00-"+parent+".jsonl"), strings.Join([]string{
			fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q}}`, at, parent, parent, benchCWD),
			fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Go"}]}}`, at),
		}, "\n")+"\n")
		text := strings.Repeat("what the parent said before it ran the subagent ", 40)
		for s := range 10 {
			id := fmt.Sprintf("019b0000-0000-7000-8000-%012d", s+1)
			var sb strings.Builder
			ordinal := 0
			line := func(format string, args ...any) {
				fmt.Fprintf(&sb, `{"timestamp":%q,"ordinal":%d,`, at, ordinal)
				fmt.Fprintf(&sb, format+"\n", args...)
				ordinal++
			}
			copied := 0
			for sb.Len() < 1<<20 {
				copied++
				sb.Reset()
				ordinal = 0
				line(`"type":"session_meta","payload":{"id":%q,"session_id":%q,"parent_thread_id":%q,"cwd":%q,"agent_path":"/root/task_%d","agent_nickname":"Sub","subagent_history_start_ordinal":%d}}`, id, parent, parent, benchCWD, s, copied+1)
				for range copied {
					line(`"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`, text)
				}
			}
			line(`"type":"turn_context","payload":{"model":"gpt-6-luna"}}`)
			line(`"type":"event_msg","payload":{"type":"task_started"}}`)
			for turn := range 20 {
				line(`"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"step %d"}]}}`, turn)
				line(`"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"output_tokens":%d},"last_token_usage":{"input_tokens":1000,"output_tokens":10}}}}`, 1000*(turn+1), 10*(turn+1))
			}
			line(`"type":"event_msg","payload":{"type":"task_complete"}}`)
			writeBenchFile(b, filepath.Join(dir, "rollout-2026-10-01T00-00-00-"+id+".jsonl"), sb.String())
		}
		codexSubBench.home = home
	})
	return codexSubBench.home
}

// BenchmarkCodexSubagentCostsFirstRead is the first GET /cost of a Codex
// thread that ran 10 subagents, whose logs start with a copy of its own.
func BenchmarkCodexSubagentCostsFirstRead(b *testing.B) {
	b.Setenv("CODEX_HOME", codexSubagentsBench(b))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d, err := newStore(b, "codex", benchCWD).Costs()
		if err != nil || d.Cost("019b0000-0000-7000-8000-000000000000").Calls != 200 {
			b.Fatal("calls not read", err)
		}
	}
}

// BenchmarkClaudeCostsMemory reports what a Store keeps of a 111 MB session
// log's calls once it read their costs, per call.
func BenchmarkClaudeCostsMemory(b *testing.B) {
	config, _ := claudeBenchLog(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	var kept, calls float64
	for range b.N {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		store := newStore(b, "claude", benchCWD)
		d, err := store.Costs()
		if err != nil {
			b.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		calls = float64(d.Cost("big").Calls)
		kept = float64(after.HeapAlloc) - float64(before.HeapAlloc)
		runtime.KeepAlive(store)
		runtime.KeepAlive(d)
	}
	b.ReportMetric(kept/calls, "B/call")
	b.ReportMetric(kept/(1<<20), "MiB")
}
