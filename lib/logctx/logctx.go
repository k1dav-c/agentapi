package logctx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type contextKey int

const (
	loggerKey contextKey = iota
)

// WithLogger returns a new context with the provided logger
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// From retrieves the logger from the context or panics if no logger is found
func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return logger
	}
	panic("no logger found in context")
}

// LevelNames are the levels ParseLevel takes, for flag help and errors.
const LevelNames = "debug, info, warn, error"

// ParseLevel returns the level a --log-level names: debug, info, warn or
// error, in any case. Empty is info.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (one of: %s)", name, LevelNames)
}

// plucked from log/slog
// remove once we update to go 1.24
var DiscardHandler slog.Handler = discardHandler{}

type discardHandler struct{}

func (dh discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (dh discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (dh discardHandler) WithAttrs(attrs []slog.Attr) slog.Handler  { return dh }
func (dh discardHandler) WithGroup(name string) slog.Handler        { return dh }
