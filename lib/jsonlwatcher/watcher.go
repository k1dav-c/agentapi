package jsonlwatcher

import (
	"bufio"
	"context"
	"io"
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

// tailFile opens the JSONL file and reads new lines as they're appended.
func (w *Watcher) tailFile(ctx context.Context, path string) string {
	// Wait for the file to exist
	var f *os.File
	ticker := w.clock.NewTicker(sessionPollInterval, "Watcher", "open")
	defer ticker.Stop()

	for {
		var err error
		f, err = os.Open(path)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			w.logger.Error("Failed to open JSONL file", "path", path, "error", err)
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-ticker.C:
			// The file may never appear: Claude Code can switch to another
			// session (e.g. /clear, or a session id that is replaced before
			// anything is written) without ever creating the one it
			// advertised first. Follow the resolver instead of waiting on
			// a path that is no longer current.
			if resolvedPath, err := w.resolver.Resolve(); err == nil && resolvedPath != path {
				w.logger.Info("JSONL session path changed before the file was created", "old_path", path, "new_path", resolvedPath)
				return resolvedPath
			}
		}
	}
	defer func() { _ = f.Close() }()
	ticker.Stop()

	w.logger.Info("Opened JSONL file for tailing", "path", path)

	reader := bufio.NewReader(f)
	var drift *agentlog.Drift
	if w.format != nil {
		drift = agentlog.NewDrift(w.format)
	}
	pollTicker := w.clock.NewTicker(pollInterval, "Watcher", "poll")
	defer pollTicker.Stop()
	resolveTicker := w.clock.NewTicker(sessionPollInterval, "Watcher", "resolve")
	defer resolveTicker.Stop()

	for {
		// Read all available complete lines
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					// Partial line or no more data; wait for more
					if len(line) > 0 {
						if _, seekErr := f.Seek(-int64(len(line)), io.SeekCurrent); seekErr != nil {
							w.logger.Error("Failed to seek back for partial line", "error", seekErr)
						}
						reader.Reset(f)
					}
					break
				}
				w.logger.Error("Error reading JSONL file", "error", err)
				return ""
			}
			if drift != nil {
				drift.Line(line)
			}
			w.processLine(line)
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

// processLine delegates parsing to the LineParser and handles completed messages.
func (w *Watcher) processLine(line []byte) {
	if w.onLine != nil {
		w.onLine(line)
	}
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
