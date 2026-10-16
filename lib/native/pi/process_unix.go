//go:build unix

package pi

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup starts Pi in its own process group, so stopping it also
// stops the commands its tools started.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateGroup(p *os.Process) error {
	if err := syscall.Kill(-p.Pid, syscall.SIGTERM); err != nil {
		return p.Signal(syscall.SIGTERM)
	}
	return nil
}

func killGroup(p *os.Process) error {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		return p.Kill()
	}
	return nil
}
