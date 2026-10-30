package nativetest

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
)

// Log records the warnings an adapter logs, for tests that check them.
type Log struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// NewLog returns a logger whose warnings and errors the Log records.
func NewLog() (*slog.Logger, *Log) {
	l := &Log{}
	return slog.New(slog.NewTextHandler(l, &slog.HandlerOptions{Level: slog.LevelWarn})), l
}

func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// Warnings returns the records logged so far, one line each.
func (l *Log) Warnings() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := strings.TrimSpace(l.buf.String())
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// Matching returns the warnings that contain every one of parts.
func (l *Log) Matching(parts ...string) []string {
	var out []string
	for _, w := range l.Warnings() {
		match := true
		for _, p := range parts {
			match = match && strings.Contains(w, p)
		}
		if match {
			out = append(out, w)
		}
	}
	return out
}
