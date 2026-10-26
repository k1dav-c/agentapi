//go:build unix

package registry

import (
	"errors"
	"os"
	"syscall"
)

// alive reports whether the process pid runs.
func alive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
