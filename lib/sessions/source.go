package sessions

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agenthome"
	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
)

// source is how one agent keeps its session logs, and how to read them.
type source struct {
	format agentlog.Format
	// namesInLog says the log holds the session's name; Codex keeps it in
	// its session index (Store.threadName).
	namesInLog bool
	// files lists the logs that may be the directory's sessions.
	files func(s *Store) ([]string, error)
	// byID lists the logs that may be session id's, by their names: in the
	// directory's own places, and elsewhere, where a log must record the
	// directory (a long path's project directory Claude Code named
	// otherwise).
	byID func(s *Store, id string) (own, elsewhere []string)
	// linesRecordCWD says the lines of a log record the working directory
	// (Claude Code's), where its first line doesn't.
	linesRecordCWD bool
	// header reads the session's ID, its start and working directory
	// (empty when only the lines that follow say) from its log's first
	// line, and reports false for a log that isn't a session's (a
	// subagent's).
	header func(line []byte) (id string, started time.Time, cwd string, ok bool)
	// preamble reports a line that can come before the header, which is
	// skipped, up to maxPreamble of them; nil when the header is the
	// first line.
	preamble    func(line []byte) bool
	maxPreamble int
	// prompt returns the user's message a line holds, if it holds one.
	prompt func(line []byte) (string, time.Time, bool)
	// scan reads a session's lines, as agentlog.ScanFile does; nil is
	// agentlog.ScanFile. It differs for a log that refers to another one's
	// lines (a Codex fork).
	scan func(path string, fn func(line []byte) bool) bool
	// roots are where the agent keeps the logs: Claude Code's config
	// directory, Codex's home, Pi's sessions directories.
	roots func(s *Store) []string
	// threads says the logs are Codex's threads: a subagent's log starts
	// with a copy of its parent's and its turn events say how far it got,
	// and a fork's log refers to the lines of the thread it forked.
	threads bool
	// subagents lists the subagents the session id ran, in no order; nil
	// for an agent that runs none.
	subagents func(s *Store, id string) ([]Subagent, error)
	// costLogs lists the logs a reading of the costs reads, as
	// Store.costLogs; nil when they are the sessions' alone.
	costLogs func(s *Store) ([]string, func(id, path string) []Subagent, error)
	// subagentsUnordered says the subagents costLogs lists are in no order
	// of their start (Claude Code's, by their files' names).
	subagentsUnordered bool
}

func sourceFor(agentType string) (source, bool) {
	switch agentType {
	case "claude":
		return source{
			format:             claudelog.Format,
			namesInLog:         true,
			files:              claudeFiles,
			byID:               claudeLogsByID,
			linesRecordCWD:     true,
			header:             claudeHeader,
			preamble:           claudelog.Preamble,
			maxPreamble:        claudelog.MaxPreambleLines,
			prompt:             claudelog.Prompt,
			roots:              func(*Store) []string { return home(agenthome.ClaudeConfigDir()) },
			subagents:          (*Store).claudeSubagents,
			costLogs:           (*Store).claudeCostLogs,
			subagentsUnordered: true,
		}, true
	case "codex":
		return source{
			format:    codexlog.Format,
			files:     codexFiles,
			byID:      codexLogsByID,
			header:    codexlog.Header,
			prompt:    codexlog.Prompt,
			scan:      codexlog.ScanLog,
			roots:     func(*Store) []string { return home(agenthome.CodexHome()) },
			threads:   true,
			subagents: (*Store).codexSubagents,
			costLogs:  (*Store).codexCostLogs,
		}, true
	case "pi":
		return source{
			format:     pilog.Format,
			namesInLog: true,
			files:      piFiles,
			byID:       piLogsByID,
			header:     pilog.Header,
			prompt:     pilog.Prompt,
			roots:      piRoots,
		}, true
	}
	return source{}, false
}

// home is the directory an agenthome function returns, if it finds one.
func home(dir string, err error) []string {
	if err != nil {
		return nil
	}
	return []string{dir}
}

// lines calls fn with the lines of the session log at path.
func (src source) lines(path string, fn func(line []byte) bool) bool {
	if src.scan != nil {
		return src.scan(path, fn)
	}
	return agentlog.ScanFile(path, fn)
}

// logsIn lists the .jsonl files of dir.
func logsIn(dir string) ([]string, error) {
	files, _, err := entriesIn(dir)
	return files, err
}

// entriesIn lists the .jsonl files of dir, and the directories in it.
func entriesIn(dir string) (files, dirs []string, err error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir():
			dirs = append(dirs, filepath.Join(dir, entry.Name()))
		case filepath.Ext(entry.Name()) == ".jsonl":
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files, dirs, nil
}

// --- Claude Code ---

func claudeHeader(line []byte) (string, time.Time, string, bool) {
	id, started, ok := claudelog.Header(line)
	return id, started, "", ok
}

