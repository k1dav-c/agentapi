package server

import "path/filepath"

// isCodexCLI reports whether program is the Codex CLI itself. Codex-type
// agents can also be wrappers or test doubles, which must not get Codex
// subcommands.
func isCodexCLI(program string) bool {
	name := filepath.Base(program)
	return name == "codex" || name == "codex.exe"
}
