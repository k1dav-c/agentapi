package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
)

// The benchmarks read synthetic logs the size of a long-running agent's:
// a 111 MB Claude Code session, 600 Claude Code sessions in one directory,
// and 2000 Codex rollouts. They are made once per test binary, in a
// directory removed by TestMain.

var benchRoot struct {
	once sync.Once
	dir  string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if benchRoot.dir != "" {
		_ = os.RemoveAll(benchRoot.dir)
	}
	os.Exit(code)
}

// benchDir returns the directory of the synthetic logs, made on first use.
func benchDir(b *testing.B) string {
	b.Helper()
	benchRoot.once.Do(func() {
		benchRoot.dir, benchRoot.err = os.MkdirTemp("", "sessions-bench-")
	})
	if benchRoot.err != nil {
		b.Fatal(benchRoot.err)
	}
	return benchRoot.dir
}

const benchCWD = "/bench/work"

var claudeBench struct {
	once sync.Once
	path string
}

// claudeBenchLog writes a Claude Code session of about 111 MB: turns of a
// prompt, thinking, a tool call, its 8 KB result and a reply, each call
// with its usage, and Claude Code's cost-state now and then.
func claudeBenchLog(b *testing.B) (config, path string) {
	b.Helper()
	config = filepath.Join(benchDir(b), "claude")
	claudeBench.once.Do(func() {
		dir := filepath.Join(config, "projects", "-bench-work")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		claudeBench.path = filepath.Join(dir, "big.jsonl")
		f, err := os.Create(claudeBench.path)
		if err != nil {
			b.Fatal(err)
		}
		output := strings.Repeat("output line of a command the agent ran\n", 210)
		thinking := strings.Repeat("The agent thinks about what to do next. ", 25)
		start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		var sb strings.Builder
		written := 0
		for turn := 0; written+sb.Len() < 111<<20; turn++ {
			at := func(s int) string { return start.Add(time.Duration(turn*60+s) * time.Second).Format(time.RFC3339Nano) }
			usage := fmt.Sprintf(`"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":0}}`, 10, 20000+turn, 500, 300, 500)
			fmt.Fprintf(&sb, `{"type":"user","uuid":"u%d","timestamp":%q,"sessionId":"big","cwd":%q,"version":"2.1.302","message":{"role":"user","content":"Fix the bug number %d in the parser"}}`+"\n", turn, at(0), benchCWD, turn)
			fmt.Fprintf(&sb, `{"type":"assistant","uuid":"a%da","timestamp":%q,"sessionId":"big","cwd":%q,"version":"2.1.302","message":{"id":"msg_%d_a","role":"assistant","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":%q,"signature":"sig"}],"stop_reason":null,%s}}`+"\n", turn, at(1), benchCWD, turn, thinking, usage)
			fmt.Fprintf(&sb, `{"type":"assistant","uuid":"a%db","timestamp":%q,"sessionId":"big","cwd":%q,"version":"2.1.302","message":{"id":"msg_%d_a","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_%d","name":"Bash","input":{"command":"go test ./..."}}],"stop_reason":"tool_use",%s}}`+"\n", turn, at(2), benchCWD, turn, turn, usage)
			fmt.Fprintf(&sb, `{"type":"user","uuid":"r%d","timestamp":%q,"sessionId":"big","cwd":%q,"version":"2.1.302","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":%q}]}}`+"\n", turn, at(10), benchCWD, turn, output)
			fmt.Fprintf(&sb, `{"type":"assistant","uuid":"a%dc","timestamp":%q,"sessionId":"big","cwd":%q,"version":"2.1.302","message":{"id":"msg_%d_b","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Fixed bug %d: the parser now reads the line whole."}],"stop_reason":"end_turn",%s}}`+"\n", turn, at(20), benchCWD, turn, turn, usage)
			if turn%100 == 99 {
				fmt.Fprintf(&sb, `{"type":"cost-state","sessionId":"big","totalCostUSD":%d,"modelUsage":{"claude-opus-5-5":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":1,"cacheCreationInputTokens":1,"costUSD":0.001}}}`+"\n", turn)
			}
			if sb.Len() > 4<<20 {
				if _, err := f.WriteString(sb.String()); err != nil {
					b.Fatal(err)
				}
				written += sb.Len()
				sb.Reset()
			}
		}
		if _, err := f.WriteString(sb.String()); err != nil {
			b.Fatal(err)
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	})
	return config, claudeBench.path
}

// appended numbers the replies appendLine appends, so each is a new call.
var appended atomic.Int64

// appendLine appends a reply to the log, as the agent does while it works.
func appendLine(b *testing.B, path string, _ int) {
	b.Helper()
	n := int(appended.Add(1))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	_, err = fmt.Fprintf(f, `{"type":"assistant","uuid":"x%d","timestamp":"2026-10-30T00:00:00Z","sessionId":"big","cwd":%q,"message":{"id":"msg_x%d","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"more"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n", n, benchCWD, n)
	if err != nil {
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	// A distinct modification time: on a coarse clock an append can keep
	// the old one, which the old cache mistook for an unchanged file.
	at := time.Now().Add(time.Duration(n) * time.Millisecond)
	if err := os.Chtimes(path, at, at); err != nil {
		b.Fatal(err)
	}
}

var claudeManyBench struct {
	once   sync.Once
	config string
	latest string
}

// claudeManyLogs writes 600 Claude Code sessions of 25 turns into one
// project directory, 105 MB in all: each turn a prompt, a call logged on
// two lines (thinking, then a tool call), its 4 KB result and a second
// call. Every fifth session has Claude Code's cost-state every ten turns,
// every tenth a subagent, and every fiftieth is a fork that copies the
// first ten turns of the session before it. It returns the config
// directory and the latest session's log.
func claudeManyLogs(b *testing.B) (config, latest string) {
	b.Helper()
	claudeManyBench.once.Do(func() {
		config := filepath.Join(benchDir(b), "claude-many")
		dir := filepath.Join(config, "projects", "-bench-many")
		output := strings.Repeat("output line of a command the agent ran\n", 105)
		thinking := strings.Repeat("The agent thinks about what to do next. ", 10)
		start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
		turns := func(sb *strings.Builder, id, msgs string, from, to int, at func(turn, s int) string) {
			for turn := from; turn < to; turn++ {
				usage := fmt.Sprintf(`"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d}`, 4, 30000+turn, 400, 250)
				fmt.Fprintf(sb, `{"type":"user","uuid":"u%d","timestamp":%q,"sessionId":%q,"cwd":"/bench/many","version":"2.1.302","message":{"role":"user","content":"Fix bug %d"}}`+"\n", turn, at(turn, 0), id, turn)
				fmt.Fprintf(sb, `{"type":"assistant","uuid":"a%da","timestamp":%q,"sessionId":%q,"cwd":"/bench/many","version":"2.1.302","message":{"id":"msg_%s_%d_a","role":"assistant","model":"claude-opus-5-5","content":[{"type":"thinking","thinking":%q,"signature":"sig"}],"stop_reason":null,%s}}`+"\n", turn, at(turn, 1), id, msgs, turn, thinking, usage)
				fmt.Fprintf(sb, `{"type":"assistant","uuid":"a%db","timestamp":%q,"sessionId":%q,"cwd":"/bench/many","version":"2.1.302","message":{"id":"msg_%s_%d_a","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_%d","name":"Bash","input":{"command":"go test ./..."}}],"stop_reason":"tool_use",%s}}`+"\n", turn, at(turn, 2), id, msgs, turn, turn, usage)
				fmt.Fprintf(sb, `{"type":"user","uuid":"r%d","timestamp":%q,"sessionId":%q,"cwd":"/bench/many","version":"2.1.302","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":%q}]}}`+"\n", turn, at(turn, 10), id, turn, output)
				fmt.Fprintf(sb, `{"type":"assistant","uuid":"a%dc","timestamp":%q,"sessionId":%q,"cwd":"/bench/many","version":"2.1.302","message":{"id":"msg_%s_%d_b","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"Fixed bug %d."}],"stop_reason":"end_turn",%s}}`+"\n", turn, at(turn, 20), id, msgs, turn, turn, usage)
			}
		}
		write := func(path, data string) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				b.Fatal(err)
			}
		}
		for i := range 600 {
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
			at := func(turn, s int) string {
				return start.Add(time.Duration(i)*20*time.Minute + time.Duration(turn*30+s)*time.Second).Format(time.RFC3339Nano)
			}
			var sb strings.Builder
			from := 0
			if i%50 == 49 {
				// A fork: the first ten turns are the earlier session's.
				turns(&sb, id, fmt.Sprint(i-1), 0, 10, func(turn, s int) string {
					return start.Add(time.Duration(i-1)*20*time.Minute + time.Duration(turn*30+s)*time.Second).Format(time.RFC3339Nano)
				})
				from = 10
			}
			for turn := from; turn < 25; turn += 10 {
				turns(&sb, id, fmt.Sprint(i), turn, min(turn+10, 25), at)
				if i%5 == 0 {
					fmt.Fprintf(&sb, `{"type":"cost-state","sessionId":%q,"modelUsage":{"claude-opus-5-5":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":1,"cacheCreationInputTokens":1,"costUSD":%d}}}`+"\n", id, turn+10)
				}
			}
			path := filepath.Join(dir, id+".jsonl")
			write(path, sb.String())
			if i%10 == 0 {
				var sub strings.Builder
				turns(&sub, id, fmt.Sprintf("sub%d", i), 0, 5, at)
				write(filepath.Join(dir, id, "subagents", "agent-a"+fmt.Sprint(i)+".jsonl"), sub.String())
			}
			claudeManyBench.latest = path
		}
		claudeManyBench.config = config
	})
	return claudeManyBench.config, claudeManyBench.latest
}

