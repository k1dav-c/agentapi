package sessions

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
)

// source is how one agent keeps its session logs, and how to read them.
type source struct {
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
	// header reads the session's ID and start from its log's first line,
	// and reports false for a log that isn't a session of the directory
	// (another directory's, a subagent's).
	header func(line []byte, cwd string) (id string, started time.Time, ok bool)
	// prompt returns the user's message a line holds, if it holds one.
	prompt func(line []byte) (string, time.Time, bool)
	parser func() agentlog.LineParser
	// namer follows the session's name in its log; nil when the agent
	// keeps the name elsewhere (Codex).
	namer func() agentlog.SessionNamer
	calls func() agentlog.CallReader
	// scan reads a session's lines, as agentlog.ScanFile does; nil is
	// agentlog.ScanFile. It differs for a log that refers to another one's
	// lines (a Codex fork).
	scan func(path string, fn func(line []byte) bool) bool
}

func sourceFor(agentType string) (source, bool) {
	switch agentType {
	case "claude":
		return source{
			files:          claudeFiles,
			byID:           claudeLogsByID,
			linesRecordCWD: true,
			header:         func(line []byte, _ string) (string, time.Time, bool) { return claudelog.HistoryHeader(line) },
			prompt:         claudelog.Prompt,
			parser:         func() agentlog.LineParser { return claudelog.NewParser() },
			namer:          func() agentlog.SessionNamer { return claudelog.NewNamer() },
			calls:          func() agentlog.CallReader { return claudelog.NewCalls() },
		}, true
	case "codex":
		return source{
			files:  codexFiles,
			byID:   codexLogsByID,
			header: codexlog.HistoryHeader,
			prompt: codexlog.Prompt,
			parser: func() agentlog.LineParser { return codexlog.NewParser() },
			calls:  func() agentlog.CallReader { return codexlog.NewCalls() },
			scan:   codexlog.ScanLog,
		}, true
	case "pi":
		return source{
			files:  piFiles,
			byID:   piLogsByID,
			header: pilog.HistoryHeader,
			prompt: pilog.Prompt,
			parser: func() agentlog.LineParser { return pilog.NewParser() },
			namer:  func() agentlog.SessionNamer { return pilog.NewNamer() },
			calls:  func() agentlog.CallReader { return pilog.NewCalls() },
		}, true
	}
	return source{}, false
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
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	return files, nil
}

// --- Claude Code ---

func claudeFiles(s *Store) ([]string, error) {
	dirs, err := claudelog.ProjectDirs(s.cwd)
	if err != nil {
		return nil, err
	}
	var files []string
	for i, dir := range dirs {
		logs, err := logsIn(dir)
		if err != nil && i == 0 {
			return nil, err
		}
		files = append(files, logs...)
	}
	return files, nil
}

// claudeLogsByID lists the logs named after session id: in the
// directory's project directories, and in any other, for a directory
// whose name Claude Code made otherwise.
func claudeLogsByID(s *Store, id string) (own, elsewhere []string) {
	if dirs, err := claudelog.ProjectDirs(s.cwd); err == nil {
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

// --- Codex ---

// codexHistoryScanLimit bounds how many session logs a Store looks
// through to find the directory's: Codex keeps every directory's in one
// tree.
const codexHistoryScanLimit = 2000

// codexRollouts lists the latest of Codex's session logs, of all
// directories.
func codexRollouts() ([]string, error) {
	root, err := codexlog.SessionsRoot("")
	if err != nil {
		return nil, err
	}
	var files []string
	// The tree is sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl: walking
	// it in reverse name order goes from the latest.
	err = walkReverse(root, 3, func(dir string) bool {
		day, err := logsIn(dir)
		if err != nil {
			return true
		}
		slices.Reverse(day)
		files = append(files, day...)
		return len(files) < codexHistoryScanLimit
	})
	return files, err
}

// codexThread is one of Codex's session logs, with its first line.
type codexThread struct {
	path string
	meta *codexlog.SessionMeta
}

// codexThreads lists Codex's session logs with their first lines, which
// say where the thread ran and whose subagent it is.
func (s *Store) codexThreads() ([]codexThread, error) {
	rollouts, err := codexRollouts()
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
	var files []string
	for _, t := range threads {
		if !t.meta.IsSubAgent() && filepath.Clean(t.meta.Payload.CWD) == filepath.Clean(s.cwd) {
			files = append(files, t.path)
		}
	}
	return files, nil
}

// codexLogsByID lists the log of thread id: Codex names it after it.
func codexLogsByID(_ *Store, id string) (own, elsewhere []string) {
	if path, ok := codexlog.LogOf(id); ok {
		return []string{path}, nil
	}
	return nil, nil
}

// walkReverse calls fn with the directories depth levels below root, in
// reverse name order, until it returns false.
func walkReverse(root string, depth int, fn func(dir string) bool) error {
	if depth == 0 {
		if !fn(root) {
			return fs.SkipAll
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].IsDir() {
			continue
		}
		if err := walkReverse(filepath.Join(root, entries[i].Name()), depth-1, fn); err != nil {
			if errors.Is(err, fs.SkipAll) && depth == 3 {
				return nil
			}
			return err
		}
	}
	return nil
}

// --- Pi ---

func piFiles(s *Store) ([]string, error) {
	dir, err := pilog.SessionsDir(s.cwd, "")
	if err != nil {
		return nil, err
	}
	return logsIn(dir)
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
