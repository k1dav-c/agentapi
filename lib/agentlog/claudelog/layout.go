package claudelog

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/coder/agentapi/lib/agenthome"
)

// maxProjectDirName is how long a project directory's name can be before
// Claude Code shortens it.
const maxProjectDirName = 200

// maxFileName is how long a file name can be on most file systems.
const maxFileName = 255

// EncodeCWD encodes a working directory path the way Claude Code names its
// projects directory: every character other than an ASCII letter or digit
// becomes a dash. For example, "/home/k1dave6412" becomes "-home-k1dave6412"
// and "/home/me/.cache/my_app" becomes "-home-me--cache-my-app". Claude
// Code replaces the UTF-16 code units of the path, so a character outside
// the Basic Multilingual Plane becomes two dashes.
func EncodeCWD(cwd string) string {
	units := utf16.Encode([]rune(cwd))
	var b strings.Builder
	b.Grow(len(units))
	for _, u := range units {
		if u < 0x80 && (u >= 'a' && u <= 'z' || u >= 'A' && u <= 'Z' || u >= '0' && u <= '9') {
			b.WriteByte(byte(u))
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ProjectDirName is the name of the directory Claude Code keeps the
// sessions it ran in cwd in: EncodeCWD's, which past 200 characters Claude
// Code cuts there and follows with a hash of the path (Java's String
// hashCode, in base 36).
func ProjectDirName(cwd string) string {
	name := EncodeCWD(cwd)
	if len(name) <= maxProjectDirName {
		return name
	}
	var hash int32
	for _, u := range utf16.Encode([]rune(cwd)) {
		hash = hash<<5 - hash + int32(u)
	}
	abs := int64(hash)
	if abs < 0 {
		abs = -abs
	}
	return name[:maxProjectDirName] + "-" + strconv.FormatInt(abs, 36)
}

// ProjectDirs are the directories that may hold the sessions Claude Code
// ran in cwd: the one ProjectDirName names, and for a long path the ones
// other versions named (the whole encoded path, or another hash).
func ProjectDirs(cwd string) ([]string, error) {
	configDir, err := agenthome.ClaudeConfigDir()
	if err != nil {
		return nil, err
	}
	projects := filepath.Join(configDir, "projects")
	name := ProjectDirName(cwd)
	dirs := []string{filepath.Join(projects, name)}
	if encoded := EncodeCWD(cwd); encoded != name {
		if len(encoded) <= maxFileName {
			dirs = append(dirs, filepath.Join(projects, encoded))
		}
		matches, _ := filepath.Glob(filepath.Join(projects, encoded[:maxProjectDirName]+"-*"))
		for _, m := range matches {
			if m != dirs[0] {
				dirs = append(dirs, m)
			}
		}
	}
	return dirs, nil
}

// LogsByID returns the session logs named after session id in any
// project directory: Claude Code names a session's log after it.
func LogsByID(id string) []string {
	if id == "" || strings.ContainsAny(id, `/\*?[`) || id == "." || id == ".." {
		return nil
	}
	configDir, err := agenthome.ClaudeConfigDir()
	if err != nil {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", id+".jsonl"))
	var logs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.Mode().IsRegular() {
			logs = append(logs, m)
		}
	}
	return logs
}
