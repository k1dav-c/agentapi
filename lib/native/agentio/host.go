package agentio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

var (
	// ErrClosed is returned by Host.Launch after Close.
	ErrClosed = errors.New("the adapter was closed")
	// ErrLoopEnded is returned by Host.Post once the loop ended.
	ErrLoopEnded = errors.New("the agent's output ended")
)

// InterruptGrace is how long Host.Close waits by default for a turn it
// interrupted to end.
const InterruptGrace = 3 * time.Second

// HostConfig configures a Host. Launch reads Start, Writer and MaxLine,
// Close the rest; tests change the graces before Close.
type HostConfig struct {
	// Name names the agent in messages, e.g. "Claude Code".
	Name   string
	Logger *slog.Logger
	// Clock drives the loop's timer and times procutil.Stop's graces; nil
	// means the real clock.
	Clock quartz.Clock
	// Start starts the process; nil means procutil.Start.
	Start procutil.StartFunc
	// Writer gets the process's stdin once it started.
	Writer *Writer
	// MaxLine is the longest line of output the loop gets (see ReadLines);
	// 0 means MaxLine.
	MaxLine int
	// StdinGrace and TermGrace are procutil.Stop's; zero means 3 s and
	// 2 s.
	StdinGrace, TermGrace time.Duration
	// InterruptGrace is how long Close waits for a turn it interrupted to
	// end; zero means InterruptGrace.
	InterruptGrace time.Duration
}

// Loop is the adapter's side of the loop a Host runs. The loop is one
// goroutine: it calls these, and the functions posted to it, one at a
// time, so they can use the adapter's loop state and call the Sink, whose
// calls must not overlap.
type Loop struct {
	// Line handles a line of the agent's output.
	Line func(line []byte)
	// NextWake returns when the loop has to act without output (to send a
	// snapshot the throttle held back, or to end an interrupted turn the
	// agent didn't end), or the zero time. The loop asks after every line,
	// posted function and wake.
	NextWake func() time.Time
	// Wake runs once the time NextWake returned came. It checks what is
	// due: a wake can come early, for a time NextWake moved since.
	Wake func()
	// Ended runs once the output ended, the loop's last call.
	Ended func()
}

// Host runs an adapter's agent process. It starts it, unless the adapter
// was closed meanwhile, and hands its output to one loop goroutine, line
// by line, with the functions posted to it and one timer. It records the
// exit, explains a start that failed, and stops the process, the running
// turn interrupted first.
//
// The process has exited, for Wait and Exited, once it is gone and the
// loop has handled its output, or OutputGrace after it is gone at the
// latest: a process the agent started can keep the output open, and keep
// writing to it.
type Host struct {
	HostConfig

	mu       sync.Mutex
	proc     procutil.Process
	launched bool
	closing  bool

	// posted carries functions to the loop. Unbuffered: one handed over is
	// one the loop runs.
	posted chan func()
	// outputEnded is closed once the loop saw the end of the output,
	// before Ended runs; loopDone once the loop ended, or when it never
	// runs.
	outputEnded chan struct{}
	loopDone    chan struct{}
	loopOnce    sync.Once
	// procExited is closed once the process exited.
	procExited chan struct{}
	// exited is closed once the process exited (see Host), or without a
	// process once Close was called or the start failed; exitErr is the
	// exit error.
	exited   chan struct{}
	exitOnce sync.Once
	exitErr  error
	stopOnce sync.Once

	// The loop's timer; only the loop uses them. wakeSeq tells the wake
	// of the timer set last from one it replaced.
	wakeTimer *quartz.Timer
	wakeAt    time.Time
	wakeSeq   int
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
	return &Host{
		HostConfig:  cfg,
		posted:      make(chan func()),
		outputEnded: make(chan struct{}),
		loopDone:    make(chan struct{}),
		procExited:  make(chan struct{}),
		exited:      make(chan struct{}),
	}
}

// Launch starts the process for cmd, hands its stdin to the Writer, and
// runs loop on its output. It returns ErrClosed after Close: a restart may
// have closed the adapter while it prepared the start (Claude Code's login
// check takes up to 10 s), and the process would run next to the new one.
// A process that started while Close ran, which found none to stop, is
// stopped here. A Host launches once: a start that fails finishes it.
func (h *Host) Launch(cmd procutil.Command, loop Loop) (procutil.Process, error) {
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
		h.endLoop()
		h.finish(nil)
		return nil, err
	case closing:
		// Close found no process; it finished the host.
		h.endLoop()
		_ = proc.Stdin().Close()
		_ = proc.Kill()
		return nil, ErrClosed
	}
	h.Writer.Attach(proc.Stdin())
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		// Unbuffered: a line is read once the loop took the one before.
		ReadLines(proc.Stdout(), h.Name, h.MaxLine, h.Logger, func(line []byte) { lines <- line })
	}()
	go h.run(lines, loop)
	go h.wait(proc)
	return proc, nil
}

