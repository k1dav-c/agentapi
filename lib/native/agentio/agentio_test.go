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

// WriteAsync doesn't wait for an agent that doesn't read; its record goes
// out once the agent does, and a failure is logged.
func TestWriteAsync(t *testing.T) {
	t.Parallel()
	w := agentio.NewWriter("test")
	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()
	w.Attach(pw)
	w.WriteAsync(map[string]string{"answer": "no"}, slog.New(slog.DiscardHandler))
	line, err := bufio.NewReader(pr).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, `{"answer":"no"}`+"\n", line)

	var logged bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logged.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug}))
	agentio.NewWriter("test").WriteAsync(1, logger)
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(logged.String(), "Failed to write to the agent")
	}, 5*time.Second, time.Millisecond)
}

// An agent that stops reading its input can't hold a write past its
// context. A record whose write began may still reach the agent; one that
// waited behind it never goes out.
func TestWriterGivesUpWithTheContext(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var written []string
	w := agentio.NewWriter("test")
	w.Attach(writerFunc(func(p []byte) (int, error) {
		if string(p) == "\"first\"\n" {
			// The agent stops reading.
			close(entered)
			<-release
		}
		mu.Lock()
		defer mu.Unlock()
		written = append(written, string(p))
		return len(p), nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- w.Write(ctx, "first") }()
	<-entered
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
	close(release)
	require.NoError(t, w.Write(context.Background(), "third"))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"\"first\"\n", "\"third\"\n"}, written)
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

// A detached request stays registered: its caller got its answer
// already, and the response goes to the adapter alone.
func TestCallsDetach(t *testing.T) {
	t.Parallel()
	var calls agentio.Calls[string, int]
	call, err := calls.Add("a", nil)
	require.NoError(t, err)
	assert.True(t, calls.Detach("a", 1, nil))
	assert.False(t, calls.Detach("a", 2, nil), "the caller is handed one answer")
	v, err := call.Wait(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, v)
	late, waiting := calls.Take("a")
	require.NotNil(t, late, "the response still finds the request")
	assert.False(t, waiting, "nobody waits for it")
	assert.False(t, late.Deliver(3, nil))
	gone, _ := calls.Take("a")
	assert.Nil(t, gone)

	// A caller that gives up on a request the agent may still carry out.
	abandoned, err := calls.Add("b", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = abandoned.Wait(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	calls.Detach("b", 0, nil)
	b, waiting := calls.Take("b")
	assert.NotNil(t, b)
	assert.False(t, waiting)

	// Close doesn't block on a request whose caller was answered.
	_, err = calls.Add("c", nil)
	require.NoError(t, err)
	calls.Detach("c", 1, nil)
	calls.Close(errors.New("exited"))
}

// A request sent and not answered may yet be carried out.
func TestCallsAwait(t *testing.T) {
	t.Parallel()
	var calls agentio.Calls[string, int]
	answered, err := calls.Add("a", nil)
	require.NoError(t, err)
	calls.Resolve("a", 7, nil)
	v, err := calls.Await(t.Context(), "a", answered)
	require.NoError(t, err)
	assert.Equal(t, 7, v)

	abandoned, err := calls.Add("b", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = calls.Await(ctx, "b", abandoned)
	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, native.ErrUnconfirmed)
	assert.False(t, calls.Resolve("b", 1, nil), "the request is dropped")

	// A request with an onResult stays registered: a response that comes
	// after its caller gave up still runs it.
	var late []int
	detached, err := calls.Add("d", func(v int, _ error) { late = append(late, v) })
	require.NoError(t, err)
	_, err = calls.Await(ctx, "d", detached)
	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, native.ErrUnconfirmed)
	assert.True(t, calls.Resolve("d", 9, nil), "the request still waits for its response")
	assert.Equal(t, []int{9}, late)
	assert.False(t, calls.Resolve("d", 10, nil))

	// So does one whose write may yet reach the agent; one that surely
	// didn't is dropped.
	_, err = calls.Add("e", func(v int, _ error) { late = append(late, v) })
	require.NoError(t, err)
	calls.WriteFailed("e", native.Unconfirmed(context.Canceled))
	assert.True(t, calls.Resolve("e", 11, nil))
	assert.Equal(t, []int{9, 11}, late)
	_, err = calls.Add("f", func(v int, _ error) { late = append(late, v) })
	require.NoError(t, err)
	calls.WriteFailed("f", context.Canceled)
	assert.False(t, calls.Resolve("f", 12, nil))

	exited := errors.New("the agent exited")
	waiting, err := calls.Add("c", nil)
	require.NoError(t, err)
	calls.Close(exited)
	_, err = calls.Await(t.Context(), "c", waiting)
	require.ErrorIs(t, err, exited)
	assert.ErrorIs(t, err, native.ErrUnconfirmed)
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

// An answer takes the request until its write fails before it went out.
func TestAnswer(t *testing.T) {
	t.Parallel()
	var a agentio.Answer
	require.True(t, a.Begin())
	assert.False(t, a.Begin(), "one answer at a time")
	a.Written(errors.New("broken pipe"))
	assert.Equal(t, agentio.Answer{}, a, "it never went out: answer again")

	require.True(t, a.Begin())
	a.Written(native.Unconfirmed(context.DeadlineExceeded))
	assert.Equal(t, agentio.Answer{Answering: true, Unconfirmed: true}, a, "the agent may yet get it")
	assert.False(t, a.Begin())

	a = agentio.Answer{}
	require.True(t, a.Begin())
	a.Written(nil)
	assert.Equal(t, agentio.Answer{Answering: true}, a)
}
