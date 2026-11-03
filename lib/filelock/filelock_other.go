//go:build !unix || aix

// Package filelock takes an exclusive lock on a file, which AgentAPI
// processes sharing a machine take in turn: updating the program, an agent
// CLI updating itself.
package filelock

import "context"

// Lock locks nothing where flock is missing: on Windows AgentAPI doesn't
// restart in place, so only agentapi update replaces the program, and
// there is no release for AIX.
func Lock(context.Context, string) (func(), error) {
	return func() {}, nil
}
