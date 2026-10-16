//go:build windows

package claude

import "os/exec"

func setProcessGroup(*exec.Cmd) {}

// signalGroup stops the process; Windows has no SIGTERM, so both are a kill.
func signalGroup(cmd *exec.Cmd, _ bool) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
