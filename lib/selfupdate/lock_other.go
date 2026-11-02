//go:build !unix || aix

package selfupdate

import "context"

// lockFile locks nothing where flock is missing: on Windows AgentAPI
// doesn't restart in place, so only agentapi update replaces the program,
// and there is no release for AIX.
func lockFile(context.Context, string) (func(), error) {
	return func() {}, nil
}
