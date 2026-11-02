//go:build unix

package selfupdate

import "golang.org/x/sys/unix"

// writable reports whether this user can create files in dir, as the
// replacement of a program there does.
func writable(dir string) error {
	return unix.Access(dir, unix.W_OK)
}
