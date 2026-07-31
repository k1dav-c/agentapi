//go:build windows

package update

import (
	"os"

	"golang.org/x/xerrors"
)

func replaceBinary(newPath, currentPath string) error {
	oldPath := currentPath + ".old"
	// Remove any leftover .old from a previous update.
	_ = os.Remove(oldPath)

	// Rename the current binary out of the way.
	if err := os.Rename(currentPath, oldPath); err != nil {
		return xerrors.Errorf("rename current binary: %w", err)
	}

	// Move the new binary into place.
	if err := os.Rename(newPath, currentPath); err != nil {
		// Try to restore the old binary.
		_ = os.Rename(oldPath, currentPath)
		return xerrors.Errorf("rename new binary: %w", err)
	}

	// Best-effort cleanup (may fail if the old binary is still locked).
	_ = os.Remove(oldPath)
	return nil
}
