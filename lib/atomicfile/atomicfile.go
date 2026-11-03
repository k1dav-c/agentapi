// Package atomicfile replaces a file at once, in a way that survives a
// crash: the file is the old data or the new, never part of either nor
// empty.
package atomicfile

import (
	"os"
	"path/filepath"
)

// syncFile and syncDir put a file, and a directory's entries, on disk;
// tests replace them.
var (
	syncFile = (*os.File).Sync
	syncDir  = func(dir string) {
		// Where the system can sync a directory.
		d, err := os.Open(dir)
		if err != nil {
			return
		}
		_ = d.Sync()
		_ = d.Close()
	}
)

// Write writes data to path, readable by its owner only (0600), through a
// temporary file in path's directory named after pattern (as
// os.CreateTemp names it), which is synced before it is renamed into
// place; the directory is synced after. The directory must exist.
func Write(path, pattern string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// On disk before it takes the file's name: after a crash, the name
	// would otherwise point at an empty file.
	if err := syncFile(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}
