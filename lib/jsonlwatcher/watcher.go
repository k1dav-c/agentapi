package jsonlwatcher

import (
	"context"
	"errors"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/transcript"
)

const (
	// pollInterval is how often we check for new content in the JSONL file.
	pollInterval = 200 * time.Millisecond

	// sessionPollInterval is how often we check for the session/JSONL file to appear.
	sessionPollInterval = 500 * time.Millisecond
)

// Config for creating a new Watcher.
type Config struct {
	Resolver agentlog.SessionResolver
	Parser   agentlog.LineParser
	// Format, if set, is the log's: the watcher logs once per log what it
	// holds that the format doesn't know.
	Format    agentlog.Format
	Logger    *slog.Logger
	OnMessage func(transcript.RichMessage) // called when a message is completed or updated
	OnLine    func([]byte)                 // called for each complete raw JSONL record
	// Clock paces the polling; the real clock when nil.
	Clock quartz.Clock
}

// Watcher tails a JSONL session file and assembles structured messages
// by delegating line parsing to an agent-specific LineParser. Completed
// messages are handed to the OnMessage callback; the watcher itself keeps
// no message state.
type Watcher struct {
	resolver  agentlog.SessionResolver
	parser    agentlog.LineParser
	format    agentlog.Format
	logger    *slog.Logger
	onMessage func(transcript.RichMessage)
	onLine    func([]byte)
	clock     quartz.Clock
}

// New creates a Watcher but does not start it.
func New(cfg Config) *Watcher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = quartz.NewReal()
	}
	return &Watcher{
		resolver:  cfg.Resolver,
		parser:    cfg.Parser,
		format:    cfg.Format,
		logger:    logger,
		onMessage: cfg.OnMessage,
		onLine:    cfg.OnLine,
		clock:     clock,
	}
}

// Start begins tailing the JSONL file. It blocks until ctx is canceled.
// It first uses the resolver to find the file, then enters a tail loop.
func (w *Watcher) Start(ctx context.Context) {
	jsonlPath, err := w.waitForSessionFile(ctx)
	if err != nil {
		// Only fails when ctx is canceled.
		return
	}
	for ctx.Err() == nil {
		w.logger.Info("Resolved JSONL path", "path", jsonlPath)
		nextPath := w.tailFile(ctx, jsonlPath)
		if nextPath == "" {
			return
		}
		// A resolver may move to a different session file while the agent is
		// running (Claude session parking does this). Finish the old parser
		// state before reading the replacement file from its beginning.
		w.emit(w.parser.Flush())
		jsonlPath = nextPath
	}
}

// waitForSessionFile polls until the resolver can find the JSONL file or
// ctx is canceled. There is deliberately no timeout: the session file may
// only be created when the user sends their first message, which can be
// arbitrarily long after the agent starts.
func (w *Watcher) waitForSessionFile(ctx context.Context) (string, error) {
	ticker := w.clock.NewTicker(sessionPollInterval, "Watcher", "resolve")
	defer ticker.Stop()

	for {
		path, err := w.resolver.Resolve()
		if err == nil {
			return path, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			// retry
		}
	}
}

// tailFile reads the JSONL file's lines as they're appended, through an
// agentlog.Follower: only complete lines, a line too long skipped, and the
// file from its start again once it shrank or was replaced. It returns
// the path to follow next when the resolver moves to another file, or ""
// once ctx is canceled.
func (w *Watcher) tailFile(ctx context.Context, path string) string {
	if next, ok := w.waitForFile(ctx, path); !ok {
		return next
	}

	w.logger.Info("Opened JSONL file for tailing", "path", path)

	var drift *agentlog.Drift
	if w.format != nil {
		drift = agentlog.NewDrift(w.format)
	}
	tail := &tailReader{w: w, drift: drift}
	readers := 0
	follower := agentlog.NewFollower(path, func(string) *tailReader {
		if readers++; readers > 1 {
			// Read again from its start: what the parser holds is of the
			// file as it was.
			w.logger.Info("JSONL file was truncated or replaced; reading it again", "path", path)
			w.emit(w.parser.Flush())
			tail.again()
		}
		return tail
	})
	pollTicker := w.clock.NewTicker(pollInterval, "Watcher", "poll")
	defer pollTicker.Stop()
	resolveTicker := w.clock.NewTicker(sessionPollInterval, "Watcher", "resolve")
	defer resolveTicker.Stop()

	failed := ""
	for {
		if err := follower.Read(); err != nil {
			// Logged once until a read succeeds; the next poll tries again.
			if err.Error() != failed {
				w.logger.Warn("Failed to read JSONL file; retrying", "path", path, "error", err)
				failed = err.Error()
			}
		} else {
			failed = ""
		}
		if drift != nil {
			drift.Report(w.logger, path)
		}

		// Flush only completed messages (those with a terminal stop_reason).
		// Without this, the last assistant message (with stop_reason="end_turn")
		// stays in the parser's pending state indefinitely because no subsequent
		// JSONL line arrives to trigger finalization. We use FlushCompleted
		// instead of Flush to avoid emitting partial messages that are still
		// being assembled across multiple JSONL lines.
		w.emit(w.parser.FlushCompleted())

		select {
		case <-ctx.Done():
			// Stopped: the session is over (a restart, or AgentAPI exits),
			// and what the parser still holds would land in whatever
			// conversation follows it.
			return ""
		case <-pollTicker.C:
			// continue reading
		case <-resolveTicker.C:
			resolvedPath, err := w.resolver.Resolve()
			if err == nil && resolvedPath != path {
				w.logger.Info("JSONL session path changed", "old_path", path, "new_path", resolvedPath)
				return resolvedPath
			}
		}
	}
}

