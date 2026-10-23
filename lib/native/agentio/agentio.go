// Package agentio is the plumbing the native adapters share to talk to
// their agent process over JSON lines: reading its output line by line,
// writing records to its stdin, and matching its responses to the
// requests waiting for them.
package agentio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/coder/agentapi/lib/native"
)

// MaxLine is the longest line ReadLines passes on by default: records with
// images are large.
const MaxLine = 64 << 20

// ReadLines reads r until it ends and calls handle with each line that
// isn't blank, without its line ending. Lines are split on "\n" only (an
// agent's JSON can contain U+2028), and a line longer than maxLine (0
// means MaxLine) is dropped with a warning. handle may keep the line. name
// names the agent in log messages, which never include the line: it can
// hold the user's prompt.
func ReadLines(r io.Reader, name string, maxLine int, logger *slog.Logger, handle func(line []byte)) {
	if maxLine <= 0 {
		maxLine = MaxLine
	}
	reader := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	dropping := false
	for {
		chunk, err := reader.ReadSlice('\n')
		switch {
		case dropping:
		case len(buf)+len(chunk) > maxLine:
			dropping = true
			buf = nil
			logger.Warn("Dropped an oversized line of agent output", "agent", name, "limit", maxLine)
		default:
			buf = append(buf, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if line := bytes.TrimRight(buf, "\r\n"); !dropping && len(bytes.TrimSpace(line)) > 0 {
			handle(line)
		}
		buf, dropping = nil, false
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				logger.Debug("Agent output closed", "agent", name, "error", err)
			}
			return
		}
	}
}

// Writer writes records to an agent's stdin as JSON, one per line, without
// interleaving them.
type Writer struct {
	name string
	mu   sync.Mutex
	w    io.Writer
}

// NewWriter returns a Writer for the agent name; it writes once Attach
// gave it the agent's stdin.
func NewWriter(name string) *Writer {
	return &Writer{name: name}
}

// Attach makes w write to stdin.
func (w *Writer) Attach(stdin io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.w = stdin
}

// States of a write; see Write.
const (
	writeWaiting int32 = iota
	writeStarted
	writeAbandoned
)

// Write writes v as one line. It returns native.ErrNotStarted before
// Attach.
//
// An agent that stops reading its input blocks a write for good, so Write
// gives up when ctx ends. A record that was still waiting behind another
// write is then never written, and Write returns ctx.Err(). One whose
// write had begun can't be taken back without breaking the line the agent
// reads: it goes on in the background, the agent may yet get it, and
// Write returns ctx.Err() marked native.ErrUnconfirmed. Closing the
// agent's input ends a write that waits for good.
func (w *Writer) Write(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	var state atomic.Int32
	done := make(chan error, 1)
	go func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !state.CompareAndSwap(writeWaiting, writeStarted) {
			return
		}
		done <- w.writeLocked(data)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	if state.CompareAndSwap(writeWaiting, writeAbandoned) {
		return ctx.Err()
	}
	select {
	case err := <-done:
		// It ended just now.
		return err
	default:
		return native.Unconfirmed(fmt.Errorf("write to %s: %w", w.name, ctx.Err()))
	}
}

// writeLocked writes data to the agent's stdin. Caller holds w.mu.
func (w *Writer) writeLocked(data []byte) error {
	if w.w == nil {
		return native.ErrNotStarted
	}
	if _, err := w.w.Write(data); err != nil {
		return fmt.Errorf("write to %s: %w", w.name, err)
	}
	return nil
}

// Writing reports whether a write is under way, waiting for the agent to
// read: tests catch an adapter blocked on a write with it.
func (w *Writer) Writing() bool {
	if w.mu.TryLock() {
		w.mu.Unlock()
		return false
	}
	return true
}

// WriteAsync writes v from a goroutine of its own, for the adapter's loop,
// which must not wait for an agent that doesn't read. A failure is logged.
func (w *Writer) WriteAsync(v any, logger *slog.Logger) {
	go func() {
		if err := w.Write(context.Background(), v); err != nil {
			logger.Debug("Failed to write to the agent", "agent", w.name, "error", err)
		}
	}()
}

// Calls matches an agent's responses to the requests waiting for them,
// by the requests' IDs. Once Close was called, because the agent's output
// ended, every request still waiting gets Close's error, and new ones are
// refused with it: a response either arrives first or the error does,
// and nothing waits forever.
type Calls[K comparable, R any] struct {
	mu     sync.Mutex
	calls  map[K]*Call[R]
	closed error
}

// Call is a request waiting for its response.
type Call[R any] struct {
	onResult func(R, error)
	done     chan result[R]
}

type result[R any] struct {
	value R
	err   error
}

// Add registers the request id. onResult, if set, is called by Resolve,
// on the adapter's loop, before the response is handed to the caller. It
// returns Close's error once the calls were closed.
func (c *Calls[K, R]) Add(id K, onResult func(R, error)) (*Call[R], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed != nil {
		return nil, c.closed
	}
	if c.calls == nil {
		c.calls = make(map[K]*Call[R])
	}
	call := &Call[R]{onResult: onResult, done: make(chan result[R], 1)}
	c.calls[id] = call
	return call, nil
}

// Resolve hands the response to request id over, after its onResult. It
// reports false for a request nobody waits for (it timed out, or was never
// made).
func (c *Calls[K, R]) Resolve(id K, value R, err error) bool {
	c.mu.Lock()
	call := c.calls[id]
	delete(c.calls, id)
	c.mu.Unlock()
	if call == nil {
		return false
	}
	if call.onResult != nil {
		call.onResult(value, err)
	}
	call.done <- result[R]{value, err}
	return true
}

// Drop forgets request id, whose caller stopped waiting.
func (c *Calls[K, R]) Drop(id K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.calls, id)
}

// Close fails the requests still waiting with err, and refuses new ones
// with it.
func (c *Calls[K, R]) Close(err error) {
	c.mu.Lock()
	calls := c.calls
	c.calls = nil
	if c.closed == nil {
		c.closed = err
	}
	c.mu.Unlock()
	for _, call := range calls {
		call.done <- result[R]{err: err}
	}
}

// Wait waits for the response, or for ctx to end. A caller that gives up
// drops the request.
func (call *Call[R]) Wait(ctx context.Context) (R, error) {
	select {
	case r := <-call.done:
		return r.value, r.err
	case <-ctx.Done():
		var zero R
		return zero, ctx.Err()
	}
}
