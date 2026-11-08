//go:build !windows

package host

import (
	"errors"
	"os/exec"
	"syscall"
)

// Supported is whether the launcher runs on this platform.
const Supported = true

// startDetached starts cmd in a session of its own, so the job's AgentAPI
// outlives the launcher (a restart, an update) and a signal to the
// launcher's process group doesn't reach it.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// alive reports whether pid is a process that runs marker (the job's
// instance ID, on its command line) where the platform tells.
func alive(pid int, marker string) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return runs(pid, marker)
}

// terminate asks pid to stop: AgentAPI saves its state and stops the agent.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// kill stops pid and its session's process group at once.
func kill(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
