package codexlog

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
)

// HistoryHeader reads the thread ID and start from the first line of a
// log, and reports false for a log that isn't a session of cwd (another
// directory's, a subagent's).
func HistoryHeader(line []byte, cwd string) (string, time.Time, bool) {
	var meta SessionMeta
	if json.Unmarshal(line, &meta) != nil || meta.Payload.ID == "" || meta.IsSubAgent() {
		return "", time.Time{}, false
	}
	if filepath.Clean(meta.Payload.CWD) != filepath.Clean(cwd) {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, meta.Timestamp)
	return meta.Payload.ID, started, true
}

// Prompt takes the user's messages from the model's input. Codex adds
// messages of its own there (the environment, AGENTS.md), which start with
// a tag.
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
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" || strings.HasPrefix(text, "<") {
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
	stopped := false
	scanFork(parent, before, 1, func(line []byte) bool {
		if !fn(line) {
			stopped = true
			return false
		}
		return true
	})
	return !stopped
}

// scanFork reads the log at path up to the line numbered before (every
// line when negative); depth counts the forks followed. Only the first
// log's session_meta line is passed on.
func scanFork(path string, before int64, depth int, fn func(line []byte) bool) bool {
	first, stopped := true, false
	ok := agentlog.ScanFile(path, func(line []byte) bool {
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
		if isMeta && depth > 0 {
			// The forked thread's meta: the fork's own came first.
			return true
		}
		if isMeta {
			if !fn(line) {
				stopped = true
				return false
			}
		}
		if isMeta && entry.Payload.ForkedFromID != "" && depth < maxForkDepth {
			if parent, found := LogOf(entry.Payload.ForkedFromID); found {
				limit := int64(-1)
				if entry.Payload.ForkedBefore != nil {
					limit = *entry.Payload.ForkedBefore
				}
				scanFork(parent, limit, depth+1, func(line []byte) bool {
					if !fn(line) {
						stopped = true
						return false
					}
					return true
				})
				if stopped {
					return false
				}
			}
			return true
		}
		if isMeta {
			return true
		}
		if !fn(line) {
			stopped = true
			return false
		}
		return true
	})
	return ok
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