// claudeProjectDirs are the project directories that may hold the
// directory's sessions, by its real path and the one AgentAPI was given.
func claudeProjectDirs(s *Store) ([]string, error) {
	var dirs []string
	for _, path := range s.dir.Paths() {
		named, err := claudelog.ProjectDirs(path)
		if err != nil {
			return nil, err
		}
		for _, dir := range named {
			if !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs, nil
}

func claudeFiles(s *Store) ([]string, error) {
	files, _, err := claudeLogs(s)
	return files, err
}

// claudeLogs lists the logs in the directory's project directories, and
// the directories there: a session's subagents are in one named after it.
func claudeLogs(s *Store) (files, dirs []string, err error) {
	projects, err := claudeProjectDirs(s)
	if err != nil {
		return nil, nil, err
	}
	for i, project := range projects {
		logs, subdirs, err := entriesIn(project)
		if err != nil && i == 0 {
			return nil, nil, err
		}
		files, dirs = append(files, logs...), append(dirs, subdirs...)
	}
	return files, dirs, nil
}

// claudeLogsByID lists the logs named after session id: in the
// directory's project directories, and in any other, for a directory
// whose name Claude Code made otherwise.
func claudeLogsByID(s *Store, id string) (own, elsewhere []string) {
	if dirs, err := claudeProjectDirs(s); err == nil {
		for _, dir := range dirs {
			path := filepath.Join(dir, id+".jsonl")
			if _, err := os.Stat(path); err == nil {
				own = append(own, path)
			}
		}
	}
	for _, path := range claudelog.LogsByID(id) {
		if !slices.Contains(own, path) {
			elsewhere = append(elsewhere, path)
		}
	}
	return own, elsewhere
}

// claudeSubagentLogs lists the logs of the subagents of the session id
// whose log is at path: <session>/subagents/agent-<id>.jsonl next to it.
func claudeSubagentLogs(id, path string) []Subagent {
	if !ValidID(id) {
		return nil
	}
	logs, _ := logsIn(filepath.Join(filepath.Dir(path), id, "subagents"))
	subs := make([]Subagent, 0, len(logs))
	for _, log := range logs {
		name := strings.TrimSuffix(filepath.Base(log), ".jsonl")
		subs = append(subs, Subagent{ID: strings.TrimPrefix(name, "agent-"), path: log, kind: "claude"})
	}
	return subs
}

// --- Codex ---

// codexThread is one of Codex's session logs, with its first line.
type codexThread struct {
	path string
	meta *codexlog.SessionMeta
}

// codexThreads lists Codex's session logs with their first lines, which
// say where the thread ran and whose subagent it is. Codex keeps every
// directory's logs in one tree, so finding a directory's takes all their
// first lines; the Store reads each once.
func (s *Store) codexThreads() ([]codexThread, error) {
	root, err := codexlog.SessionsRoot("")
	if err != nil {
		return nil, err
	}
	rollouts, err := codexlog.Rollouts(root)
	if err != nil {
		return nil, err
	}
	listed := make(map[string]bool, len(rollouts))
	for _, path := range rollouts {
		listed[path] = true
	}
	s.metas.Retain(func(path string) bool { return listed[path] })
	threads := make([]codexThread, 0, len(rollouts))
	for _, path := range rollouts {
		if meta, err := s.metas.Get(path); err == nil {
			threads = append(threads, codexThread{path: path, meta: meta})
		}
	}
	return threads, nil
}

// codexFiles lists the logs of Codex's threads in the directory.
func codexFiles(s *Store) ([]string, error) {
	threads, err := s.codexThreads()
	if err != nil {
		return nil, err
	}
	return codexSessionLogs(s, threads), nil
}

// codexSessionLogs are the logs of threads that are sessions in the
// directory.
func codexSessionLogs(s *Store, threads []codexThread) []string {
	var files []string
	for _, t := range threads {
		if !t.meta.IsSubAgent() && s.dir.Is(t.meta.Payload.CWD) {
			files = append(files, t.path)
		}
	}
	return files
}

// codexLogsByID lists the log of thread id: Codex names it after it.
func codexLogsByID(_ *Store, id string) (own, elsewhere []string) {
	if path, ok := codexlog.LogOf(id); ok {
		return []string{path}, nil
	}
	return nil, nil
}

// --- Pi ---

func piFiles(s *Store) ([]string, error) {
	var files []string
	var dirs []string
	for _, path := range s.dir.Paths() {
		dir, err := pilog.SessionsDir(path, s.config.PiSessionDir)
		if err != nil {
			return nil, err
		}
		if slices.Contains(dirs, dir) {
			continue
		}
		dirs = append(dirs, dir)
		logs, err := logsIn(dir)
		if err != nil {
			return nil, err
		}
		files = append(files, logs...)
	}
	return files, nil
}

// piRoots are the directories Pi keeps the directory's sessions in.
func piRoots(s *Store) []string {
	var dirs []string
	for _, path := range s.dir.Paths() {
		if dir, err := pilog.SessionsDir(path, s.config.PiSessionDir); err == nil && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// piLogsByID lists the logs of session id: Pi names a session's log
// <time>_<id>.jsonl.
func piLogsByID(s *Store, id string) (own, elsewhere []string) {
	files, _ := piFiles(s)
	for _, path := range files {
		if strings.HasSuffix(filepath.Base(path), "_"+id+".jsonl") {
			own = append(own, path)
		}
	}
	return own, nil
}