// boundedStore returns a Store of the 600 sessions whose indexes keep
// readers of about maxBytes: a directory with 20 times the logs, 2 GB,
// passes the bound of 128 MB.
func boundedStore(b *testing.B, maxBytes int) *Store {
	b.Helper()
	s := newStore(b, "claude", "/bench/many")
	if maxBytes > 0 {
		s.logs = agentlog.NewIndex(func(string) *sessionLog { return s.newSessionLog(false) }, maxLogs, maxBytes)
		s.subs = agentlog.NewIndex(func(string) *sessionLog { return s.newSessionLog(true) }, maxLogs, maxBytes)
	}
	return s
}

// The bounds of the benchmarks of many logs: the Store's own, and one the
// logs' readers pass.
var bounds = []struct {
	name     string
	maxBytes int
}{{"within-bound", 0}, {"past-bound", 4 << 20}}

// BenchmarkClaudeCostsUnchanged is the summary's spend once a minute in a
// directory of 600 sessions, none of which changed since.
func BenchmarkClaudeCostsUnchanged(b *testing.B) {
	config, _ := claudeManyLogs(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	for _, bound := range bounds {
		b.Run(bound.name, func(b *testing.B) {
			store := boundedStore(b, bound.maxBytes)
			if _, err := store.Costs(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				d, err := store.Costs()
				if err != nil || len(d.Sessions) != 600 {
					b.Fatal("sessions missing", len(d.Sessions), err)
				}
			}
		})
	}
}

// BenchmarkClaudeCostsAppended is GET /cost while the agent works in a
// directory of 600 sessions: a call was appended to the latest since.
func BenchmarkClaudeCostsAppended(b *testing.B) {
	config, latest := claudeManyLogs(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	for _, bound := range bounds {
		b.Run(bound.name, func(b *testing.B) {
			store := boundedStore(b, bound.maxBytes)
			if _, err := store.Costs(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				b.StopTimer()
				appendLine(b, latest, i)
				b.StartTimer()
				d, err := store.Costs()
				if err != nil || len(d.Sessions) != 600 {
					b.Fatal("sessions missing", len(d.Sessions), err)
				}
			}
		})
	}
}

var codexBench struct {
	once sync.Once
	home string
}

// codexBenchHome writes 2000 Codex rollouts over 100 days, each with the
// large session_meta line Codex writes (its base instructions): 1980 of
// other directories, 10 sessions of benchCWD and 10 subagents of the
// latest one.
func codexBenchHome(b *testing.B) string {
	b.Helper()
	codexBench.once.Do(func() {
		home := filepath.Join(benchDir(b), "codex")
		instructions := strings.Repeat("You are Codex, a coding agent. ", 600)
		start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		for i := range 2000 {
			at := start.Add(time.Duration(i) * 72 * time.Minute)
			id := fmt.Sprintf("019a0000-0000-7000-8000-%012d", i)
			cwd, extra := fmt.Sprintf("/elsewhere/%d", i%37), ""
			switch {
			case i >= 1980 && i < 1990:
				cwd = benchCWD
			case i >= 1990:
				cwd = benchCWD
				extra = fmt.Sprintf(`,"parent_thread_id":"019a0000-0000-7000-8000-%012d","agent_nickname":"Sub","agent_path":"/root/task_%d"`, 1989, i)
			}
			dir := filepath.Join(home, "sessions", at.Format("2006"), at.Format("01"), at.Format("02"))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				b.Fatal(err)
			}
			lines := []string{
				fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"session_id":%q,"cwd":%q,"cli_version":"0.163.0","base_instructions":{"text":%q}%s}}`, at.Format(time.RFC3339Nano), id, id, cwd, instructions, extra),
				fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}`, at.Format(time.RFC3339Nano)),
				fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Task %d"}]}}`, at.Format(time.RFC3339Nano), i),
				fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_started"}}`, at.Format(time.RFC3339Nano)),
				fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":12000,"cached_input_tokens":8000,"output_tokens":300},"last_token_usage":{"input_tokens":12000,"cached_input_tokens":8000,"output_tokens":300}}}}`, at.Format(time.RFC3339Nano)),
				fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete"}}`, at.Format(time.RFC3339Nano)),
			}
			path := filepath.Join(dir, fmt.Sprintf("rollout-%s-%s.jsonl", at.Format("2006-01-02T15-04-05"), id))
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				b.Fatal(err)
			}
			if err := os.Chtimes(path, at, at); err != nil {
				b.Fatal(err)
			}
		}
		codexBench.home = home
	})
	return codexBench.home
}

