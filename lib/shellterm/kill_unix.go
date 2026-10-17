//go:build unix

package shellterm

import (
	"os"
	"syscall"
)

// killGroup kills the shell's process group: the shell leads its own
// session, so its jobs go too.
func killGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}
