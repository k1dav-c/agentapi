package codexlog

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
)

// Header reads the thread ID, its start and working directory from the
// first line of a log, and reports false for a log that isn't a session's
// (a subagent's).
func Header(line []byte) (id string, started time.Time, cwd string, ok bool) {
	var meta SessionMeta
	if json.Unmarshal(line, &meta) != nil || meta.Payload.ID == "" || meta.IsSubAgent() {
		return "", time.Time{}, "", false
	}
	started, _ = time.Parse(time.RFC3339Nano, meta.Timestamp)
	return meta.Payload.ID, started, meta.Payload.CWD, true
}

// Prompt takes the user's messages from the model's input, without the
// context Codex adds there in the user's name (the environment,
// AGENTS.md).
func Prompt(line []byte) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"response_item"`)) || !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry codexLine
	if json.Unmarshal(line, &entry) != nil || entry.Type != "response_item" {
		return "", time.Time{}, false
	}
	var payload codexPayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Type != "message" || payload.Role != "user" {
		return "", time.Time{}, false
	}
	var blocks []codexContentBlock
	if json.Unmarshal(payload.Content, &blocks) != nil {
		return "", time.Time{}, false
	}
	text := strings.TrimSpace(userText(blocks))
	if text == "" {
		return "", time.Time{}, false
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return text, at, true
}

// maxForkDepth bounds how many forks back ScanLog follows.
const maxForkDepth = 16

// ScanLog reads a Codex session log. A fork's log holds only what happened
// since: its session_meta names the thread it forked (forked_from_id) and
// how much of that thread's log it continues
// (forked_from_ordinal_exclusive), so those lines come first, after the
// fork's own session_meta line.
func ScanLog(path string, fn func(line []byte) bool) bool {
	return scanFork(path, -1, 0, fn)
}

// ScanForked reads the lines of the thread a fork continues: those of
// thread id's log before the line numbered before (all when negative),
// and those of the threads it continues in turn. It returns false when
// fn did.
func ScanForked(id string, before int64, fn func(line []byte) bool) bool {
	parent, found := LogOf(id)
	if !found {
		return true
	}
	return scanUntilStopped(parent, before, 1, fn)
}

// scanFork reads the log at path up to the line numbered before (every
// line when negative); depth counts the forks followed. Only the first
// log's session_meta line is passed on.
func scanFork(path string, before int64, depth int, fn func(line []byte) bool) bool {
	first := true
	return agentlog.ScanFile(path, func(line []byte) bool {
		var entry struct {
			Ordinal *int64 `json:"ordinal"`
			Type    string `json:"type"`
			Payload struct {
				ForkedFromID string `json:"forked_from_id"`
				ForkedBefore *int64 `json:"forked_from_ordinal_exclusive"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(line, &entry)
		if before >= 0 && entry.Ordinal != nil && *entry.Ordinal >= before {
			return false
		}
		isMeta := first && entry.Type == "session_meta"
		first = false
		switch {
		case !isMeta:
			return fn(line)
		case depth > 0:
			// The forked thread's meta: the fork's own came first.
			return true
		case !fn(line):
			return false
		case entry.Payload.ForkedFromID == "" || depth >= maxForkDepth:
			return true
		}
		parent, found := LogOf(entry.Payload.ForkedFromID)
		if !found {
			return true
		}
		limit := int64(-1)
		if entry.Payload.ForkedBefore != nil {
			limit = *entry.Payload.ForkedBefore
		}
		return scanUntilStopped(parent, limit, depth+1, fn)
	})
}

// scanUntilStopped is scanFork that reports false when fn did, rather
// than whether the log could be read.
func scanUntilStopped(path string, before int64, depth int, fn func(line []byte) bool) bool {
	stopped := false
	scanFork(path, before, depth, func(line []byte) bool {
		stopped = !fn(line)
		return !stopped
	})
	return !stopped
}

// LogOf finds the log of a Codex thread: its file is named after it
// (rollout-<time>-<thread id>.jsonl).
func LogOf(threadID string) (string, bool) {
	if threadID == "" || strings.ContainsAny(threadID, `/\*?[`) {
		return "", false
	}
	root, err := SessionsRoot("")
	if err != nil {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}
