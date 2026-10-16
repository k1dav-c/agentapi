package codex

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Process is a running app-server as the adapter sees it. Tests replace it
// with pipes.
type Process interface {
	// Stdin receives the client's messages.
	Stdin() io.WriteCloser
	// Stdout delivers app-server's messages; it ends when the process
	// exits.
	Stdout() io.Reader
	// Wait blocks until the process exits. The error describes a failed
	// exit and includes the end of stderr.
	Wait() error
	// Terminate asks the process to exit (SIGTERM).
	Terminate() error
	// Kill stops the process at once.
	Kill() error
}

// LaunchSpec is the command line the adapter starts.
type LaunchSpec struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
	Logger  *slog.Logger
}

// Launcher starts app-server.
type Launcher func(spec LaunchSpec) (Process, error)

// stderrTailBytes is how much of stderr a failed exit reports.
const stderrTailBytes = 4 << 10

// launchProcess starts app-server as a child process. Its lifetime is not
// tied to any context; Close stops it.
func launchProcess(spec LaunchSpec) (Process, error) {
	cmd := exec.Command(spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// A pipe of our own instead of StdoutPipe: Wait must not close it under
	// the reader, which reads to EOF.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = stdoutW
	stderr := &stderrSink{logger: spec.Logger}
	cmd.Stderr = stderr
	// Commands Codex runs may keep stderr open after app-server exits.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, fmt.Errorf("start %s: %w", spec.Program, err)
	}
	_ = stdoutW.Close()
	p := &execProcess{cmd: cmd, stdin: stdin, stdout: stdoutR, stderr: stderr, done: make(chan struct{})}
	go p.wait()
	return p, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	stderr *stderrSink
	done   chan struct{}
	err    error
}

func (p *execProcess) wait() {
	err := p.cmd.Wait()
	if err != nil {
		if tail := p.stderr.tail(); tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
	}
	p.err = err
	close(p.done)
	// Commands Codex started may still hold stdout open; don't let them
	// keep the reader waiting.
	time.AfterFunc(2*time.Second, func() { _ = p.stdout.Close() })
}

// StderrTail returns the end of stderr, which explains an exit at startup
// even when the exit status is 0.
func (p *execProcess) StderrTail() string { return p.stderr.tail() }

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.Reader     { return p.stdout }
func (p *execProcess) Wait() error           { <-p.done; return p.err }

func (p *execProcess) Terminate() error {
	err := p.cmd.Process.Signal(syscall.SIGTERM)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		// Windows can't deliver SIGTERM.
		return p.Kill()
	}
	return nil
}

func (p *execProcess) Kill() error {
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// stderrSink logs app-server's stderr (tracing output) line by line at
// debug level and keeps its end for exit errors.
type stderrSink struct {
	logger *slog.Logger
	mu     sync.Mutex
	buf    []byte // last stderrTailBytes bytes
	line   []byte // incomplete line
}

func (s *stderrSink) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, b...)
	if len(s.buf) > stderrTailBytes {
		s.buf = s.buf[len(s.buf)-stderrTailBytes:]
	}
	s.line = append(s.line, b...)
	for {
		i := bytes.IndexByte(s.line, '\n')
		if i < 0 {
			break
		}
		if s.logger != nil {
			if text := strings.TrimSpace(ansiEscape.ReplaceAllString(string(s.line[:i]), "")); text != "" {
				s.logger.Debug("codex app-server", "stderr", text)
			}
		}
		s.line = s.line[i+1:]
	}
	if len(s.line) > stderrTailBytes {
		s.line = s.line[len(s.line)-stderrTailBytes:]
	}
	return len(b), nil
}

func (s *stderrSink) tail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(ansiEscape.ReplaceAllString(string(s.buf), ""))
}
