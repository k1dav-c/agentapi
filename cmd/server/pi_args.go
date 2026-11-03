package server

import (
	"path/filepath"
	"strings"
)

// isPiCLI reports whether program is the Pi coding agent CLI itself rather
// than a wrapper started with --type pi.
func isPiCLI(program string) bool {
	return filepath.Base(program) == "pi"
}

// piSessionDir returns the --session-dir Pi was started with, where its
// session logs are.
func piSessionDir(args []string) string {
	return piFlagValue(args, "--session-dir")
}

// piFlagValue returns the value of a "--flag value" or "--flag=value"
// option.
func piFlagValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			return value
		}
	}
	return ""
}
