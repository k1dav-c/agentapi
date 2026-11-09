// Package shellterm runs a shell in a pseudo-terminal that several clients
// can watch and type into at once, like a shared tmux window. The native
// transport offers it to the chat: its agent has no terminal of its own,
// and a login or a quick command needs one.
package shellterm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// ErrClosed is returned once the terminal was closed.
var ErrClosed = errors.New("the terminal is closed")

const (
	// defaultScrollback is how much recent output a new watcher gets.
	defaultScrollback = 256 << 10
	// watcherBuffer is how many chunks of output a watcher may fall behind
	// before it is dropped; it gets the scrollback again when it comes back.
	watcherBuffer = 512
	// closeGrace is how long Close waits for the shell to exit after its
	// terminal hangs up, before it kills the shell's process group.
	closeGrace = 2 * time.Second
	// writeTimeout is how long Write waits for the shell's program to take
	// its input.
	writeTimeout = 10 * time.Second
	// Default size until a client says otherwise.
	defaultCols, defaultRows = 100, 30
)

// Config configures a Terminal.
type Config struct {
	// Shell is the program; empty means $SHELL, or else /bin/bash, or else
	// /bin/sh. It runs as a login shell.
	Shell string
	// Dir is the working directory.
	Dir string
	// Env is the shell's environment; nil means os.Environ(). TERM is set.
	Env    []string
	Logger *slog.Logger
	// Scrollback is how much recent output a new watcher gets; zero means
	// 256 KiB.
	Scrollback int
}

// Terminal is a shell in a pseudo-terminal. It starts when someone first
// watches it, keeps running while nobody does, and starts again once it
// exited and someone watches it again.
type Terminal struct {
	cfg Config

	mu         sync.Mutex
	cmd        *exec.Cmd
	ptmx       *os.File
	done       chan struct{}
	cols, rows uint16
	history    *scrollback
	watchers   map[int]chan []byte
	nextID     int
	closed     bool

	// writing holds the turn to write, so writes don't interleave; a
	// write waiting for it gives up when its context ends.
	writing      chan struct{}
	writeTimeout time.Duration
}

// New returns a terminal; nothing runs until Watch.
func New(cfg Config) *Terminal {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Scrollback <= 0 {
		cfg.Scrollback = defaultScrollback
	}
	return &Terminal{
		cfg: cfg, cols: defaultCols, rows: defaultRows,
		history:  newScrollback(cfg.Scrollback),
		watchers: make(map[int]chan []byte),
		writing:  make(chan struct{}, 1), writeTimeout: writeTimeout,
	}
}

// shell returns the program to run.
func (t *Terminal) shell() string {
	if t.cfg.Shell != "" {
		return t.cfg.Shell
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	for _, shell := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "sh"
}

// Watch starts the shell if it doesn't run, and returns the recent output
// and the output from now on. The channel is closed when the shell exits,
// the terminal closes, the watcher falls too far behind or stop is called.
func (t *Terminal) Watch() (history []byte, output <-chan []byte, stop func(), err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, nil, nil, ErrClosed
	}
	if t.cmd == nil {
		if err := t.startLocked(); err != nil {
			return nil, nil, nil, err
		}
	}
	id := t.nextID
	t.nextID++
	ch := make(chan []byte, watcherBuffer)
	t.watchers[id] = ch
	stop = func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if c, ok := t.watchers[id]; ok {
			delete(t.watchers, id)
			close(c)
		}
	}
	return t.history.bytes(), ch, stop, nil
}

func (t *Terminal) startLocked() error {
	shell := t.shell()
	cmd := exec.Command(shell, "-l")
	cmd.Dir = t.cfg.Dir
	env := t.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(append([]string(nil), env...), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: t.cols, Rows: t.rows})
	if err != nil {
		return fmt.Errorf("start %s: %w", shell, err)
	}
	ptmx = nonblocking(ptmx)
	t.cfg.Logger.Info("Started the terminal's shell", "shell", shell, "pid", cmd.Process.Pid, "dir", t.cfg.Dir)
	t.cmd, t.ptmx, t.done = cmd, ptmx, make(chan struct{})
	go t.read(cmd, ptmx, t.done)
	return nil
}

