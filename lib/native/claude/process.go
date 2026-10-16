package claude

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Process is a running Claude Code process as the adapter sees it. Tests
// replace it with a scripted one.
type Process interface {
	// Stdin carries user messages and control frames to the agent.
	Stdin() io.WriteCloser
	// Stdout carries the agent's stream-json frames. It reaches EOF once
	// the process exited and its output was read.
	Stdout() io.Reader
	// Stderr carries diagnostics; may be nil.
	Stderr() io.Reader
	// Wait blocks until the process exits. It is called once.
	Wait() error
	// Terminate asks the process (and its children) to exit.
	Terminate() error
	// Kill stops the process (and its children) at once.
	Kill() error
}

// Command is what StartProcess runs.
type Command struct {
	Program string
	Args    []string
	Env     []string
	Dir     string
}

// StartProcess starts a process; Config.StartProcess overrides it.
type StartProcess func(Command) (Process, error)

// outputGrace is how long the output pipes stay open after the process
// exited, for children that inherited them.
const outputGrace = 2 * time.Second

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	stderr *os.File
}

// startExec runs the command in its own process group, so MCP servers and
// other children are stopped with it. Output goes through pipes the adapter
// owns: exec.Cmd's own pipes are closed by Wait, which can drop the last
// lines (the turn's result) before they are read.
func startExec(c Command) (Process, error) {
	cmd := exec.Command(c.Program, c.Args...)
	cmd.Env = c.Env
	cmd.Dir = c.Dir
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, err
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	err = cmd.Start()
	// The child has its own copies now.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if err != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		return nil, fmt.Errorf("start %s: %w", c.Program, err)
	}
	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdoutR, stderr: stderrR}, nil
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.Reader     { return p.stdout }
func (p *execProcess) Stderr() io.Reader     { return p.stderr }
func (p *execProcess) Terminate() error      { return signalGroup(p.cmd, false) }
func (p *execProcess) Kill() error           { return signalGroup(p.cmd, true) }

func (p *execProcess) Wait() error {
	err := p.cmd.Wait()
	// A child that inherited the output (and outlives the CLI) would keep
	// the read loop waiting for EOF.
	time.AfterFunc(outputGrace, func() {
		_ = p.stdout.Close()
		_ = p.stderr.Close()
	})
	return err
}
