//go:build unix

package procutil

import (
	"os"
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup sends SIGTERM (SIGKILL if kill) to the process group p leads,
// or to p alone if it doesn't lead one. It returns os.ErrProcessDone once p
// was waited for.
func signalGroup(p *os.Process, kill bool) error {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
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
