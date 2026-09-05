package jsonlwatcher

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

const (
	// pollInterval is how often we check for new content in the JSONL file.
	pollInterval = 200 * time.Millisecond

	// sessionPollInterval is how often we check for the session/JSONL file to appear.
	sessionPollInterval = 500 * time.Millisecond
)

// Config for creating a new Watcher.
type Config struct {
	Resolver  SessionResolver
	Parser    LineParser
	Logger    *slog.Logger
	OnMessage func(RichMessage) // called when a message is completed or updated
	OnLine    func([]byte)      // called for each complete raw JSONL record
}

// Watcher tails a JSONL session file and assembles structured messages
// by delegating line parsing to an agent-specific LineParser. Completed
// messages are handed to the OnMessage callback; the watcher itself keeps
// no message state.
type Watcher struct {
	resolver  SessionResolver
	parser    LineParser
	logger    *slog.Logger
	onMessage func(RichMessage)
	onLine    func([]byte)
}

// New creates a Watcher but does not start it.
func New(cfg Config) *Watcher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		resolver:  cfg.Resolver,
		parser:    cfg.Parser,
		logger:    logger,
		onMessage: cfg.OnMessage,
		onLine:    cfg.OnLine,
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
	ticker := time.NewTicker(sessionPollInterval)
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
	ticker := time.NewTicker(sessionPollInterval)
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
			// retry
		}
	}
	defer f.Close()
	ticker.Stop()

	w.logger.Info("Opened JSONL file for tailing", "path", path)

	reader := bufio.NewReader(f)
	pollTicker := time.NewTicker(pollInterval)
	defer pollTicker.Stop()
	resolveTicker := time.NewTicker(sessionPollInterval)
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
			w.processLine(line)
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
			// Flush all pending messages on shutdown, even incomplete ones.
			w.emit(w.parser.Flush())
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
func (w *Watcher) emit(msgs []RichMessage) {
	if w.onMessage == nil {
		return
	}
	for _, msg := range msgs {
		w.onMessage(msg)
	}
}
