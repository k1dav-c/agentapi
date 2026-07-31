package termexec

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"fmt"

	"github.com/ActiveState/termtest/xpty"
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

	waitOnce   sync.Once
	waitState  *os.ProcessState
	waitErr    error
	waitDone   chan struct{}

	// readerDone is closed when the PTY reader goroutine exits.
	// Use ReaderDone() to get the channel, ReaderErr() for the cause.
	readerDone chan struct{}
	readerErr  error // written before readerDone is closed, read-safe after
}

type StartProcessConfig struct {
	Program        string
	Args           []string
	TerminalWidth  uint16
	TerminalHeight uint16
	Clock          quartz.Clock
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
	execCmd.Env = append(os.Environ(), "TERM=vt100")
	if err := xp.StartProcessInTerminal(execCmd); err != nil {
		xp.Close()
		return nil, err
	}

	process := &Process{xp: xp, execCmd: execCmd, clock: clock, waitDone: make(chan struct{}), readerDone: make(chan struct{})}

	go func() {
		// Signal reader exit so callers (e.g. the supervisor) can detect
		// when the PTY output stream ends.
		defer close(process.readerDone)

		// Working around xpty concurrency limitations:
		//
		// We need to track when the terminal screen was last updated (for ReadScreen),
		// but xpty only updates terminal state through xp.ReadRune(), which blocks
		// indefinitely until the process outputs data and panics when SetReadDeadline
		// is used.
		//
		// If we wrapped ReadRune + lastScreenUpdate in a mutex, this goroutine would
		// hold the lock while waiting for process output, starving ReadScreen callers.
		//
		// Instead, we directly use xpty's internal components:
		// - pp.ReadRune() — handles the blocking read from the process (lock-free)
		// - xp.Term.WriteRune() — updates the terminal state (under mutex)
		//
		// Warning: This depends on xpty internals (the unexported "pp" field) and
		// may break if xpty changes. The xpty version is pinned to v0.6.0.
		// A proper fix would require forking xpty or getting upstream changes.
		pp := util.GetUnexportedField(xp, "pp").(*xpty.PassthroughPipe)
		injector := &wideCharInjector{}
		for {
			r, _, err := pp.ReadRune()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Normal shutdown: PTY closed or process exited.
					logger.Debug("PTY reader stopped: EOF")
				} else {
					logger.Error("PTY reader stopped: unexpected error reading from pseudo terminal", "error", err)
				}
				process.readerErr = err
				return
			}
			process.screenUpdateLock.Lock()
			// writing to the terminal updates its state. without it,
			// xp.State will always return an empty string
			xp.Term.WriteRune(r)
			if injector.shouldPad(r) {
				// Keep the emulator's cursor in sync with the two-column
				// layout the application assumes for wide runes. The
				// padding is stripped in ReadScreen. See widechar.go.
				xp.Term.WriteRune(widePadRune)
			}
			process.lastScreenUpdate = clock.Now()
			process.screenUpdateLock.Unlock()
		}
	}()

	return process, nil
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

func (p *Process) Signal(sig os.Signal) error {
	return p.execCmd.Process.Signal(sig)
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
			state := p.xp.State.String()
			p.screenUpdateLock.RUnlock()
			return stripWidePadding(state)
		}
		p.screenUpdateLock.RUnlock()
		t := p.clock.NewTimer(16 * time.Millisecond)
		<-t.C
		t.Stop()
	}
	p.screenUpdateLock.RLock()
	state := p.xp.State.String()
	p.screenUpdateLock.RUnlock()
	return stripWidePadding(state)
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
