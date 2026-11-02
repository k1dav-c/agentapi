package logctx_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/logctx"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"":      slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"DEBUG": slog.LevelDebug,
		" warn": slog.LevelWarn,
	} {
		got, err := logctx.ParseLevel(name)
		require.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}
	for _, name := range []string{"trace", "warning", "info+2", "verbose"} {
		_, err := logctx.ParseLevel(name)
		assert.ErrorContains(t, err, `unknown log level "`+name+`" (one of: debug, info, warn, error)`)
	}
}
