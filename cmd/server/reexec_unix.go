//go:build unix

package server

import "syscall"

// execProgram replaces the process's program with path. Standard input,
// output and error stay; Go opens every other file with close-on-exec.
func execProgram(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
