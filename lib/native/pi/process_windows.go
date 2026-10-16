//go:build windows

package pi

import (
	"os"
	"os/exec"
)

func setProcessGroup(*exec.Cmd) {}

// terminateGroup kills the process: Windows has no SIGTERM.
func terminateGroup(p *os.Process) error { return p.Kill() }

func killGroup(p *os.Process) error { return p.Kill() }