// BenchmarkClaudeCostAfterAppend is GET /cost while the agent writes: a line
// was appended to a 111 MB session log since the last request.
func BenchmarkClaudeCostAfterAppend(b *testing.B) {
	config, path := claudeBenchLog(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	store := newStore(b, "claude", benchCWD)
	if _, err := store.Costs(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		b.StopTimer()
		appendLine(b, path, i)
		b.StartTimer()
		d, err := store.Costs()
		if err != nil || d.Cost("big").Calls == 0 {
			b.Fatal("no calls read", err)
		}
	}
}

// BenchmarkClaudeCostFirstRead is the first GET /cost of a server: it
// reads all of a 111 MB session log.
func BenchmarkClaudeCostFirstRead(b *testing.B) {
	config, _ := claudeBenchLog(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d, err := newStore(b, "claude", benchCWD).Costs()
		if err != nil || d.Cost("big").Calls == 0 {
			b.Fatal("no calls read", err)
		}
	}
}

// BenchmarkClaudeReadHistory reads the conversation of a 111 MB session
// log, as resuming the session does.
func BenchmarkClaudeReadHistory(b *testing.B) {
	config, _ := claudeBenchLog(b)
	b.Setenv("CLAUDE_CONFIG_DIR", config)
	store := newStore(b, "claude", benchCWD)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h, err := store.ReadHistory("big")
		if err != nil || len(h.Messages) == 0 {
			b.Fatal("no history read", err)
		}
	}
}

