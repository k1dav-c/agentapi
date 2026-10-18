// Package agenthome finds the directories the agents keep their state in,
// honoring the same environment variables the agents do.
package agenthome

import (
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeConfigDir is Claude Code's config directory, which holds its
// sessions and projects: $CLAUDE_CONFIG_DIR, or ~/.claude.
func ClaudeConfigDir() (string, error) {
	return dir("CLAUDE_CONFIG_DIR", ".claude")
}

// CodexHome is Codex's home, which holds its config and sessions:
// $CODEX_HOME, or ~/.codex.
func CodexHome() (string, error) {
	return dir("CODEX_HOME", ".codex")
}

// PiAgentDir is Pi's agent directory, which holds its sessions and MCP
// config: $PI_CODING_AGENT_DIR, or ~/.pi/agent.
func PiAgentDir() (string, error) {
	return dir("PI_CODING_AGENT_DIR", filepath.Join(".pi", "agent"))
}

// dir returns $env, or the directory under the user's home.
func dir(env, underHome string) (string, error) {
	if d := os.Getenv(env); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, underHome), nil
}