// read passes the shell's output on until it exits.
func (t *Terminal) read(cmd *exec.Cmd, ptmx *os.File, done chan struct{}) {
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		// A background job can hold the terminal open after the shell
		// exited; the output ends with the shell.
		_ = ptmx.Close()
	}()
	buf := make([]byte, 32<<10)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			t.broadcast(append([]byte(nil), buf[:n]...))
		}
		if err != nil {
			// EIO once the shell and its children closed the terminal.
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				t.cfg.Logger.Debug("The terminal's output ended", "error", err)
			}
			break
		}
	}
	err := <-exited
	status := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status = exitErr.ExitCode()
	}
	t.cfg.Logger.Info("The terminal's shell exited", "status", status)

	t.mu.Lock()
	defer t.mu.Unlock()
	notice := []byte(fmt.Sprintf("\r\n[The shell exited with status %d. Open the terminal again for a new one.]\r\n", status))
	t.appendLocked(notice)
	for id, ch := range t.watchers {
		select {
		case ch <- notice:
		default:
		}
		delete(t.watchers, id)
		close(ch)
	}
	if t.cmd == cmd {
		t.cmd, t.ptmx = nil, nil
	}
	close(done)
}

// broadcast records output and sends it to the watchers. A watcher that
// fell too far behind is dropped rather than shown a gap.
func (t *Terminal) broadcast(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendLocked(data)
	for id, ch := range t.watchers {
		select {
		case ch <- data:
		default:
			delete(t.watchers, id)
			close(ch)
		}
	}
}

func (t *Terminal) appendLocked(data []byte) {
	t.history.add(data)
}

// Write types data into the shell. One write goes in at a time, so two
// pastes don't interleave. A program that doesn't read its input fills the
// terminal's buffer; Write then gives up after writeTimeout, or when ctx
// ends, with part of data perhaps typed, and returns ErrInputStalled or
// ctx's error.
func (t *Terminal) Write(ctx context.Context, data []byte) error {
	select {
	case t.writing <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	t.mu.Lock()
	ptmx := t.ptmx
	t.mu.Unlock()
	if ptmx == nil {
		<-t.writing
		return ErrNotRunning
	}
	if err := ptmx.SetWriteDeadline(time.Now().Add(t.writeTimeout)); err != nil {
		// A terminal without deadlines: the write goes on in the
		// background, holding the turn, and Write only stops waiting.
		written := make(chan error, 1)
		go func() {
			defer func() { <-t.writing }()
			_, err := ptmx.Write(data)
			written <- err
		}()
		select {
		case err := <-written:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.writeTimeout):
			return ErrInputStalled
		}
	}
	defer func() { <-t.writing }()
	// A deadline in the past stops the write when ctx ends.
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = ptmx.SetWriteDeadline(time.Unix(1, 0))
		close(fired)
	})
	_, err := ptmx.Write(data)
	if !stop() {
		<-fired
		if err != nil {
			return ctx.Err()
		}
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return ErrInputStalled
	}
	return err
}

// ErrInputStalled is returned by Write when the shell's program didn't take
// the input in time.
var ErrInputStalled = errors.New("the terminal's program isn't reading its input")

// ErrNotRunning is returned by Write while no shell runs.
var ErrNotRunning = errors.New("the terminal's shell is not running")

// Running reports whether the shell runs. A watch whose output ended while
// it does fell too far behind, and can watch again.
func (t *Terminal) Running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cmd != nil && !t.closed
}

// Resize sets the terminal's size, for the shell that runs and the next.
func (t *Terminal) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cols, t.rows = cols, rows
	if t.ptmx == nil {
		return nil
	}
	return pty.Setsize(t.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close stops the shell: its terminal hangs up, and its process group is
// killed if it doesn't exit in time.
func (t *Terminal) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cmd, ptmx, done := t.cmd, t.ptmx, t.done
	t.mu.Unlock()
	if cmd == nil {
		return nil
	}
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(closeGrace):
		killGroup(cmd.Process)
		<-done
	}
	return nil
}
