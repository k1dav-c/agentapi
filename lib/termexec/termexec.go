package termexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ActiveState/termtest/xpty"
	"github.com/ActiveState/vt10x"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/util"
	"github.com/coder/quartz"
)

type Process struct {
	xp               *xpty.Xpty
	execCmd          *exec.Cmd
	screenUpdateLock sync.RWMutex
	lastScreenUpdate time.Time
	clock            quartz.Clock

	waitOnce  sync.Once
	waitState *os.ProcessState
	waitErr   error
	waitDone  chan struct{}

	// readerDone is closed when the PTY reader goroutine exits.
	// Use ReaderDone() to get the channel, ReaderErr() for the cause.
	readerDone    chan struct{}
	screenUpdates chan struct{}
	readerErr     error // written before readerDone is closed, read-safe after

	// Render cache: rendering the vt10x state is O(rows*cols) and the
	// snapshot loop calls ReadScreen every 25ms, so reuse the previous
	// render while no PTY output has arrived. Returning the identical
	// string also lets downstream equality checks short-circuit on
	// pointer equality. renderMu is always acquired while holding
	// screenUpdateLock (read or write side is fine for cache purposes).
	renderMu       sync.Mutex
	renderValid    bool
	renderedAt     time.Time
	renderedScreen string
}

type StartProcessConfig struct {
	Program        string
	Args           []string
	TerminalWidth  uint16
	TerminalHeight uint16
	Clock          quartz.Clock
	// Env is the process environment; nil means os.Environ(). TERM is set.
	Env []string
}

func StartProcess(ctx context.Context, args StartProcessConfig) (*Process, error) {
	logger := logctx.From(ctx)
	clock := args.Clock
	if clock == nil {
		clock = quartz.NewReal()
	}
	xp, err := xpty.New(args.TerminalWidth, args.TerminalHeight, false)
	if err != nil {
		return nil, err
	}
	execCmd := exec.Command(args.Program, args.Args...)
	// vt100 is the terminal type that the vt10x library emulates.
	// Setting this signals to the process that it should only use compatible
	// escape sequences.
	env := args.Env
	if env == nil {
		env = os.Environ()
	}
	execCmd.Env = append(append([]string(nil), env...), "TERM=vt100")
	if err := xp.StartProcessInTerminal(execCmd); err != nil {
		_ = xp.Close()
		return nil, err
	}

	process := &Process{xp: xp, execCmd: execCmd, clock: clock, waitDone: make(chan struct{}), readerDone: make(chan struct{}), screenUpdates: make(chan struct{}, 1)}

	go func() {
		// Signal reader exit so callers (e.g. the supervisor) can detect
		// when the PTY output stream ends.
		defer close(process.readerDone)

		// Working around xpty: it updates the terminal only through
		// xp.ReadRune, which blocks until the process writes and panics
		// with a read deadline, and starts a goroutine, two channels and a
		// timer for every rune (an 80 KB burst of output took 212 ms of CPU
		// and 40 MB of allocations). The reader reads the output from the
		// bufio.Reader xpty reads it from, in chunks, and writes each chunk
		// to the terminal under one lock, which ReadScreen callers don't
		// wait for while the reader waits for output.
		//
		// Warning: this depends on xpty internals (the unexported "pp" and
		// "rdr" fields), pinned to v0.6.0.
		pp := util.GetUnexportedField(xp, "pp").(*xpty.PassthroughPipe)
		output := util.GetUnexportedField(pp, "rdr").(*bufio.Reader)
		err := process.readOutput(output)
		if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EIO) {
			// Normal shutdown: PTY closed or process exited.
			logger.Debug("PTY reader stopped", "error", err)
		} else {
			logger.Error("PTY reader stopped: unexpected error reading from pseudo terminal", "error", err)
		}
		process.readerErr = err
	}()

	return process, nil
}

