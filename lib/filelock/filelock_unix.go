//go:build unix && !aix

// Package filelock takes an exclusive lock on a file, which AgentAPI
// processes sharing a machine take in turn: updating the program, an agent
// CLI updating itself.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// poll is how often a lock another process holds is tried again.
const poll = 100 * time.Millisecond

// Lock takes an exclusive flock of path, which it creates, trying again
// while another process holds it until ctx ends. The file stays.
func Lock(ctx context.Context, path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open the lock file: %w", err)
	}
	fd := int(f.Fd())
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(fd, unix.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}
