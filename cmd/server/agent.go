package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/filelock"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/procenv"
)

// runningAgent is the native agent as runServer prepares it: the server
// starts the agent from factory, and starts it again when it exits.
type runningAgent struct {
	factory native.Factory
	// updateNative updates the agent's CLI before a restart.
	updateNative func(context.Context)
}

// startAgent prepares the agent's adapter factory for cwd. Nothing is
// prepared for --print-openapi.
func startAgent(opts serverOptions, cwd string, logger *slog.Logger) (*runningAgent, error) {
	if opts.printOpenAPI {
		return &runningAgent{}, nil
	}
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

// agentUpdateTimeout bounds how long a restart waits for the agent CLI to
// update itself before starting the installed version anyway.
const agentUpdateTimeout = 3 * time.Minute

// agentUpdater returns a function that updates the agent CLI, or nil when
// AgentAPI doesn't update this agent. Codex and Pi only update when asked
// (they just announce a new version at startup), so a restart runs `codex
// update` or `pi update --self` to start the latest release. Claude Code
// updates itself in the background, so restarting already starts its
// latest version.
func agentUpdater(agentType instanceapi.AgentType, program string, logger *slog.Logger) func(context.Context) {
	var args, env []string
	switch {
	case agentType == instanceapi.AgentTypeCodex && isCodexCLI(program):
		args, env = []string{"update"}, []string{"CODEX_NON_INTERACTIVE=1"}
	case agentType == instanceapi.AgentTypePi && isPiCLI(program):
		args = []string{"update", "--self"}
	default:
		return nil
	}
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, agentUpdateTimeout)
		defer cancel()
		start := time.Now()
		// The AgentAPIs that run this CLI update it one at a time: two
		// npm installs of one package at once can leave it broken.
		unlock, err := lockAgentUpdate(ctx, program)
		if err != nil {
			logger.Warn("Failed to take the agent CLI's update lock; starting the installed version",
				"program", program, "error", err)
			return
		}
		defer unlock()
		// In its own process group, which is killed when ctx ends; npm's
		// children can hold the output open after it, which isn't waited
		// for long.
		output, err := procutil.CombinedOutput(ctx, procutil.Command{
			Program: program,
			Args:    args,
			Env:     procenv.ForAgent(nil, env...),
		}, agentUpdateOutputBytes)
		if err != nil {
			logger.Warn("Failed to update the agent CLI before restarting; starting the installed version",
				"program", program, "error", err, "output", tailLines(string(output), 10))
			return
		}
		logger.Info("Updated the agent CLI before restarting", "program", program,
			"duration", time.Since(start).Round(time.Millisecond), "output", tailLines(string(output), 3))
	}
}

// agentUpdateOutputBytes is how much of the end of an agent CLI update's
// output is kept for the log.
const agentUpdateOutputBytes = 16 << 10

// lockAgentUpdate takes the lock that the AgentAPIs updating program take
// in turn, in the user's cache directory (~/.cache/agentapi on Linux),
// waiting until ctx ends.
func lockAgentUpdate(ctx context.Context, program string) (func(), error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "agentapi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(ctx, filepath.Join(dir, filepath.Base(program)+"-update.lock"))
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
