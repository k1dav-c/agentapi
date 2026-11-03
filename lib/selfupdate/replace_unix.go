//go:build unix

package selfupdate

import (
	"os"
	"path/filepath"
)

// replaceBinary renames newPath over currentPath, then syncs their
// directory, so that the rename survives a crash.
func replaceBinary(newPath, currentPath string) error {
	if err := os.Rename(newPath, currentPath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(currentPath))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
