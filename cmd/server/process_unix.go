//go:build unix

package server

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunning checks if a process with the given PID is running.
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}

	// A PID can be reused after agentapi exits without cleaning up its PID
	// file. On Linux, make sure the live PID still is an agentapi: this
	// executable, or another one named agentapi (another install, a build
	// from source, the program before an update), whose PID file isn't
	// stale either. An unrelated process isn't taken for one.
	if runtime.GOOS == "linux" {
		currentExecutable, err := os.Executable()
		if err != nil {
			return true // Liveness detection remains best-effort.
		}
		pidExecutable, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
		if err != nil {
			return true // Preserve conservative behavior if procfs is unavailable.
		}
		pidExecutable = strings.TrimSuffix(pidExecutable, " (deleted)")
		if strings.HasPrefix(filepath.Base(pidExecutable), "agentapi") {
			return true
		}
		currentInfo, currentErr := os.Stat(currentExecutable)
		pidInfo, pidErr := os.Stat(pidExecutable)
		if currentErr == nil && pidErr == nil {
			return os.SameFile(currentInfo, pidInfo)
		}
		return currentExecutable == pidExecutable
	}

	return true
}