// waitForFile polls until the file at path exists, and reports whether it
// does. It doesn't when ctx is canceled (next is "") or the resolver moved
// to another file before this one was created (next is that file).
func (w *Watcher) waitForFile(ctx context.Context, path string) (next string, ok bool) {
	ticker := w.clock.NewTicker(sessionPollInterval, "Watcher", "open")
	defer ticker.Stop()
	var failed string
	for {
		_, err := os.Stat(path)
		if err == nil {
			return "", true
		}
		if !errors.Is(err, fs.ErrNotExist) && err.Error() != failed {
			// It may go away: keep trying.
			w.logger.Error("Failed to open JSONL file", "path", path, "error", err)
			failed = err.Error()
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-ticker.C:
			// The file may never appear: Claude Code can switch to another
			// session (e.g. /clear, or a session id that is replaced before
			// anything is written) without ever creating the one it
			// advertised first. Follow the resolver instead of waiting on
			// a path that is no longer current.
			if resolvedPath, err := w.resolver.Resolve(); err == nil && resolvedPath != path {
				w.logger.Info("JSONL session path changed before the file was created", "old_path", path, "new_path", resolvedPath)
				return resolvedPath, false
			}
		}
	}
}

// tailReader hands the lines of the file a watcher follows to it. Read
// again from its start, the file's lines that were read before go to the
// parser alone: its messages are merged downstream by ID, but OnLine's
// timeline events would come twice.
type tailReader struct {
	w     *Watcher
	drift *agentlog.Drift
	// read holds the hashes of the lines read, before those of the reading
	// before the file was read again.
	read   []uint64
	before map[uint64]struct{}
}

// again starts another reading of the file.
func (t *tailReader) again() {
	t.before = make(map[uint64]struct{}, len(t.read))
	for _, sum := range t.read {
		t.before[sum] = struct{}{}
	}
	t.read = t.read[:0]
}

// Line reads one complete line.
func (t *tailReader) Line(line []byte) bool {
	h := fnv.New64a()
	_, _ = h.Write(line)
	sum := h.Sum64()
	t.read = append(t.read, sum)
	if _, ok := t.before[sum]; ok {
		t.w.parseLine(line)
		return true
	}
	if t.drift != nil {
		t.drift.Line(line)
	}
	t.w.processLine(line)
	return true
}

// processLine delegates parsing to the LineParser and handles completed messages.
func (w *Watcher) processLine(line []byte) {
	if w.onLine != nil {
		w.onLine(line)
	}
	w.parseLine(line)
}

// parseLine hands a line to the parser, and emits the messages it completed.
func (w *Watcher) parseLine(line []byte) {
	completed, err := w.parser.ParseLine(line)
	if err != nil {
		w.logger.Debug("Failed to parse JSONL line", "error", err, "line", string(line[:min(len(line), 100)]))
		return
	}

	w.emit(completed)
}

// emit hands completed messages to the OnMessage callback. Parsers may
// re-emit the same message as its content accumulates (e.g. the Codex
// parser emits a turn update per content block); deduplication by
// (MessageID, Role) happens downstream in the EventEmitter.
func (w *Watcher) emit(msgs []transcript.RichMessage) {
	if w.onMessage == nil {
		return
	}
	for _, msg := range msgs {
		w.onMessage(msg)
	}
}