// readOutput writes the process's output to the terminal emulator until
// it ends: a chunk at a time, under one lock. An incomplete UTF-8 sequence
// at a chunk's end waits for the next chunk.
func (p *Process) readOutput(output io.Reader) error {
	injector := &wideCharInjector{}
	buf := make([]byte, 32<<10)
	var partial []byte
	for {
		n, err := output.Read(buf)
		if n > 0 {
			data := buf[:n]
			if len(partial) > 0 {
				data = append(partial, data...)
			}
			p.screenUpdateLock.Lock()
			used := writeOutput(p.xp.Term, injector, data, err != nil)
			p.lastScreenUpdate = p.clock.Now()
			p.screenUpdateLock.Unlock()
			partial = slices.Clone(data[used:])
			p.notifyScreenUpdate()
		}
		if err != nil {
			return err
		}
	}
}

// writeOutput writes the runes of data to term and returns how many bytes
// it used: all of them, unless data ends inside a UTF-8 sequence and more
// output may follow (not last). A byte that isn't UTF-8 is written as
// U+FFFD. After a double-width rune it writes a padding rune, which keeps
// the emulator's cursor in step with the two columns the application
// assumes; ReadScreen strips it (see widechar.go).
func writeOutput(term interface{ WriteRune(rune) }, injector *wideCharInjector, data []byte, last bool) int {
	i := 0
	for i < len(data) {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 && !last && !utf8.FullRune(data[i:]) {
			break
		}
		term.WriteRune(r)
		if injector.shouldPad(r) {
			term.WriteRune(widePadRune)
		}
		i += size
	}
	return i
}

// ReaderDone returns a channel that is closed when the PTY reader goroutine exits.
// After this channel closes, ReadScreen will always return a stale snapshot and
// Write will accept input that the agent will never see.
func (p *Process) ReaderDone() <-chan struct{} {
	return p.readerDone
}

// ReaderErr returns the error that caused the PTY reader goroutine to stop.
// Only valid after ReaderDone() is closed; returns nil before that.
func (p *Process) ReaderErr() error {
	return p.readerErr
}

// Pid returns the OS process ID of the child process.
func (p *Process) Pid() int {
	return p.execCmd.Process.Pid
}

// ReadScreen returns the contents of the terminal window.
// It waits for the terminal to be stable for 16ms before
// returning, or 48 ms since it's called, whichever is sooner.
//
// This logic acts as a kind of vsync. Agents regularly redraw
// parts of the screen. If we naively snapshotted the screen,
// we'd often capture it while it's being updated. This would
// result in a malformed agent message being returned to the
// user.
func (p *Process) ReadScreen() string {
	for range 3 {
		p.screenUpdateLock.RLock()
		if p.clock.Since(p.lastScreenUpdate) >= 16*time.Millisecond {
			screen := p.renderScreenRLocked()
			p.screenUpdateLock.RUnlock()
			return screen
		}
		p.screenUpdateLock.RUnlock()
		t := p.clock.NewTimer(16 * time.Millisecond)
		<-t.C
		t.Stop()
	}
	p.screenUpdateLock.RLock()
	screen := p.renderScreenRLocked()
	p.screenUpdateLock.RUnlock()
	return screen
}

// renderScreenRLocked renders the terminal state to a string, reusing the
// cached render when no PTY output has arrived since it was taken.
// Caller MUST hold p.screenUpdateLock (read lock is sufficient: the lock
// prevents the reader goroutine from mutating terminal state and
// lastScreenUpdate, while renderMu serializes cache access between
// concurrent ReadScreen callers).
func (p *Process) renderScreenRLocked() string {
	ts := p.lastScreenUpdate
	p.renderMu.Lock()
	defer p.renderMu.Unlock()
	if p.renderValid && p.renderedAt.Equal(ts) {
		return p.renderedScreen
	}
	screen := renderScreen(p.xp.State)
	p.renderValid = true
	p.renderedAt = ts
	p.renderedScreen = screen
	return screen
}

