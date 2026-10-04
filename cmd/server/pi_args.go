package server

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

// isPiCLI reports whether program is the Pi coding agent CLI itself rather
// than a wrapper started with --type pi.
func isPiCLI(program string) bool {
	return filepath.Base(program) == "pi"
}

// piSessionFlags are the Pi options that pick or disable the session, or
// run Pi without its interactive UI. --session-id can't be combined with
// them, and with them the user has chosen the session themselves.
var piSessionFlags = []string{
	"--session", "--session-id", "--continue", "-c", "--resume", "-r",
	"--no-session", "--fork", "--print", "-p", "--mode",
}

// withPiSessionID gives the launched Pi process its own session ID, so
// AgentAPI can find its session log by name. Pi only writes the log once
// the first message is sent, and several Pi processes can share a working
// directory, so matching on directory and start time could follow another
// process's session. Each start gets a new ID: a restart starts a new
// conversation, as it does for the other agents.
func withPiSessionID(args []string) []string {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		for _, flag := range piSessionFlags {
			if name == flag {
				return args
			}
		}
	}
	return append(append([]string{}, args...), "--session-id", newPiSessionID())
}

func newPiSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "agentapi-" + hex.EncodeToString(b[:])
}

// piSessionOptions returns the session ID and directory Pi was started with.
func piSessionOptions(args []string) jsonlwatcher.PiSession {
	return jsonlwatcher.PiSession{
		ID:  piFlagValue(args, "--session-id"),
		Dir: piFlagValue(args, "--session-dir"),
	}
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
