package sessions

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
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

	// lines counts the lines read, skipped those before the header; headed
	// says the header was read.
	lines, skipped int
	headed         bool
	// session says the log is one of the directory's sessions: its header
	// said so, and the working directory its lines record (Claude
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
	// priced are the log's calls at the store's prices, in its order: a
	// reading of the costs prices only the calls read or changed since
	// the last. reader identifies the reader among the store's, and
	// version counts the changes to priced.
	priced  []ModelCall
	reader  uint64
	version int
	// drift counts what the log holds that its format doesn't know, its
	// calls' included.
	drift *agentlog.Drift

	// from is the first line of a Codex subagent's log that is its own:
	// Codex starts a subagent's log with a copy of its parent's.
	from int64
	// status is how far a Codex subagent got, by the last turn event of
	// its own lines: a turn that started and hasn't ended still runs.
	status string
}

func (s *Store) newSessionLog(subagent bool) *sessionLog {
	drift := agentlog.NewDrift(s.src.format)
	l := &sessionLog{
		store: s, src: &s.src, subagent: subagent, codex: s.agent == "codex",
		calls: s.src.format.NewCalls(drift), drift: drift, reader: s.readers.Add(1),
	}
	if s.src.namesInLog {
		l.namer = s.src.format.NewNamer()
	}
	return l
}

// Line reads the log's next line.
func (l *sessionLog) Line(line []byte) bool {
	l.lines++
	l.drift.Line(line)
	if !l.headed {
		if !l.subagent && l.src.preamble != nil && l.skipped < l.src.maxPreamble && l.src.preamble(line) {
			l.skipped++
			return true
		}
		l.headed = true
		if !l.head(line) {
			// Not a session of the directory: nothing of it counts.
			return false
		}
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

// head reads the log's header, its first line but those that can come
// before it (Claude Code's), and reports whether the log is to be
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
// header did, and the first working directory its lines record
// (Claude Code).
func (l *sessionLog) identified() bool {
	return l.headed && (!l.src.linesRecordCWD || l.recordedCWD)
}

// own reports whether a line of a Codex subagent's log is its own rather
// than one copied from its parent.
func (l *sessionLog) own(line []byte) bool {
	return codexOwnLine(line, l.from)
}

// codexOwnLine reports whether a line of a Codex subagent's log is its own,
// its first own line being from: not its parent's, which the log starts
// with a copy of, nor a session_meta line. A line without an ordinal is
// its own. The line's fields are found without decoding it: the copy is of
// the parent's whole log.
func codexOwnLine(line []byte, from int64) bool {
	if !agentlog.Members(line, func(_, _ []byte) bool { return true }) {
		return true
	}
	if typ, _ := agentlog.StringField(line, "type"); typ == "session_meta" {
		return false
	}
	raw, ok := agentlog.Field(line, "ordinal")
	if !ok {
		return true
	}
	ordinal, err := strconv.ParseInt(string(raw), 10, 64)
	return err != nil || ordinal >= from
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
	return 256 + len(l.firstPrompt) + len(l.title) + l.calls.Size() + len(l.priced)*agentlog.CallSize
}

// price prices the calls read or changed since it last ran.
func (l *sessionLog) price() {
	from, calls := l.calls.Changed()
	if from == len(l.priced) && len(calls) == 0 {
		return
	}
	priced := l.priced[:from]
	if from < len(l.priced) {
		// Calls priced before changed: the readings that hold them keep
		// them as they were.
		priced = slices.Clone(priced)
	}
	for _, c := range calls {
		priced = append(priced, l.store.price(c))
	}
	l.priced = priced
	l.version++
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
	// calls are the log's calls, priced, which the caller must not
	// change; read says which reading of the log they are.
	calls []ModelCall
	read  logRead
	info  agentlog.FileInfo
}

// logRead says which reading of a log calls were made of: by which of the
// store's readers, after how many changes to its calls, and how many there
// were. A subagent's says which subagent's they are.
type logRead struct {
	reader   uint64
	version  int
	calls    int
	subagent string
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
		l.price()
		snap.calls = slices.Clip(l.priced)
		snap.read = logRead{reader: l.reader, version: l.version, calls: len(l.priced)}
	}
	return snap
}
