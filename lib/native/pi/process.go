package pi

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// process is the running Pi process.
type process interface {
	// Wait blocks until the process exits and returns its exit error.
	Wait() error
	// Terminate asks the process (group) to stop.
	Terminate() error
	// Kill stops the process (group) at once.
	Kill() error
	// StderrTail returns the last part of what the process wrote to stderr.
	StderrTail() string
}

// transport connects the adapter to Pi: commands go to stdin, events and
// responses come from stdout.
type transport struct {
	stdin  io.WriteCloser
	stdout io.Reader
	proc   process
}

// spawnFunc starts Pi. Tests replace it with a scripted fake.
type spawnFunc func(program string, args []string, cwd string, env []string, logger *slog.Logger) (*transport, error)

// stderrTailBytes is how much of stderr is kept for the exit error.
const stderrTailBytes = 4 << 10

// execProcess is a Pi process started with os/exec.
type execProcess struct {
	cmd *exec.Cmd

	mu   sync.Mutex
	tail []byte
	// stderrDone is closed once stderr is drained.
	stderrDone chan struct{}
	exited     chan struct{}
	waitErr    error
}

// spawnProcess starts program in its own process group with stdin and
// stdout as pipes. stderr is logged at debug level and its tail kept.
func spawnProcess(program string, args []string, cwd string, env []string, logger *slog.Logger) (*transport, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = cwd
	cmd.Env = env
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
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	if err := cmd.Start(); err != nil {
		closeAll(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW)
		return nil, fmt.Errorf("start %s: %w", program, err)
	}
	// The child holds its own copies.
	closeAll(stdinR, stdoutW, stderrW)

	p := &execProcess{cmd: cmd, stderrDone: make(chan struct{}), exited: make(chan struct{})}
	go p.readStderr(stderrR, logger)
	go func() {
		err := cmd.Wait()
		// A child Pi started can keep stderr open after Pi exits.
		select {
		case <-p.stderrDone:
		case <-time.After(2 * time.Second):
		}
		p.waitErr = err
		close(p.exited)
	}()
	return &transport{stdin: stdinW, stdout: stdoutR, proc: p}, nil
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// readStderr logs Pi's diagnostics. They can contain prompt text, so they
// are only logged at debug level.
func (p *execProcess) readStderr(r io.ReadCloser, logger *slog.Logger) {
	defer close(p.stderrDone)
	defer func() { _ = r.Close() }()
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			logger.Debug("pi stderr", "line", strings.TrimRight(line, "\r\n"))
			p.mu.Lock()
			p.tail = append(p.tail, line...)
			if len(p.tail) > stderrTailBytes {
				p.tail = append([]byte(nil), p.tail[len(p.tail)-stderrTailBytes:]...)
			}
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (p *execProcess) Wait() error {
	<-p.exited
	return p.waitErr
}

func (p *execProcess) running() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

func (p *execProcess) Terminate() error {
	if !p.running() {
		return nil
	}
	return terminateGroup(p.cmd.Process)
}

func (p *execProcess) Kill() error {
	if !p.running() {
		return nil
	}
	err := killGroup(p.cmd.Process)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *execProcess) StderrTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimSpace(string(p.tail))
}
