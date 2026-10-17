// Package shellterm runs a shell in a pseudo-terminal that several clients
// can watch and type into at once, like a shared tmux window. The native
// transport offers it to the chat: its agent has no terminal of its own,
// and a login or a quick command needs one.
package shellterm

import (
	"bytes"
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
	history    []byte
	watchers   map[int]chan []byte
	nextID     int
	closed     bool
}

// New returns a terminal; nothing runs until Watch.
func New(cfg Config) *Terminal {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Scrollback <= 0 {
		cfg.Scrollback = defaultScrollback
	}
	return &Terminal{cfg: cfg, cols: defaultCols, rows: defaultRows, watchers: make(map[int]chan []byte)}
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
	return append([]byte(nil), t.history...), ch, stop, nil
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
	t.cfg.Logger.Info("Started the terminal's shell", "shell", shell, "pid", cmd.Process.Pid, "dir", t.cfg.Dir)
	t.cmd, t.ptmx, t.done = cmd, ptmx, make(chan struct{})
	go t.read(cmd, ptmx, t.done)
	return nil
}

// read passes the shell's output on until it exits.
func (t *Terminal) read(cmd *exec.Cmd, ptmx *os.File, done chan struct{}) {
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
	err := cmd.Wait()
	_ = ptmx.Close()
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
	t.history = append(t.history, data...)
	if over := len(t.history) - t.cfg.Scrollback; over > 0 {
		kept := t.history[over:]
		// Start at a line, not inside an escape sequence.
		if i := bytes.IndexByte(kept, '\n'); i >= 0 {
			kept = kept[i+1:]
		}
		t.history = append([]byte(nil), kept...)
	}
}

// Write types data into the shell.
func (t *Terminal) Write(data []byte) error {
	t.mu.Lock()
	ptmx := t.ptmx
	t.mu.Unlock()
	if ptmx == nil {
		return ErrNotRunning
	}
	_, err := ptmx.Write(data)
	return err
}

// ErrNotRunning is returned by Write while no shell runs.
var ErrNotRunning = errors.New("the terminal's shell is not running")

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
