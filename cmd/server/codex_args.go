package server

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// codexReasoningSummaryKey is the Codex setting that makes it log a readable
// summary of its reasoning. With it off (Codex's default for some models)
// the session log only holds encrypted reasoning, so the chat has no
// thinking to show.
const codexReasoningSummaryKey = "model_reasoning_summary"

var codexReasoningSummaryLine = regexp.MustCompile(`^\s*` + codexReasoningSummaryKey + `\s*=`)

// withCodexReasoningSummary turns on Codex reasoning summaries for the
// launched process unless the user already chose a value, on the command
// line or anywhere in their Codex config (including profiles).
func withCodexReasoningSummary(args []string, configPath string) []string {
	for _, arg := range args {
		if strings.Contains(arg, codexReasoningSummaryKey) {
			return args
		}
	}
	if configSetsCodexReasoningSummary(configPath) {
		return args
	}
	return append([]string{"-c", codexReasoningSummaryKey + `="auto"`}, args...)
}

func configSetsCodexReasoningSummary(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		if codexReasoningSummaryLine.MatchString(line) {
			return true
		}
	}
	return false
}

// isCodexCLI reports whether program is the Codex CLI itself. Codex-type
// agents can also be wrappers or test doubles, which must not get Codex
// flags or subcommands.
func isCodexCLI(program string) bool {
	name := filepath.Base(program)
	return name == "codex" || name == "codex.exe"
}

// codexConfigPath returns $CODEX_HOME/config.toml, defaulting to
// ~/.codex/config.toml.
func codexConfigPath() string {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".codex")
	}
	return filepath.Join(root, "config.toml")
}
