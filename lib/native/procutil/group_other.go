//go:build !unix

package procutil

import (
	"os"
	"os/exec"
)

func setProcessGroup(*exec.Cmd) {}

// signalGroup kills p: there is no SIGTERM, and no process group to signal.
func signalGroup(p *os.Process, _ bool) error { return p.Kill() }
