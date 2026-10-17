//go:build unix

package shellterm

import (
	"os"
	"syscall"
)

// killGroup kills the shell's process group: the shell leads its own
// session, so its foreground jobs go too.
func killGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}

// nonblocking returns the terminal's master in non-blocking mode, where
// Close stops a Read: pty's ioctls leave it blocking, and a Read that
// blocks would keep Close, and the end of the shell, waiting while a
// background job holds the terminal open.
func nonblocking(ptmx *os.File) *os.File {
	fd, err := syscall.Dup(int(ptmx.Fd()))
	if err != nil {
		return ptmx
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return ptmx
	}
	name := ptmx.Name()
	_ = ptmx.Close()
	return os.NewFile(uintptr(fd), name)
}
