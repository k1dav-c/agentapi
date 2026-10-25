//go:build windows

package registry

// alive reports whether the process pid runs. Windows can't tell without
// opening the process, so every entry is kept.
func alive(int) bool { return true }