// BenchmarkCodexSubagents is GET /subagents of a Codex session among 2000
// rollouts, after a first request.
func BenchmarkCodexSubagents(b *testing.B) {
	b.Setenv("CODEX_HOME", codexBenchHome(b))
	store := newStore(b, "codex", benchCWD)
	if _, err := store.Subagents("019a0000-0000-7000-8000-000000001989"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		subs, err := store.Subagents("019a0000-0000-7000-8000-000000001989")
		if err != nil || len(subs) != 10 {
			b.Fatalf("got %d subagents, %v", len(subs), err)
		}
	}
}

// BenchmarkCodexSubagentsFirstRequest is the first GET /subagents of a
// server among 2000 Codex rollouts, which reads their first lines.
func BenchmarkCodexSubagentsFirstRequest(b *testing.B) {
	b.Setenv("CODEX_HOME", codexBenchHome(b))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		subs, err := newStore(b, "codex", benchCWD).Subagents("019a0000-0000-7000-8000-000000001989")
		if err != nil || len(subs) != 10 {
			b.Fatalf("got %d subagents, %v", len(subs), err)
		}
	}
}

// BenchmarkCodexCosts is GET /sessions' costs among 2000 Codex rollouts,
// after a first request.
func BenchmarkCodexCosts(b *testing.B) {
	b.Setenv("CODEX_HOME", codexBenchHome(b))
	store := newStore(b, "codex", benchCWD)
	if _, err := store.Costs(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		d, err := store.Costs()
		if err != nil || len(d.Sessions) == 0 {
			b.Fatal("no sessions read", err)
		}
	}
}
