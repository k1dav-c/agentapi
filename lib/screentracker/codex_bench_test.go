package screentracker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coder/agentapi/lib/msgfmt"
)

// buildCodexScreen constructs a realistic Codex terminal screen of the given
// height. It includes a header, user input echo, agent response body, and a
// status bar with a dynamic spinner counter — the combination that causes
// format-cache misses on every tick.
func buildCodexScreen(rows int, spinnerSec int, userInput string) string {
	lines := make([]string, 0, rows)

	// Header (2 lines)
	lines = append(lines,
		">_ You are using OpenAI Codex in ~/project",
		"",
	)

	// Echoed user input (2 lines)
	lines = append(lines,
		"user",
		userInput,
	)
	lines = append(lines, "")

	// Agent response body — fill most of the screen
	bodyLines := rows - 7 // header(2) + user(3) + status(2)
	if bodyLines < 1 {
		bodyLines = 1
	}
	for i := range bodyLines {
		lines = append(lines, fmt.Sprintf("Agent response line %d: explaining some concept with detail", i))
	}

	// Status bar (2 lines)
	lines = append(lines,
		fmt.Sprintf("▌ • Working (%ds • Ctrl C to interrupt)", spinnerSec),
		"gpt-5.1-codex-max default · 95%% left · ~/project",
	)

	return strings.Join(lines, "\n")
}

// BenchmarkCodexPipeline benchmarks the full per-tick pipeline that runs on
// every format-cache miss: screenDiff → FormatAgentMessage → trimPreviousMessageOverlap.
// This is the critical path for Codex because its spinner counter changes
// every ~1 second, invalidating the cache.
func BenchmarkCodexPipeline(b *testing.B) {
	const userInput = "Explain the theory of relativity in detail"

	for _, rows := range []int{50, 200, 700} {
		b.Run(fmt.Sprintf("%d_rows", rows), func(b *testing.B) {
			// Old screen: baseline snapshot taken before user message.
			// Just a welcome screen with some prior content.
			oldLines := make([]string, rows)
			for i := range oldLines {
				oldLines[i] = fmt.Sprintf("previous session line %d with filler content here", i)
			}
			oldScreen := strings.Join(oldLines, "\n")

			// New screen: Codex response with spinner at 5 seconds.
			newScreen := buildCodexScreen(rows, 5, userInput)

			// Previous turn agent message for overlap detection.
			prevMsg := strings.Repeat("Previous turn content line with details\n", 20)

			b.ReportAllocs()
			for b.Loop() {
				diff := screenDiff(oldScreen, newScreen, msgfmt.AgentTypeCodex)
				formatted := msgfmt.FormatAgentMessage(msgfmt.AgentTypeCodex, diff, userInput)
				trimPreviousMessageOverlap(prevMsg, formatted)
			}
		})
	}
}

// BenchmarkCodexSpinnerInvalidation measures the cost difference between
// a cache hit (same screen) and a cache miss (spinner counter changed).
// In production, every tick is a miss because the counter increments.
func BenchmarkCodexSpinnerInvalidation(b *testing.B) {
	const userInput = "Build a snake game"
	const rows = 200

	oldLines := make([]string, rows)
	for i := range oldLines {
		oldLines[i] = fmt.Sprintf("baseline line %d", i)
	}
	oldScreen := strings.Join(oldLines, "\n")
	prevMsg := strings.Repeat("Prior turn output\n", 10)

	b.Run("cache_hit", func(b *testing.B) {
		screen := buildCodexScreen(rows, 5, userInput)
		b.ReportAllocs()
		for b.Loop() {
			diff := screenDiff(oldScreen, screen, msgfmt.AgentTypeCodex)
			formatted := msgfmt.FormatAgentMessage(msgfmt.AgentTypeCodex, diff, userInput)
			trimPreviousMessageOverlap(prevMsg, formatted)
		}
	})

	b.Run("cache_miss_spinner_change", func(b *testing.B) {
		// Alternate between two spinner values to simulate the counter
		// changing every tick, which is what happens in production.
		screenA := buildCodexScreen(rows, 5, userInput)
		screenB := buildCodexScreen(rows, 6, userInput)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			screen := screenA
			if i%2 == 1 {
				screen = screenB
			}
			diff := screenDiff(oldScreen, screen, msgfmt.AgentTypeCodex)
			formatted := msgfmt.FormatAgentMessage(msgfmt.AgentTypeCodex, diff, userInput)
			trimPreviousMessageOverlap(prevMsg, formatted)
			i++
		}
	})
}
