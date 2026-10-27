package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/agentlog/pilog"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/procenv"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/termexec"
)

// runningAgent is the agent of one transport, as runServer starts it,
// waits for it and stops it.
type runningAgent struct {
	// io is the conversation's view of a PTY agent; nil for a native
	// agent, which the server starts itself from factory.
	io      st.AgentIO
	factory native.Factory
	// updateNative updates a native agent's CLI before a restart.
	updateNative func(context.Context)
	// process and supervisor run a PTY agent, which a restart replaces.
	process    *termexec.Process
	supervisor *agentSupervisor
}

// startAgent starts the agent of opts.transport in cwd. Nothing runs for
// --print-openapi.
func startAgent(ctx context.Context, opts serverOptions, cwd string, logger *slog.Logger) (*runningAgent, error) {
	switch {
	case opts.printOpenAPI:
		return &runningAgent{}, nil
	case opts.transport == httpapi.TransportNative:
		return startNativeAgent(opts, cwd, logger)
	default:
		return startPTYAgent(ctx, opts, logger)
	}
}

// startNativeAgent prepares the adapter factory; the server starts the
// agent.
func startNativeAgent(opts serverOptions, cwd string, logger *slog.Logger) (*runningAgent, error) {
	factory, err := nativeFactory(opts.agentType, nativeAdapterConfig{
		Program: opts.agent,
		Args:    opts.agentArgs,
		CWD:     cwd,
		Logger:  logger,
	})
	if err != nil {
		return nil, err
	}
	return &runningAgent{factory: factory, updateNative: agentUpdater(opts.agentType, opts.agent, logger)}, nil
}

// startPTYAgent runs the agent's terminal UI in a terminal emulator. The
// agent runs as long as AgentAPI: its process gets runServer's ctx, also
// when a restart starts it.
func startPTYAgent(ctx context.Context, opts serverOptions, logger *slog.Logger) (*runningAgent, error) {
	programArgs := opts.agentArgs
	if opts.agentType == AgentTypeCodex && isCodexCLI(opts.agent) {
		programArgs = withCodexReasoningSummary(programArgs, codexConfigPath())
	}
	setup := func(context.Context) (*termexec.Process, error) {
		args := programArgs
		if opts.agentType == AgentTypePi && isPiCLI(opts.agent) {
			args = withPiSessionID(args)
		}
		proc, err := httpapi.SetupProcess(ctx, httpapi.SetupProcessConfig{
			Program:        opts.agent,
			ProgramArgs:    args,
			TerminalWidth:  opts.termWidth,
			TerminalHeight: opts.termHeight,
			AgentType:      opts.agentType,
		})
		if err == nil && opts.agentType == AgentTypePi {
			pilog.RegisterSession(proc.Pid(), piSessionOptions(args))
		}
		return proc, err
	}
	proc, err := setup(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to setup process: %w", err)
	}
	swappable := termexec.NewSwappableProcess(proc)
	return &runningAgent{
		io:      swappable,
		process: proc,
		supervisor: &agentSupervisor{
			logger: logger,
			swap:   swappable,
			setup:  setup,
			update: agentUpdater(opts.agentType, opts.agent, logger),
		},
	}, nil
}

// pid is the PTY agent's process ID, which finds its session log; 0
// for other transports.
func (a *runningAgent) pid() int {
	if a.process == nil {
		return 0
	}
	return a.process.Pid()
}

// restart replaces a PTY agent's process, or is nil for other transports.
func (a *runningAgent) restart() func(context.Context) (int, error) {
	if a.supervisor == nil {
		return nil
	}
	return a.supervisor.Restart
}

// waitForExit waits until the agent exits by itself, which the server
// reports, and returns why; or, for a native agent, until ctx ends, which
// returns false. A PTY agent's process that a restart replaced isn't an
// exit.
func (a *runningAgent) waitForExit(ctx context.Context, srv *httpapi.Server) (bool, error) {
	switch {
	case a.process != nil:
		current := a.process
		for {
			err := current.Wait()
			if settled := a.supervisor.currentSettled(); settled != nil && settled != current {
				current = settled
				continue
			}
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, termexec.ErrNonZeroExitCode):
				return true, fmt.Errorf("========\n%s\n========\n: %w", strings.TrimSpace(current.ReadScreen()), err)
			default:
				return true, fmt.Errorf("failed to wait for process: %w", err)
			}
		}
	default:
		exited := srv.NativeExited()
		if exited == nil {
			return false, nil
		}
		select {
		case err := <-exited:
			if err != nil {
				return true, fmt.Errorf("agent exited: %w", err)
			}
			return true, nil
		case <-ctx.Done():
			return false, nil
		}
	}
}

// close stops a PTY agent. The server stops a native agent.
func (a *runningAgent) close(logger *slog.Logger) {
	if a.process == nil {
		return
	}
	current := a.supervisor.currentSettled()
	if current == nil {
		current = a.process
	}
	if err := current.Close(logger, 5*time.Second); err != nil {
		logger.Error("Failed to close process cleanly", "error", err)
	}
}

type agentSupervisor struct {
	mu     sync.Mutex
	logger *slog.Logger
	swap   *termexec.SwappableProcess
	setup  func(context.Context) (*termexec.Process, error)
	// update, when set, runs between stopping the old process and starting
	// the new one so a restart picks up the latest agent CLI.
	update func(context.Context)
}

func (s *agentSupervisor) Restart(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.swap.Current()
	if old == nil {
		return 0, errors.New("agent process is not running")
	}
	s.logger.Info("Restarting agent process")
	if err := old.Close(s.logger, 10*time.Second); err != nil {
		s.logger.Warn("Error closing agent process during restart", "error", err)
	}
	if s.update != nil {
		s.update(ctx)
	}
	next, err := s.setup(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to start new agent process: %w", err)
	}
	s.swap.Set(next)
	s.logger.Info("Agent process restarted", "pid", next.Pid())
	return next.Pid(), nil
}

func (s *agentSupervisor) currentSettled() *termexec.Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.swap.Current()
}

// agentUpdateTimeout bounds how long a restart waits for the agent CLI to
// update itself before starting the installed version anyway.
const agentUpdateTimeout = 3 * time.Minute

// agentUpdater returns a function that updates the agent CLI, or nil when
// AgentAPI doesn't update this agent. Codex and Pi only update when asked
// (they just announce a new version at startup), so a restart runs `codex
// update` or `pi update --self` to start the latest release. Claude Code
// updates itself in the background, so restarting already starts its
// latest version.
func agentUpdater(agentType AgentType, program string, logger *slog.Logger) func(context.Context) {
	var args, env []string
	switch {
	case agentType == AgentTypeCodex && isCodexCLI(program):
		args, env = []string{"update"}, []string{"CODEX_NON_INTERACTIVE=1"}
	case agentType == AgentTypePi && isPiCLI(program):
		args = []string{"update", "--self"}
	default:
		return nil
	}
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, agentUpdateTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, program, args...)
		cmd.Env = procenv.ForAgent(nil, env...)
		start := time.Now()
		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.Warn("Failed to update the agent CLI before restarting; starting the installed version",
				"program", program, "error", err, "output", tailLines(string(output), 10))
			return
		}
		logger.Info("Updated the agent CLI before restarting", "program", program,
			"duration", time.Since(start).Round(time.Millisecond), "output", tailLines(string(output), 3))
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