// renderScreen renders only rows 0..lastContentRow. For sequential-output
// agents the cursor tracks the content extent, but TUI agents (e.g. Claude
// Code's Ink framework) may position content below the cursor via absolute
// cursor movement. We use max(cursorY, lastNonEmptyRow) to cover both cases,
// cutting the per-snapshot cost from 80×1000 to 80×(actual content rows).
func renderScreen(state *vt10x.State) string {
	state.Lock()
	defer state.Unlock()
	rows, cols := state.Size()
	_, cursorY := state.Cursor()
	// Scan backwards from the bottom to find the last row with content.
	// This handles TUI agents that write below the cursor position.
	lastContent := cursorY
	for y := rows - 1; y > cursorY; y-- {
		for x := 0; x < cols; x++ {
			r, _, _ := state.Cell(x, y)
			if r != 0 && r != ' ' && r != widePadRune {
				lastContent = y
				goto found
			}
		}
	}
found:
	renderRows := lastContent + 1
	if renderRows > rows {
		renderRows = rows
	}
	var screen strings.Builder
	screen.Grow(renderRows * (cols + 1))
	for y := 0; y < renderRows; y++ {
		for x := 0; x < cols; x++ {
			r, _, _ := state.Cell(x, y)
			if r == widePadRune {
				continue
			}
			if r >= 0 && r < 128 {
				screen.WriteByte(byte(r))
			} else {
				screen.WriteRune(r)
			}
		}
		screen.WriteByte('\n')
	}
	return screen.String()
}

// Write sends input to the process via the pseudo terminal.
func (p *Process) Write(data []byte) (int, error) {
	return p.xp.TerminalInPipe().Write(data)
}

// Close closes the process using a SIGINT signal or forcefully killing it if the process
// does not exit after the timeout. It then closes the pseudo terminal.
func (p *Process) Close(logger *slog.Logger, timeout time.Duration) error {
	logger.Info("Closing process")
	// Always close the PTY, even if signaling fails.
	defer func() {
		if err := p.xp.Close(); err != nil {
			logger.Error("Failed to close pseudo terminal", "error", err)
		}
	}()

	if err := p.execCmd.Process.Signal(os.Interrupt); err != nil {
		// If the process already exited, SIGINT fails — that's fine,
		// just ensure we still close the PTY (handled by defer above).
		if !errors.Is(err, os.ErrProcessDone) {
			logger.Error("Failed to send SIGINT to process", "error", err)
		}
		return nil
	}

	// Wait for the process to exit or force-kill after timeout.
	// Use doWait to avoid racing with a concurrent Wait() call.
	go p.doWait()

	timeoutTimer := p.clock.NewTimer(timeout)
	defer timeoutTimer.Stop()
	select {
	case <-timeoutTimer.C:
		if err := p.execCmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to forcefully kill the process: %w", err)
		}
		// Don't wait for the process to exit to avoid hanging indefinitely.
	case <-p.waitDone:
		if p.waitErr != nil {
			var pathErr *os.SyscallError
			// ECHILD is expected if the process has already exited.
			if !(errors.As(p.waitErr, &pathErr) && errors.Is(pathErr.Err, syscall.ECHILD)) {
				return fmt.Errorf("process exited with error: %w", p.waitErr)
			}
		}
	}
	return nil
}

var ErrNonZeroExitCode = errors.New("non-zero exit code")

// doWait performs the actual os.Process.Wait exactly once, safe for concurrent callers.
func (p *Process) doWait() {
	p.waitOnce.Do(func() {
		p.waitState, p.waitErr = p.execCmd.Process.Wait()
		close(p.waitDone)
	})
}

// Wait waits for the process to exit.
func (p *Process) Wait() error {
	p.doWait()
	if p.waitErr != nil {
		return fmt.Errorf("process exited with error: %w", p.waitErr)
	}
	if p.waitState != nil && p.waitState.ExitCode() != 0 {
		return ErrNonZeroExitCode
	}
	return nil
}

// ScreenUpdates coalesces PTY output notifications for one snapshot consumer.
// The channel remains open when the process exits, avoiding a busy receive loop.
func (p *Process) ScreenUpdates() <-chan struct{} { return p.screenUpdates }

func (p *Process) notifyScreenUpdate() {
	select {
	case p.screenUpdates <- struct{}{}:
	default:
	}
}
