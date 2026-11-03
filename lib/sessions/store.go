package sessions

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/pricing"
)

// Store reads the sessions an agent had in a working directory from the
// agent's session logs, and keeps what it read for the next request: the
// logs of a long session run to hundreds of megabytes, and a request reads
// only what was appended since. The server holds one for its agent and
// directory. It is safe for concurrent use.
type Store struct {
	agent string
	// dir is the working directory: AgentAPI's, which the logs record by
	// its real path.
	dir    *agentlog.Dir
	prices *pricing.Table
	config Config
	logger *slog.Logger
	src    source

	// logs holds what was read of the sessions' logs, subs of their
	// subagents'.
	logs *agentlog.Index[*sessionLog]
	subs *agentlog.Index[*sessionLog]
	// views holds what was read of subagents' logs for their timeline
	// events and runs.
	views *agentlog.Index[*subagentView]
	// names reads Codex's session index, which holds its threads' names.
	names *agentlog.Index[*codexlog.ThreadNames]
	// metas holds the first lines of Codex's session logs.
	metas codexlog.Metas
	// costs makes concurrent Costs calls share one reading: GET /sessions,
	// GET /cost and the webhook ask at once, as the chat does on every
	// turn, and a long log takes a while. cost is what the last reading
	// leaves the next, which only it uses.
	costs singleflight.Group
	cost  costCache
	// costPasses counts the readings, and costPass runs in each (tests).
	costPasses atomic.Int64
	costPass   func()
	// readers numbers the logs' readers.
	readers atomic.Uint64
	// unpriced holds the models without a price that were logged.
	unpriced sync.Map
	// models holds the names of the models called, by themselves (intern).
	modelsMu sync.Mutex
	models   map[string]string

	mu sync.Mutex
	// paths finds the log of a session by its ID, as the logs read so far
	// say.
	paths map[string]string
}

// Config is what a Store needs besides the agent and its directory.
type Config struct {
	// Prices price the model calls; nil is the built-in prices.
	Prices *pricing.Table
	// PiSessionDir is the --session-dir Pi runs with, which holds its
	// session logs; empty when it runs without one.
	PiSessionDir string
	// Logger reports logs whose format the Store doesn't know all of; nil
	// is slog's default.
	Logger *slog.Logger
}

// The bounds of what a Store keeps of the logs it read: past them, it
// forgets the logs read longest ago, and reads them again when asked.
const (
	maxLogs     = 20_000
	maxLogBytes = 128 << 20
	// A subagent's view holds about twice its log.
	maxViews     = 1000
	maxViewBytes = 64 << 20
)

// Supported reports whether a Store reads the sessions of an agent of
// agentType.
func Supported(agentType string) bool {
	_, ok := sourceFor(agentType)
	return ok
}

