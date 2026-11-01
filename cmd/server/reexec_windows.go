//go:build windows

package server

import "errors"

// execProgram can't replace a process's program on Windows.
func execProgram(string, []string, []string) error {
	return errors.New("restarting in place is not supported on Windows")
}
