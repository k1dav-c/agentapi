//go:build unix

package selfupdate

import "os"

func replaceBinary(newPath, currentPath string) error {
	return os.Rename(newPath, currentPath)
}
