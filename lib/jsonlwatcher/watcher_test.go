package jsonlwatcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/transcript"
)

// pump advances clock one event at a time until cond holds: the watcher
// reads and resolves only when its clock ticks, and handles a tick on its
// own goroutine.
func pump(t *testing.T, clock *quartz.Mock, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the watcher")
		}
		if _, ok := clock.Peek(); ok {
			_, w := clock.AdvanceNext()
			w.MustWait(context.Background())
		}
		time.Sleep(time.Millisecond)
	}
}

// staticResolver is a test helper that returns a fixed path.
type staticResolver struct {
	path string
}

func (r *staticResolver) Resolve() (string, error) {
	return r.path, nil
}

type switchingResolver struct {
	mu   sync.RWMutex
	path string
}

func (r *switchingResolver) Resolve() (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.path, nil
}

func (r *switchingResolver) setPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.path = path
}

func TestWatcherSwitchesSessionFiles(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "foreground.jsonl")
	secondPath := filepath.Join(tmpDir, "parked.jsonl")
	firstLine := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-05T00:00:00Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"file_path":"one"}}],"stop_reason":"tool_use"}}` + "\n"
	secondLine := `{"type":"assistant","uuid":"a2","timestamp":"2026-09-05T00:00:01Z","message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"tool_2","name":"Write","input":{"file_path":"two"}}],"stop_reason":"tool_use"}}` + "\n"
	if err := os.WriteFile(firstPath, []byte(firstLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(secondLine), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := &switchingResolver{path: firstPath}
	var mu sync.Mutex
	var messages []transcript.RichMessage
	lineCount := 0
	clock := quartz.NewMock(t)
	w := New(Config{
		Resolver: resolver,
		Parser:   claudelog.NewParser(),
		Clock:    clock,
		OnMessage: func(message transcript.RichMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, message)
		},
		OnLine: func([]byte) {
			mu.Lock()
			defer mu.Unlock()
			lineCount++
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	waitFor := func(condition func() bool) {
		t.Helper()
		pump(t, clock, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return condition()
		})
	}

	waitFor(func() bool { return lineCount == 1 && len(messages) >= 1 })
	resolver.setPath(secondPath)
	waitFor(func() bool { return lineCount == 2 && len(messages) >= 2 })

	if got := messages[len(messages)-1].Content[0].ToolUseID; got != "tool_2" {
		t.Fatalf("last tool use ID = %q, want tool_2", got)
	}
}

// Claude Code may advertise a session id whose JSONL is never written (e.g.
// the user runs /clear before the first message). The watcher must follow
// the resolver to the new session instead of waiting for the old file.
func TestWatcherFollowsSessionThatIsNeverCreated(t *testing.T) {
	tmpDir := t.TempDir()
	missingPath := filepath.Join(tmpDir, "never-created.jsonl")
	activePath := filepath.Join(tmpDir, "active.jsonl")
	line := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-03T00:00:00Z","message":{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(activePath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := &switchingResolver{path: missingPath}
	var mu sync.Mutex
	var messages []transcript.RichMessage
	clock := quartz.NewMock(t)
	opening := clock.Trap().NewTicker("Watcher", "open")
	w := New(Config{
		Resolver: resolver,
		Parser:   claudelog.NewParser(),
		Clock:    clock,
		OnMessage: func(message transcript.RichMessage) {
			mu.Lock()
			defer mu.Unlock()
			messages = append(messages, message)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	// It waits for the file to appear; the session moves on meanwhile.
	opening.MustWait(ctx).Release()
	opening.Close()
	clock.Advance(sessionPollInterval).MustWait(ctx)
	resolver.setPath(activePath)
	pump(t, clock, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(messages) > 0
	})
}

func TestTailFile(t *testing.T) {
	tmpDir := t.TempDir()
	jsonlPath := filepath.Join(tmpDir, "test.jsonl")

	// Collect emitted messages, upserting by (MessageID, Role) the same
	// way the EventEmitter deduplicates downstream.
	var emitted []transcript.RichMessage
	var mu sync.Mutex
	collect := func(msg transcript.RichMessage) {
		mu.Lock()
		defer mu.Unlock()
		for i := len(emitted) - 1; i >= 0; i-- {
			if emitted[i].MessageID == msg.MessageID && emitted[i].Role == msg.Role {
				emitted[i] = msg
				return
			}
		}
		emitted = append(emitted, msg)
	}
	messages := func() []transcript.RichMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]transcript.RichMessage(nil), emitted...)
	}

	clock := quartz.NewMock(t)
	w := New(Config{
		Resolver:  &staticResolver{path: jsonlPath},
		Parser:    claudelog.NewParser(),
		OnMessage: collect,
		Clock:     clock,
	})

	// Write initial content
	f, err := os.Create(jsonlPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:19.000Z","message":{"role":"user","content":"hello"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.tailFile(ctx, jsonlPath)
	pump(t, clock, func() bool { return len(messages()) == 1 })

	// Append more data
	f, err = os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"hi there"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10}}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	pump(t, clock, func() bool { return len(messages()) == 2 })

	// Trigger finalization with another user message
	f, err = os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","uuid":"u2","timestamp":"2026-07-24T09:35:27.000Z","message":{"role":"user","content":"thanks"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	pump(t, clock, func() bool { return len(messages()) == 3 })

	cancel()
}

func TestStoppedWatcherDropsWhatItHolds(t *testing.T) {
	jsonlPath := filepath.Join(t.TempDir(), "session.jsonl")
	// An assistant message without a stop reason stays pending.
	require.NoError(t, os.WriteFile(jsonlPath, []byte(
		`{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:19.000Z","message":{"role":"user","content":"hello"}}`+"\n"+
			`{"type":"assistant","uuid":"a1","timestamp":"2026-07-24T09:35:26.000Z","message":{"id":"msg_001","role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"half"}]}}`+"\n"),
		0o644))
	var mu sync.Mutex
	var roles []string
	w := New(Config{
		Resolver: &staticResolver{path: jsonlPath},
		Parser:   claudelog.NewParser(),
		OnMessage: func(msg transcript.RichMessage) {
			mu.Lock()
			defer mu.Unlock()
			roles = append(roles, msg.Role)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Start(ctx)
	}()
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(roles) == 1
	}, 5*time.Second, 10*time.Millisecond)

	// A restart stops the watcher: its pending message would land in the
	// next conversation.
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"user"}, roles)
}

func TestWatcherReportsFormatDrift(t *testing.T) {
	jsonlPath := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(jsonlPath, []byte(
		`{"type":"user","uuid":"u1","timestamp":"2026-07-24T09:35:19.000Z","version":"2.9.0","message":{"role":"user","content":"hello"}}`+"\n"+
			`{"type":"brand-new"}`+"\n"), 0o600))
	var out strings.Builder
	var mu sync.Mutex
	logged := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}
	clock := quartz.NewMock(t)
	w := New(Config{
		Resolver: &staticResolver{path: jsonlPath},
		Parser:   claudelog.NewParser(),
		Format:   claudelog.Format,
		Clock:    clock,
		Logger:   slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) }), nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	pump(t, clock, func() bool { return strings.Contains(logged(), "level=WARN") })
	assert.Contains(t, logged(), `agent=claude version=2.9.0`)

	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"brand-new"}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	for range 5 {
		_, w := clock.AdvanceNext()
		w.MustWait(ctx)
	}
	assert.Equal(t, 1, strings.Count(logged(), "level=WARN"), "once per log")
}

// writerFunc is an io.Writer of a function.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
