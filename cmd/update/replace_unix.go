//go:build unix

package update

import "os"

func replaceBinary(newPath, currentPath string) error {
	return os.Rename(newPath, currentPath)
}
