// Package providerlog keeps AgentAPI's provider ledger: which model
// provider each agent session ran on, and from when. Claude Code's session
// log doesn't say which endpoint a call went to (AgentAPI points the agent
// at a provider through its environment), so the ledger is what attributes
// a Claude Code session's calls to a provider; Codex's log names the
// provider itself.
//
// The ledger is a JSON Lines file beside the config file
// (~/.agentapi/provider-ledger.jsonl), which every AgentAPI of the user's
// on the machine appends to: one line, in one write, for each entry.
// Entries older than Retention are dropped when a process rewrites it, at
// most once a day; the rewrite and the appends take a lock file in turn,
// so no entry is lost to it.
package providerlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/filelock"
	"github.com/coder/agentapi/lib/provider"
)

// FileName is the ledger's name, beside the config file.
const FileName = "provider-ledger.jsonl"

// Retention is how long an entry is kept.
const Retention = 90 * 24 * time.Hour

// pruneInterval is how often a process rewrites the ledger without the
// entries past Retention, at most.
const pruneInterval = 24 * time.Hour

// lockTimeout bounds the wait for another process's append or rewrite.
const lockTimeout = 5 * time.Second

// Entry is a line of the ledger: from At on, the agent's session ran on
// the provider.
type Entry struct {
	At time.Time `json:"at"`
	// Agent is the agent type: "claude" or "codex".
	Agent string `json:"agent"`
	// Session is the agent's own session ID.
	Session string `json:"session"`
	// Provider is the provider's ID; provider.Default for the agent's own.
	Provider string `json:"provider"`
}

// Mark is an entry of a session: from At on, it ran on Provider, "" for
// the agent's own.
type Mark struct {
	At       time.Time
	Provider string
}

// Ledger is the provider ledger at a path. It is safe for concurrent use.
type Ledger struct {
	path   string
	clock  quartz.Clock
	logger *slog.Logger

	// writeMu serializes this process's appends and rewrites (the lock
	// file serializes them with other processes').
	writeMu   sync.Mutex
	lastPrune time.Time

	mu sync.Mutex
	// offset is how far the file was read; tail the last line read, which
	// ends there, to tell a rewritten file from a grown one.
	offset int64
	tail   []byte
	// marks are the sessions' marks by agent and session, the earliest
	// first. A map or slice returned is never changed: a change makes new
	// ones.
	marks map[string]map[string][]Mark
	// version counts the changes to marks.
	version uint64
}

// Options configure a Ledger.
type Options struct {
	// Clock tells the time of a rewrite; nil is the real clock.
	Clock quartz.Clock
	// Logger reports lines the ledger can't read; nil is slog's default.
	Logger *slog.Logger
}

// Open returns the ledger at path; the file is created by the first entry
// recorded.
func Open(path string, options Options) *Ledger {
	if options.Clock == nil {
		options.Clock = quartz.NewReal()
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Ledger{path: path, clock: options.Clock, logger: options.Logger}
}

// Path is where the ledger is.
func (l *Ledger) Path() string { return l.path }

// Record appends e, at most a day after the last rewrite rewriting the
// ledger without the entries past Retention first.
func (l *Ledger) Record(e Entry) error {
	if e.Session == "" || e.Agent == "" {
		return errors.New("a provider ledger entry needs an agent and a session")
	}
	if e.Provider == "" {
		e.Provider = provider.Default
	}
	e.At = e.At.UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("create the provider ledger's directory: %w", err)
	}
	unlock, err := l.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if now := l.clock.Now(); l.lastPrune.IsZero() || now.Sub(l.lastPrune) >= pruneInterval {
		l.lastPrune = now
		if err := l.pruneLocked(now); err != nil {
			l.logger.Warn("Failed to drop the provider ledger's old entries", "path", l.path, "error", err)
		}
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open the provider ledger: %w", err)
	}
	// One write: other processes' appends come before or after it, whole.
	_, err = f.Write(line)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("append to the provider ledger: %w", err)
	}
	return nil
}

// Prune rewrites the ledger without the entries past Retention, if it has
// any.
func (l *Ledger) Prune() error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	unlock, err := l.lock()
	if err != nil {
		return err
	}
	defer unlock()
	now := l.clock.Now()
	l.lastPrune = now
	return l.pruneLocked(now)
}

// lock takes the lock file other processes append and rewrite under.
func (l *Ledger) lock() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	unlock, err := filelock.Lock(ctx, l.path+".lock")
	if err != nil {
		return nil, fmt.Errorf("lock the provider ledger: %w", err)
	}
	return unlock, nil
}

