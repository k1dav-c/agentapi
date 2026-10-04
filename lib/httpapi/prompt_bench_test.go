package httpapi

import (
	"fmt"
	"strings"
	"testing"

	mf "github.com/coder/agentapi/lib/msgfmt"
)

// EmitScreen checks every screen change for an interactive prompt, so its
// cost matters while an agent streams output. Screens are 80x1000 like
// AgentAPI's default terminal.

// benchIdleScreen is a Claude screen with its input box at the bottom.
func benchIdleScreen(frame int) string {
	lines := make([]string, 0, 1000)
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("● frame %d line %d %s", frame, i, strings.Repeat("abc測試 ", 8)))
	}
	for len(lines) < 996 {
		lines = append(lines, strings.Repeat(" ", 80))
	}
	lines = append(lines, strings.Repeat("─", 80), "❯ ", strings.Repeat("─", 80), "  ⏵⏵ bypass permissions on")
	return strings.Join(lines, "\n")
}

// benchBusyScreen is streaming output with no input box in view, the case
// where every prompt check has to run.
func benchBusyScreen(frame int) string {
	lines := make([]string, 0, 1000)
	for i := 0; i < 1000; i++ {
		lines = append(lines, fmt.Sprintf("frame %04d row %02d %s", frame, i, strings.Repeat("abcdef012345 ", 4)))
	}
	return strings.Join(lines, "\n")
}

func benchmarkEmitScreen(b *testing.B, screen func(int) string) {
	e := NewEventEmitter(WithAgentType(mf.AgentTypeClaude))
	screens := []string{screen(0), screen(1)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.EmitScreen(screens[i%2])
	}
}

func BenchmarkEmitScreenIdle(b *testing.B) { benchmarkEmitScreen(b, benchIdleScreen) }
func BenchmarkEmitScreenBusy(b *testing.B) { benchmarkEmitScreen(b, benchBusyScreen) }