// run is the loop.
func (h *Host) run(lines <-chan []byte, loop Loop) {
	defer h.endLoop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				h.setWake(time.Time{}, nil)
				close(h.outputEnded)
				loop.Ended()
				return
			}
			loop.Line(line)
		case f := <-h.posted:
			f()
		}
		h.setWake(loop.NextWake(), loop.Wake)
	}
}

// setWake sets the loop's timer to post wake at next, or stops it if next
// is zero. Loop only.
func (h *Host) setWake(next time.Time, wake func()) {
	if next.Equal(h.wakeAt) {
		return
	}
	if h.wakeTimer != nil {
		h.wakeTimer.Stop()
		h.wakeTimer = nil
	}
	h.wakeAt = next
	h.wakeSeq++
	if next.IsZero() {
		return
	}
	seq := h.wakeSeq
	h.wakeTimer = h.Clock.AfterFunc(max(h.Clock.Until(next), 0), func() {
		_ = h.Post(context.Background(), func() {
			if seq == h.wakeSeq {
				// The timer fired; the loop sets it again.
				h.wakeTimer, h.wakeAt = nil, time.Time{}
			}
			wake()
		})
	}, "agentio", "wake")
}

// wait records the exit of proc. The process counts as exited once the
// loop ended too, or OutputGrace later.
func (h *Host) wait(proc procutil.Process) {
	err := procutil.WithStderr(proc.Wait(), proc.StderrTail())
	close(h.procExited)
	// Real time, as the clock could be one nobody advances once the
	// adapter is retired.
	timer := time.NewTimer(procutil.OutputGrace)
	defer timer.Stop()
	select {
	case <-h.loopDone:
	case <-timer.C:
		h.Logger.Warn(h.Name + " exited, but its output is still open; not waiting for the rest")
	}
	h.finish(err)
}

// endLoop marks the loop ended, or that it never runs.
func (h *Host) endLoop() {
	h.loopOnce.Do(func() { close(h.loopDone) })
}

func (h *Host) finish(err error) {
	h.exitOnce.Do(func() {
		h.exitErr = err
		close(h.exited)
	})
}

// Post hands f to the loop, which runs it in order with the lines. It
// returns ErrLoopEnded if the loop ended, and ctx's error if ctx ends,
// before the loop took f.
func (h *Host) Post(ctx context.Context, f func()) error {
	select {
	case h.posted <- f:
		return nil
	case <-h.loopDone:
		return ErrLoopEnded
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RunOnLoop runs f on the loop and waits for it. It reports false if the
// loop ended without running it.
func (h *Host) RunOnLoop(f func()) bool {
	done := make(chan struct{})
	if h.Post(context.Background(), func() {
		f()
		close(done)
	}) != nil {
		return false
	}
	<-done
	return true
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

// OutputEnded reports whether the loop saw the end of the agent's output.
// It is true before the loop's Ended runs.
func (h *Host) OutputEnded() bool {
	select {
	case <-h.outputEnded:
		return true
	default:
		return false
	}
}

// LoopDone is closed once the loop ended, or never runs.
func (h *Host) LoopDone() <-chan struct{} {
	return h.loopDone
}

// Exited is closed once the process exited (see Host), or without a
// process once Close was called or the start failed.
func (h *Host) Exited() <-chan struct{} {
	return h.exited
}

// Wait waits until the process exited (see Host) and returns its exit
// error, with the end of its stderr. Without a process it returns nil once
// Close was called or the start failed, and native.ErrNotStarted before.
func (h *Host) Wait() error {
	h.mu.Lock()
	idle := !h.launched && !h.closing
	h.mu.Unlock()
	if idle {
		return native.ErrNotStarted
	}
	<-h.exited
	return h.exitErr
}

// StartError stops the process of a start that failed with err and says
// why. Once the agent's output ended, its exit error, or else its stderr,
// says more than err: an unknown session to resume, a bad option.
func (h *Host) StartError(err error) error {
	ended := h.OutputEnded()
	h.Close(nil)
	if !ended {
		return err
	}
	exitErr := h.Wait()
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

// Close stops the process and waits until it exited (see Host). First,
// interrupt, if set, interrupts a turn that runs and returns a channel its
// end closes, or nil without a turn; ctx ends once Close stops waiting.
// Close waits for the turn to end, so the agent's session log doesn't end
// in a tool call without result, for the exit, or InterruptGrace, in real
// time: the clock could be one nobody advances once the adapter is
// retired. Then procutil.Stop closes stdin and escalates to SIGTERM and
// SIGKILL on the clock. Without a process, Close returns at once, and the
// loop never runs.
func (h *Host) Close(interrupt func(ctx context.Context) <-chan struct{}) {
	h.mu.Lock()
	h.closing = true
	proc := h.proc
	h.mu.Unlock()
	if proc == nil {
		// A Launch under way stops its process.
		h.endLoop()
		h.finish(nil)
		return
	}
	h.stopOnce.Do(func() {
		if interrupt != nil {
			h.interrupt(interrupt)
		}
		procutil.Stop(proc, h.procExited, procutil.StopOptions{
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
	case <-h.procExited:
	case <-timer.C:
		h.Logger.Warn(h.Name + " didn't end its turn in time; closing it anyway")
	}
}
