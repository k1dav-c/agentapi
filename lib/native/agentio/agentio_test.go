package agentio_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/agentio"
)

func readAll(input string, maxLine int) []string {
	var lines []string
	agentio.ReadLines(strings.NewReader(input), "test", maxLine, slog.New(slog.DiscardHandler), func(line []byte) {
		lines = append(lines, string(line))
	})
	return lines
}

func TestReadLines(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{`{"a":1}`, `{"b":"x y"}`, `{"c":3}`, `{"last":true}`},
		readAll("{\"a\":1}\r\n\n  \n{\"b\":\"x y\"}\n{\"c\":3}\n{\"last\":true}", 0),
		"blank lines are skipped, \\r\\n is trimmed, U+2028 doesn't split, the last line needs no newline")

	long := strings.Repeat("x", 200<<10)
	assert.Equal(t, []string{"before", "after"}, readAll("before\n"+long+"\nafter\n", 100<<10),
		"a line over the limit is dropped, and the next one read")
}

func TestReadLinesKeepsLines(t *testing.T) {
	t.Parallel()
	var kept [][]byte
	agentio.ReadLines(strings.NewReader("one\ntwo\n"), "test", 0, slog.New(slog.DiscardHandler), func(line []byte) {
		kept = append(kept, line)
	})
	assert.Equal(t, [][]byte{[]byte("one"), []byte("two")}, kept, "a line isn't overwritten by the next")
}

func TestWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := agentio.NewWriter("test")
	assert.ErrorIs(t, w.Write(ctx, map[string]int{"a": 1}), native.ErrNotStarted)

	var buf bytes.Buffer
	var mu sync.Mutex
	w.Attach(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, w.Write(ctx, map[string]int{"n": i}))
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	require.Len(t, lines, 50)
	for _, line := range lines {
		assert.Regexp(t, `^\{"n":\d+\}$`, line, "records don't interleave")
	}

	w.Attach(writerFunc(func([]byte) (int, error) { return 0, io.ErrClosedPipe }))
	assert.EqualError(t, w.Write(ctx, 1), "write to test: io: read/write on closed pipe")
}

// An agent that stops reading its input can't hold a write past its
// context. A record whose write began may still reach the agent; one that
// waited behind it never goes out.
func TestWriterGivesUpWithTheContext(t *testing.T) {
	t.Parallel()
	r, pipe := io.Pipe()
	w := agentio.NewWriter("test")
	w.Attach(pipe)

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- w.Write(ctx, "first") }()
	require.Eventually(t, w.Writing, 5*time.Second, time.Millisecond)
	second := make(chan error, 1)
	go func() { second <- w.Write(ctx, "second") }()
	cancel()
	err := <-first
	require.ErrorIs(t, err, native.ErrUnconfirmed, "the agent may yet read it")
	assert.ErrorIs(t, err, context.Canceled)
	err = <-second
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, native.ErrUnconfirmed, "it never went out")

	// The agent reads again: it gets the record whose write began, whole,
	// and nothing of the other.
	lines := make(chan string, 10)
	go func() {
		reader := bufio.NewReader(r)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			lines <- line
		}
	}()
	require.NoError(t, w.Write(context.Background(), "third"))
	assert.Equal(t, "\"first\"\n", <-lines)
	assert.Equal(t, "\"third\"\n", <-lines)
	_ = r.Close()
	assert.Error(t, w.Write(context.Background(), "fourth"), "the agent's input is closed")
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestCalls(t *testing.T) {
	t.Parallel()
	var calls agentio.Calls[string, int]
	var seen []string
	call, err := calls.Add("a", func(v int, err error) { seen = append(seen, "onResult") })
	require.NoError(t, err)
	assert.True(t, calls.Resolve("a", 7, nil))
	seen = append(seen, "resolved")
	v, err := call.Wait(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 7, v)
	assert.Equal(t, []string{"onResult", "resolved"}, seen, "onResult runs before Resolve returns")
	assert.False(t, calls.Resolve("a", 8, nil), "a response is handed over once")

	dropped, err := calls.Add("b", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = dropped.Wait(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	calls.Drop("b")
	assert.False(t, calls.Resolve("b", 1, nil))
}

func TestCallsClose(t *testing.T) {
	t.Parallel()
	var calls agentio.Calls[int, string]
	waiting, err := calls.Add(1, nil)
	require.NoError(t, err)
	exited := errors.New("the agent exited")
	calls.Close(exited)
	_, err = waiting.Wait(t.Context())
	assert.ErrorIs(t, err, exited, "a request still waiting gets the error")
	_, err = calls.Add(2, nil)
	assert.ErrorIs(t, err, exited, "new requests are refused")
	calls.Close(errors.New("again"))
	_, err = calls.Add(3, nil)
	assert.ErrorIs(t, err, exited, "the first error stays")
}
