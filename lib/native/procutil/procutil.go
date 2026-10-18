// Package procutil starts and stops the agent processes the native adapters
// drive. Each process runs in its own process group, so stopping it also
// stops what it started (MCP servers, the commands its tools run); the
// adapter owns the pipes; stderr is logged and its end kept for error
// messages; and Stop shuts the process down politely before it kills it.
package procutil

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// OutputGrace is how long the output of a process that exited may stay
// open. Processes it started can inherit its stdout and stderr and outlive
// it; their pipes are closed after this long without output, so readers
// see EOF instead of waiting for them.
const OutputGrace = 2 * time.Second

// StderrTailBytes is how much of stderr a process keeps for error messages.
const StderrTailBytes = 4 << 10

// Command is a program to start.
type Command struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	// Logger receives stderr at debug level; nil discards it.
	Logger *slog.Logger
}

// Process is a running agent process as an adapter sees it. Tests replace
// it with a scripted one.
type Process interface {
	// Stdin carries the adapter's messages to the agent. Closing it makes a
	// write that is blocked (an agent that stopped reading) fail.
	Stdin() io.WriteCloser
	// Stdout carries the agent's messages. It reaches EOF once the process
	// exited and everything it wrote was read.
	Stdout() io.Reader
	// Wait blocks until the process exits and returns its exit error. It
	// may be called more than once.
	Wait() error
	// Terminate asks the process and the processes it started to exit.
	Terminate() error
	// Kill stops the process and the processes it started at once.
	Kill() error
	// StderrTail returns the end of what the process wrote to stderr. It
	// explains most failed starts, including clean exits.
	StderrTail() string
}

// StartFunc starts a process. Adapters take one so that tests can replace
// Start.
type StartFunc func(Command) (Process, error)

var _ StartFunc = Start

// Start runs c in its own process group with pipes the caller owns:
// exec.Cmd's own pipes are closed by Wait, which can drop the last lines of
// output before they are read.
func Start(c Command) (Process, error) {
	cmd := exec.Command(c.Program, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	setProcessGroup(cmd)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW)
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW)
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	if err := cmd.Start(); err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW)
		return nil, fmt.Errorf("start %s: %w", c.Program, err)
	}
	// The child has its own copies now.
	closeAll(stdinR, stdoutW, stderrW)
	return newProcess(cmd, c, stdinW, stdoutR, stderrR), nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// WithStderr adds the end of stderr to an exit error.
func WithStderr(err error, tail string) error {
	if err == nil || tail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, tail)
}

// Output runs c to completion and returns the first max bytes of its
// stdout. When ctx ends, c and the processes it started are killed.
func Output(ctx context.Context, c Command, max int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Program, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return signalGroup(cmd.Process, true) }
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: max}
	err := cmd.Run()
	return out.Bytes(), err
}

// limitedWriter keeps the first n bytes and drops the rest.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	keep := min(len(p), l.n)
	if keep > 0 {
		_, _ = l.w.Write(p[:keep])
		l.n -= keep
	}
	return len(p), nil
}

// process is a Process started with os/exec.
type process struct {
	cmd     *exec.Cmd
	program string
	logger  *slog.Logger
	stdin   *os.File
	stdout  *outputReader
	stderr  *os.File

	mu   sync.Mutex
	tail []byte

	stderrDone chan struct{}
	exited     chan struct{}
	err        error
}

func newProcess(cmd *exec.Cmd, c Command, stdin, stdout, stderr *os.File) *process {
	logger := c.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	p := &process{
		cmd:        cmd,
		program:    c.Program,
		logger:     logger,
		stdin:      stdin,
		stdout:     &outputReader{f: stdout},
		stderr:     stderr,
		stderrDone: make(chan struct{}),
		exited:     make(chan struct{}),
	}
	go p.readStderr()
	go p.wait()
	return p
}

func (p *process) Stdin() io.WriteCloser { return p.stdin }
func (p *process) Stdout() io.Reader     { return p.stdout }

func (p *process) Wait() error {
	<-p.exited
	return p.err
}

func (p *process) Terminate() error { return p.signal(false) }
func (p *process) Kill() error      { return p.signal(true) }

func (p *process) signal(kill bool) error {
	err := signalGroup(p.cmd.Process, kill)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *process) StderrTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimSpace(string(p.tail))
}

// wait reaps the process. Wait returns once stderr is read too, so the
// tail explains the exit; a process the agent started may hold stderr
// open, so that wait is bounded.
func (p *process) wait() {
	err := p.cmd.Wait()
	p.stdout.processExited()
	select {
	case <-p.stderrDone:
	case <-time.After(OutputGrace):
		_ = p.stderr.Close()
		<-p.stderrDone
	}
	p.err = err
	close(p.exited)
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// readStderr logs the agent's diagnostics and keeps their end. They can
// contain prompt text, so they are only logged at debug level.
func (p *process) readStderr() {
	defer close(p.stderrDone)
	defer func() { _ = p.stderr.Close() }()
	reader := bufio.NewReader(p.stderr)
	for {
		line, err := reader.ReadString('\n')
		if line = strings.TrimSpace(ansiEscape.ReplaceAllString(line, "")); line != "" {
			p.logger.Debug("Agent stderr", "program", p.program, "line", line)
			p.mu.Lock()
			p.tail = append(p.tail, line...)
			p.tail = append(p.tail, '\n')
			if len(p.tail) > StderrTailBytes {
				p.tail = p.tail[len(p.tail)-StderrTailBytes:]
			}
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// outputReader is the read end of the stdout pipe. Once the process
// exited, a Read that gets no output for OutputGrace means a process the
// agent started holds the pipe open; the pipe is then closed and the
// reader sees EOF. Output already written is always read first, however
// long the reader takes to come back for it.
type outputReader struct {
	f *os.File

	mu      sync.Mutex
	reading bool
	exited  bool
	timer   *time.Timer
}

func (r *outputReader) Read(b []byte) (int, error) {
	r.mu.Lock()
	r.reading = true
	if r.exited {
		r.armLocked()
	}
	r.mu.Unlock()
	n, err := r.f.Read(b)
	r.mu.Lock()
	r.reading = false
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
	if errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

func (r *outputReader) processExited() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exited = true
	if r.reading {
		r.armLocked()
	}
}

func (r *outputReader) armLocked() {
	if r.timer == nil {
		r.timer = time.AfterFunc(OutputGrace, func() { _ = r.f.Close() })
		return
	}
	r.timer.Reset(OutputGrace)
}