// pruneLocked rewrites the ledger without the entries older than
// Retention before now, and the lines it can't read. writeMu and the lock
// are held.
func (l *Ledger) pruneLocked(now time.Time) error {
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := now.Add(-Retention)
	kept := make([]byte, 0, len(data))
	dropped := false
	for line := range bytes.Lines(data) {
		e, ok := parse(line)
		if !ok || e.At.Before(cutoff) {
			dropped = true
			continue
		}
		kept = append(kept, line...)
	}
	if !dropped {
		return nil
	}
	return atomicfile.Write(l.path, ".provider-ledger-*", kept)
}

// parse reads a line of the ledger: a complete one, with an agent, a
// session and a time.
func parse(line []byte) (Entry, bool) {
	var e Entry
	if !bytes.HasSuffix(line, []byte("\n")) || json.Unmarshal(line, &e) != nil ||
		e.Agent == "" || e.Session == "" || e.At.IsZero() {
		return Entry{}, false
	}
	if e.Provider == provider.Default {
		e.Provider = ""
	}
	return e, true
}

// Sessions returns the marks of agent's sessions by their ID, each
// session's the earliest first, and a version that changes with any
// session's: the ledger read on from where the last call stopped. The
// caller must not change them. A ledger that can't be read has none.
func (l *Ledger) Sessions(agent string) (map[string][]Mark, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.readLocked(); err != nil {
		l.logger.Debug("Failed to read the provider ledger", "path", l.path, "error", err)
	}
	return l.marks[agent], l.version
}

// readLocked reads what was appended to the ledger since it last read
// it; a ledger rewritten meanwhile, read all again. l.mu is held.
func (l *Ledger) readLocked() error {
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.resetLocked()
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	switch {
	case l.offset > 0 && (info.Size() < l.offset || !l.tailMatches(f)):
		// Rewritten: read from the start.
		l.resetLocked()
	case info.Size() == l.offset:
		return nil
	}
	if _, err := f.Seek(l.offset, io.SeekStart); err != nil {
		return err
	}
	var added []Entry
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// The rest is a line being written, read once it's whole.
			break
		}
		l.offset += int64(len(line))
		l.tail = append(l.tail[:0], line...)
		e, ok := parse(line)
		if !ok {
			l.logger.Debug("Skipped a line of the provider ledger it can't read", "path", l.path)
			continue
		}
		added = append(added, e)
	}
	if len(added) > 0 {
		l.addLocked(added)
	}
	return nil
}

// tailMatches reports whether f still has the line read last where it
// ended: a file rewritten since has other lines there.
func (l *Ledger) tailMatches(f *os.File) bool {
	if int64(len(l.tail)) > l.offset {
		return false
	}
	buf := make([]byte, len(l.tail))
	if _, err := f.ReadAt(buf, l.offset-int64(len(l.tail))); err != nil {
		return false
	}
	return bytes.Equal(buf, l.tail)
}

// resetLocked forgets what was read.
func (l *Ledger) resetLocked() {
	if l.offset == 0 && l.marks == nil {
		return
	}
	l.offset, l.tail = 0, nil
	if len(l.marks) > 0 {
		l.version++
	}
	l.marks = nil
}

// addLocked adds the marks of entries: in new maps and slices, which the
// callers of Sessions don't hold.
func (l *Ledger) addLocked(entries []Entry) {
	marks := maps.Clone(l.marks)
	if marks == nil {
		marks = make(map[string]map[string][]Mark)
	}
	cloned := make(map[string]bool)
	changed := make(map[[2]string]bool)
	for _, e := range entries {
		sessions := marks[e.Agent]
		if !cloned[e.Agent] {
			sessions = maps.Clone(sessions)
			if sessions == nil {
				sessions = make(map[string][]Mark)
			}
			marks[e.Agent] = sessions
			cloned[e.Agent] = true
		}
		key := [2]string{e.Agent, e.Session}
		if !changed[key] {
			sessions[e.Session] = slices.Clip(sessions[e.Session])
			changed[key] = true
		}
		sessions[e.Session] = append(sessions[e.Session], Mark{At: e.At.UTC(), Provider: e.Provider})
	}
	for key := range changed {
		// Appended by processes and goroutines in turn, not quite in the
		// order of their times.
		slices.SortStableFunc(marks[key[0]][key[1]], func(a, b Mark) int { return a.At.Compare(b.At) })
	}
	l.marks = marks
	l.version++
}

// At returns the provider marks say a session ran on at t: the latest
// mark's at or before t, "" (the agent's own) before the first.
func At(marks []Mark, t time.Time) string {
	i, _ := slices.BinarySearchFunc(marks, t, func(m Mark, t time.Time) int {
		if m.At.After(t) {
			return 1
		}
		return -1
	})
	if i == 0 {
		return ""
	}
	return marks[i-1].Provider
}
