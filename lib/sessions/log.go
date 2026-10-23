package sessions

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
)

// sessionLog reads a session's log, or a subagent's, as far as it was
// written: the session's summary and the model calls. It reads the lines
// appended later from where it stopped (agentlog.Index).
type sessionLog struct {
	store *Store
	src   *source
	// subagent says the log is a subagent's: its calls are read, and no
	// summary.
	subagent bool
	codex    bool

	lines int
	// session says the log is one of the directory's sessions: its first
	// line said so, and the working directory its lines record (Claude
	// Code) is the directory.
	session     bool
	recordedCWD bool
	id          string
	started     time.Time
	title       string
	firstPrompt string
	prompts     int
	namer       agentlog.SessionNamer
	// firstAt is the time of the first line that has one: when a
	// subagent started.
	firstAt time.Time

	calls agentlog.CallReader
	// drift counts what the log holds that its format doesn't know.
	drift *agentlog.Drift

	// from is the first line of a Codex subagent's log that is its own:
	// Codex starts a subagent's log with a copy of its parent's.
	from int64
	// status is how far a Codex subagent got, by the last turn event of
	// its own lines: a turn that started and hasn't ended still runs.
	status string
}

func (s *Store) newSessionLog(subagent bool) *sessionLog {
	l := &sessionLog{
		store: s, src: &s.src, subagent: subagent, codex: s.agent == "codex",
		calls: s.src.format.NewCalls(), drift: agentlog.NewDrift(s.src.format),
	}
	if s.src.namesInLog {
		l.namer = s.src.format.NewNamer()
	}
	return l
}

// Line reads the log's next line.
func (l *sessionLog) Line(line []byte) bool {
	first := l.lines == 0
	l.lines++
	l.drift.Line(line)
	if first && !l.head(line) {
		// Not a session of the directory: nothing of it counts.
		return false
	}
	if l.src.linesRecordCWD && !l.subagent && !l.recordedCWD {
		// The directory's name doesn't say whose log it is: Claude Code
		// names /w/a_b's and /w/a-b's alike. The first working directory
		// a line records does.
		if cwd, ok := agentlog.StringField(line, "cwd"); ok && cwd != "" {
			l.recordedCWD = true
			if !l.store.dir.Is(cwd) {
				l.session = false
				return false
			}
		}
	}
	if l.firstAt.IsZero() {
		if at, ok := agentlog.LineTime(line); ok {
			l.firstAt = at
		}
	}
	if l.from > 0 && !l.own(line) {
		return true
	}
	if !l.subagent {
		l.summarize(line)
	}
	l.calls.Line(line)
	if l.codex && l.subagent {
		l.trackStatus(line)
	}
	return true
}

// head reads the log's first line, and reports whether the log is to be
// read on.
func (l *sessionLog) head(line []byte) bool {
	if l.subagent {
		if l.codex {
			var meta codexlog.SessionMeta
			if json.Unmarshal(line, &meta) == nil {
				l.from = meta.Payload.HistoryStart
			}
		}
		return true
	}
	id, started, cwd, ok := l.src.header(line)
	if !ok || (cwd != "" && !l.store.dir.Is(cwd)) {
		return false
	}
	l.session, l.id, l.started = true, id, started
	if l.codex {
		// A fork's log holds only what happened since it forked; the
		// prompts before were the thread's it forked.
		var meta codexlog.SessionMeta
		if json.Unmarshal(line, &meta) == nil && meta.Payload.ForkedFromID != "" {
			before := int64(-1)
			if meta.Payload.ForkedBefore != nil {
				before = *meta.Payload.ForkedBefore
			}
			codexlog.ScanForked(meta.Payload.ForkedFromID, before, func(line []byte) bool {
				l.summarize(line)
				return true
			})
		}
	}
	return true
}

// identified reports whether the log said whose session it holds: its
// first line did, and the first working directory its lines record
// (Claude Code).
func (l *sessionLog) identified() bool {
	return l.lines > 0 && (!l.src.linesRecordCWD || l.recordedCWD)
}

// own reports whether a line of a Codex subagent's log is its own rather
// than one copied from its parent.
func (l *sessionLog) own(line []byte) bool {
	var entry struct {
		Ordinal *int64 `json:"ordinal"`
		Type    string `json:"type"`
	}
	return json.Unmarshal(line, &entry) != nil || (entry.Type != "session_meta" && (entry.Ordinal == nil || *entry.Ordinal >= l.from))
}

// summarize reads the session's name and the user's prompts.
func (l *sessionLog) summarize(line []byte) {
	if l.namer != nil {
		if name, changed := l.namer.Name(line); changed {
			l.title = name
		}
	}
	if prompt, at, ok := l.src.prompt(line); ok {
		if l.prompts == 0 {
			l.firstPrompt = shorten(prompt, firstPromptLength)
			if l.started.IsZero() {
				l.started = at
			}
		}
		l.prompts++
	}
}

func (l *sessionLog) trackStatus(line []byte) {
	if !bytes.Contains(line, []byte(`"event_msg"`)) {
		return
	}
	var entry struct {
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	switch entry.Payload.Type {
	case "task_started":
		l.status = "running"
	case "task_complete":
		l.status = "completed"
	case "turn_aborted":
		l.status = "failed"
	}
}

// Size is about how many bytes of memory the reader holds.
func (l *sessionLog) Size() int {
	return 256 + len(l.firstPrompt) + len(l.title) + l.calls.Size()
}

// logSnapshot is what was read of a log, which the reader's caller may
// keep.
type logSnapshot struct {
	// session says the log is one of the directory's sessions, summary
	// what it says of it.
	session bool
	summary SessionSummary
	// firstAt is the time of the first line that has one.
	firstAt time.Time
	status  string
	calls   []agentlog.Call
	info    agentlog.FileInfo
}

// reportDrift logs once what the log holds that its format doesn't know,
// for a log that is the directory's.
func (l *sessionLog) reportDrift(path string) {
	if l.session || l.subagent {
		l.drift.Report(l.store.logger, path)
	}
}

// snapshot returns what l read, with its calls if withCalls.
func (l *sessionLog) snapshot(path string, info agentlog.FileInfo, withCalls bool) logSnapshot {
	snap := logSnapshot{
		session: l.session && l.id != "",
		summary: SessionSummary{
			ID: l.id, Title: l.title, FirstPrompt: l.firstPrompt, Prompts: l.prompts,
			CreatedAt: l.started, UpdatedAt: info.ModTime, path: path,
		},
		firstAt: l.firstAt,
		status:  l.status,
		info:    info,
	}
	if withCalls {
		snap.calls = l.calls.Calls()
	}
	return snap
}
