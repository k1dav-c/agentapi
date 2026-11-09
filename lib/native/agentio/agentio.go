// Package agentio is the plumbing the native adapters share to run their
// agent process and talk to it over JSON lines. Host starts and stops the
// process and runs the adapter's loop on its output; Writer writes records
// to its stdin; Calls matches its responses to the requests waiting for
// them. Decode and Drift read records leniently and warn when the protocol
// changed; Throttle and InterruptDeadline time the loop's snapshots and
// the end of an interrupted turn.
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
//
// A caller can stop waiting while the request stays registered (Detach):
// the agent reported it under way before its response, or the caller gave
// up on a request the agent may still carry out. Its response, once it
// comes, is then the adapter's alone.
type Calls[K comparable, R any] struct {
	mu     sync.Mutex
	calls  map[K]*Call[R]
	closed error
}

// Call is a request waiting for its response.
type Call[R any] struct {
	onResult func(R, error)
	done     chan result[R]
	// waiting is set while a caller waits for done; cleared by the one
	// hand-over done gets.
	waiting atomic.Bool
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
	call.waiting.Store(true)
	c.calls[id] = call
	return call, nil
}

// Resolve hands the response to request id over, after its onResult. It
// reports false for a request nobody waits for (it timed out, or was never
// made).
func (c *Calls[K, R]) Resolve(id K, value R, err error) bool {
	call, _ := c.Take(id)
	if call == nil {
		return false
	}
	if call.onResult != nil {
		call.onResult(value, err)
	}
	call.Deliver(value, err)
	return true
}

// Take removes request id, for an adapter that handles its response
// itself, and reports whether a caller still waits for it, which Deliver
// then hands the response to. call is nil for a request that isn't
// registered.
func (c *Calls[K, R]) Take(id K) (call *Call[R], waiting bool) {
	c.mu.Lock()
	call = c.calls[id]
	delete(c.calls, id)
	c.mu.Unlock()
	if call == nil {
		return nil, false
	}
	return call, call.waiting.Load()
}

// Detach hands value and err to the caller waiting for request id now,
// and keeps the request registered: its response, once it comes, goes to
// Take or onResult only. It reports false if id isn't registered or its
// caller was handed something already.
func (c *Calls[K, R]) Detach(id K, value R, err error) bool {
	c.mu.Lock()
	call := c.calls[id]
	c.mu.Unlock()
	if call == nil {
		return false
	}
	return call.Deliver(value, err)
}

// WriteFailed handles request id whose write failed with err: it is
// dropped, unless the write may yet reach the agent (native.ErrUnconfirmed)
// and it has an onResult, which its response then runs.
func (c *Calls[K, R]) WriteFailed(id K, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.calls[id]
	if call == nil {
		return
	}
	if call.onResult != nil && errors.Is(err, native.ErrUnconfirmed) {
		call.waiting.Store(false)
		return
	}
	delete(c.calls, id)
}

// Drop forgets request id, whose caller stopped waiting.
func (c *Calls[K, R]) Drop(id K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.calls, id)
}

// Await waits for the response to request id, which was sent. A caller
// that stops waiting, or whose request Close failed, gets the error marked
// native.ErrUnconfirmed: the agent may carry it out all the same. A
// request with an onResult stays registered when its caller stops
// waiting, so a response that comes later still runs it; one without is
// dropped.
func (c *Calls[K, R]) Await(ctx context.Context, id K, call *Call[R]) (R, error) {
	var r result[R]
	select {
	case r = <-call.done:
	case <-ctx.Done():
		if call.waiting.CompareAndSwap(true, false) {
			if call.onResult == nil {
				c.Drop(id)
			}
			var zero R
			return zero, native.Unconfirmed(ctx.Err())
		}
		// Handed over meanwhile.
		r = <-call.done
	}
	if r.err != nil {
		c.Drop(id)
		return r.value, native.Unconfirmed(r.err)
	}
	return r.value, nil
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
		var zero R
		call.Deliver(zero, err)
	}
}

// Deliver hands value and err to the caller waiting for call, unless it
// was handed something already. It reports whether it handed them over.
func (call *Call[R]) Deliver(value R, err error) bool {
	if !call.waiting.CompareAndSwap(true, false) {
		return false
	}
	call.done <- result[R]{value, err}
	return true
}

// Wait waits for the response, or for ctx to end; a response already
// handed over wins over ctx. A caller that gives up drops the request, or
// detaches it if the agent may still answer.
func (call *Call[R]) Wait(ctx context.Context) (R, error) {
	select {
	case r := <-call.done:
		return r.value, r.err
	case <-ctx.Done():
		select {
		case r := <-call.done:
			return r.value, r.err
		default:
		}
		var zero R
		return zero, ctx.Err()
	}
}
