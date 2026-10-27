package agentio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/native/procutil"
)

// ErrClosed is returned by Host.Launch after Close.
var ErrClosed = errors.New("the adapter was closed")

// InterruptGrace is how long Host.Close waits by default for a turn it
// interrupted to end.
const InterruptGrace = 3 * time.Second

// HostConfig configures a Host. Launch reads Start and Writer, Close the
// rest; tests change the graces before Close.
type HostConfig struct {
	// Name names the agent in messages, e.g. "Claude Code".
	Name   string
	Logger *slog.Logger
	// Clock times procutil.Stop's graces; nil means the real clock.
	Clock quartz.Clock
	// Start starts the process; nil means procutil.Start.
	Start procutil.StartFunc
	// Writer gets the process's stdin once it started.
	Writer *Writer
	// StdinGrace and TermGrace are procutil.Stop's; zero means 3 s and
	// 2 s.
	StdinGrace, TermGrace time.Duration
	// InterruptGrace is how long Close waits for a turn it interrupted to
	// end; zero means InterruptGrace.
	InterruptGrace time.Duration
}

// Host runs an adapter's agent process: it starts it unless the adapter
// was closed meanwhile, records its exit, explains a start that failed,
// and stops it, the running turn interrupted first.
type Host struct {
	HostConfig

	mu       sync.Mutex
	proc     procutil.Process
	launched bool
	closing  bool

	// exited is closed once the process exited, or without a process once
	// Close was called or the start failed; exitErr is the exit error.
	exited   chan struct{}
	exitOnce sync.Once
	exitErr  error
	stopOnce sync.Once
}

// NewHost returns a Host for cfg.
func NewHost(cfg HostConfig) *Host {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if cfg.Start == nil {
		cfg.Start = procutil.Start
	}
	return &Host{HostConfig: cfg, exited: make(chan struct{})}
}

// Launch starts the process for cmd, hands its stdin to the Writer and
// records its exit. It returns ErrClosed after Close: a restart may have
// closed the adapter while it prepared the start (Claude Code's login
// check takes up to 10 s), and the process would run next to the new one.
// A process that started while Close ran, which found none to stop, is
// stopped here. A Host launches once: a start that fails finishes it.
func (h *Host) Launch(cmd procutil.Command) (procutil.Process, error) {
	h.mu.Lock()
	switch {
	case h.closing:
		h.mu.Unlock()
		return nil, ErrClosed
	case h.launched:
		h.mu.Unlock()
		return nil, errors.New("the agent was started already")
	}
	h.launched = true
	h.mu.Unlock()

	proc, err := h.Start(cmd)
	h.mu.Lock()
	closing := h.closing
	if err == nil && !closing {
		h.proc = proc
	}
	h.closing = h.closing || err != nil
	h.mu.Unlock()
	switch {
	case err != nil:
		h.markExited(nil)
		return nil, err
	case closing:
		_ = proc.Stdin().Close()
		_ = proc.Kill()
		return nil, ErrClosed
	}
	h.Writer.Attach(proc.Stdin())
	go func() {
		err := proc.Wait()
		h.markExited(procutil.WithStderr(err, proc.StderrTail()))
	}()
	return proc, nil
}

func (h *Host) markExited(err error) {
	h.exitOnce.Do(func() {
		h.exitErr = err
		close(h.exited)
	})
}

// Process returns the process, or nil before it started.
func (h *Host) Process() procutil.Process {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.proc
}

// Closing reports whether Close was called, or the start failed.
func (h *Host) Closing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closing
}

// Exited is closed once the process exited, or without a process once
// Close was called or the start failed.
func (h *Host) Exited() <-chan struct{} {
	return h.exited
}

// ExitErr returns the process's exit error, with the end of its stderr,
// once Exited is closed.
func (h *Host) ExitErr() error {
	select {
	case <-h.exited:
		return h.exitErr
	default:
		return nil
	}
}

// StartError stops the process of a start that failed with err and says
// why. Once the agent's output ended (outputEnded), its exit error, or
// else its stderr, says more than err: an unknown session to resume, a bad
// option.
func (h *Host) StartError(err error, outputEnded bool) error {
	h.Close(nil)
	if !outputEnded {
		return err
	}
	exitErr := h.ExitErr()
	if exitErr == nil {
		// A clean exit after a fatal error.
		if proc := h.Process(); proc != nil {
			exitErr = procutil.WithStderr(err, proc.StderrTail())
		} else {
			exitErr = err
		}
	}
	return fmt.Errorf("%s exited during startup: %w", h.Name, exitErr)
}

// Close stops the process and waits for it to exit. First, interrupt, if
// set, interrupts a turn that runs and returns a channel its end closes,
// or nil without a turn; ctx ends once Close stops waiting. Close waits
// for the turn to end, so the agent's session log doesn't end in a tool
// call without result, for the exit, or InterruptGrace, in real time: the
// clock could be one nobody advances once the adapter is retired. Then
// procutil.Stop closes stdin and escalates to SIGTERM and SIGKILL on the
// clock. Without a process, Close returns at once.
func (h *Host) Close(interrupt func(ctx context.Context) <-chan struct{}) {
	h.mu.Lock()
	h.closing = true
	proc := h.proc
	h.mu.Unlock()
	if proc == nil {
		h.markExited(nil)
		return
	}
	h.stopOnce.Do(func() {
		if interrupt != nil {
			h.interrupt(interrupt)
		}
		procutil.Stop(proc, h.exited, procutil.StopOptions{
			StdinGrace: h.StdinGrace,
			TermGrace:  h.TermGrace,
			Clock:      h.Clock,
			Logger:     h.Logger,
			Name:       h.Name,
		})
	})
	// Stop returns after SIGKILL without waiting for the exit; a reload
	// resumes the same session at once, which the old process must not
	// still hold.
	<-h.exited
}

// interrupt interrupts the running turn and waits for it to end, for the
// exit or for the grace to pass.
func (h *Host) interrupt(interrupt func(ctx context.Context) <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := interrupt(ctx)
	if ended == nil {
		return
	}
	grace := h.InterruptGrace
	if grace <= 0 {
		grace = InterruptGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-ended:
	case <-h.exited:
	case <-timer.C:
		h.Logger.Warn(h.Name + " didn't end its turn in time; closing it anyway")
	}
}
