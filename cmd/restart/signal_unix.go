//go:build unix

package restart

import "syscall"

// signalRestart has the agentapi server pid restart in place.
func signalRestart(pid int) error {
	return syscall.Kill(pid, syscall.SIGUSR2)
}
