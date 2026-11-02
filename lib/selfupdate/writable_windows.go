//go:build windows

package selfupdate

import "os"

// writable reports whether this user can create files in dir, as the
// replacement of a program there does: it creates one.
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".agentapi-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