// New returns a Store of the sessions an agent of agentType ("claude",
// "codex" or "pi") had in cwd.
func New(agentType, cwd string, config Config) (*Store, error) {
	src, ok := sourceFor(agentType)
	if !ok {
		return nil, fmt.Errorf("no session history for agent %q", agentType)
	}
	prices := config.Prices
	if prices == nil {
		prices = pricing.Builtin()
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Store{
		agent:  agentType,
		dir:    agentlog.NewDir(cwd),
		prices: prices,
		config: config,
		logger: logger,
		src:    src,
		names:  agentlog.NewIndex(codexlog.NewThreadNames, 1, maxLogBytes),
		paths:  make(map[string]string),
	}
	s.logs = agentlog.NewIndex(func(string) *sessionLog { return s.newSessionLog(false) }, maxLogs, maxLogBytes)
	s.subs = agentlog.NewIndex(func(string) *sessionLog { return s.newSessionLog(true) }, maxLogs, maxLogBytes)
	s.views = agentlog.NewIndex(func(string) *subagentView { return &subagentView{} }, maxViews, maxViewBytes)
	return s, nil
}

// Scope identifies the logs the Store reads, on its machine: its agent,
// the directory's real path, and the real paths of where the agent keeps
// its logs. Stores with one scope read the same sessions, and find that
// they spent the same.
func (s *Store) Scope() string {
	parts := []string{s.agent, s.dir.Paths()[0]}
	for _, root := range s.src.roots(s) {
		parts = append(parts, agentlog.RealPath(root))
	}
	return strings.Join(parts, "\n")
}

// logReader reads logs through an index, or a pass over one.
type logReader interface {
	Read(path string, fn func(*sessionLog, agentlog.FileInfo)) error
}

// readLog reads what was appended to a session's log since it was last
// read, through r (s.logs or a pass over it), and returns what was read of
// it, with its calls if withCalls.
func (s *Store) readLog(r logReader, path string, withCalls bool) (logSnapshot, error) {
	snap, err := s.readSubagentLog(r, path, withCalls)
	if err != nil {
		return logSnapshot{}, err
	}
	if snap.session {
		s.mu.Lock()
		s.paths[snap.summary.ID] = path
		s.mu.Unlock()
	} else if snap.summary.ID != "" {
		// Read on, it turned out another directory's.
		s.mu.Lock()
		if s.paths[snap.summary.ID] == path {
			delete(s.paths, snap.summary.ID)
		}
		s.mu.Unlock()
	}
	return snap, nil
}

// readSubagentLog is readLog for a subagent's log, through s.subs or a
// pass over it: it records no session's path.
func (s *Store) readSubagentLog(r logReader, path string, withCalls bool) (logSnapshot, error) {
	var snap logSnapshot
	err := r.Read(path, func(l *sessionLog, info agentlog.FileInfo) {
		l.reportDrift(path)
		snap = l.snapshot(path, info, withCalls)
	})
	return snap, err
}

// summaries reads the summaries of the directory's sessions.
func (s *Store) summaries() ([]SessionSummary, error) {
	files, err := s.src.files(s)
	if err != nil {
		return nil, err
	}
	var list []SessionSummary
	for _, path := range files {
		snap, err := s.readLog(s.logs, path, false)
		if err == nil && snap.session {
			if !s.src.namesInLog {
				snap.summary.Title = s.threadName(snap.summary.ID)
			}
			list = append(list, snap.summary)
		}
	}
	return list, nil
}

// pathOf returns the log of the session id, if a read found it and it is
// still there.
func (s *Store) pathOf(id string) (string, bool) {
	s.mu.Lock()
	path, ok := s.paths[id]
	s.mu.Unlock()
	if !ok {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		s.mu.Lock()
		delete(s.paths, id)
		s.mu.Unlock()
		return "", false
	}
	return path, true
}

// find returns the log of session id, if it is one of the directory's.
func (s *Store) find(id string) (string, bool) {
	if path, ok := s.pathOf(id); ok {
		return path, true
	}
	own, elsewhere := s.src.byID(s, id)
	for _, path := range append(own, elsewhere...) {
		got, ok, recorded := s.identify(path)
		// Away from the directory's own places, only the directory its
		// lines record tells whose the log is.
		if ok && got == id && (recorded || slices.Contains(own, path)) {
			s.mu.Lock()
			s.paths[id] = path
			s.mu.Unlock()
			return path, true
		}
	}
	return "", false
}

// maxIdentifyLines bounds how far identify reads a log for the working
// directory its lines record.
const maxIdentifyLines = 1000

// identify reads the start of a log for the session it holds, and reports
// whether it is one of the directory's, and whether its lines record the
// directory (Claude Code's). It reads as far as that takes, where reading
// the log through the index would read it all.
func (s *Store) identify(path string) (id string, ok, recorded bool) {
	l := s.newSessionLog(false)
	agentlog.ScanFile(path, func(line []byte) bool {
		return l.Line(line) && !l.identified() && l.lines < maxIdentifyLines
	})
	return l.id, l.session && l.id != "", l.recordedCWD
}

// threadName is the name Codex gave its thread id, from its session index.
func (s *Store) threadName(id string) string {
	path, err := codexlog.IndexPath()
	if err != nil {
		return ""
	}
	var name string
	_ = s.names.Read(path, func(names *codexlog.ThreadNames, _ agentlog.FileInfo) {
		name = names.Name(id)
	})
	return name
}
