//go:build unix

package procutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func termSignal(kill bool) syscall.Signal {
	if kill {
		return syscall.SIGKILL
	}
	return syscall.SIGTERM
}

// signalGroup sends SIGTERM (SIGKILL if kill) to the process group p leads,
// or to p alone if it doesn't lead one. It returns os.ErrProcessDone once p
// was waited for: signalExitedGroup takes over then.
func signalGroup(p *os.Process, kill bool) error {
	sig := termSignal(kill)
	// Signal 0 checks that p wasn't reaped, so its pid can't belong to
	// another process group by now.
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return err
	}
	if err := syscall.Kill(-p.Pid, sig); err != nil {
		return p.Signal(sig)
	}
	return nil
}

// signalExitedGroup sends SIGTERM (SIGKILL if kill) to the process group
// pgid, whose leader exited: the processes it started (MCP servers, the
// commands its tools ran) still belong to it. A group that is gone
// (ESRCH) is no error. The pgid can't name another group while one of
// them runs; once they are all gone it can, so callers stop signaling it
// then (groupAlive).
func signalExitedGroup(pgid int, kill bool) error {
	if pgid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pgid, termSignal(kill)); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// groupAlive reports whether a process of the group pgid still runs.
func groupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
