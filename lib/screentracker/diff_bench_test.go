package screentracker

import (
	"strings"
	"testing"

	"github.com/coder/agentapi/lib/msgfmt"
)

func BenchmarkScreenDiff(b *testing.B) {
	// Simulate a typical terminal: 80 columns, 24 rows.
	buildScreen := func(lineCount int, prefix string) string {
		lines := make([]string, lineCount)
		for i := range lines {
			lines[i] = prefix + strings.Repeat("x", 76-len(prefix))
		}
		return strings.Join(lines, "\n")
	}

	b.Run("small/no_change", func(b *testing.B) {
		screen := buildScreen(24, "line")
		for b.Loop() {
			screenDiff(screen, screen, msgfmt.AgentTypeCustom)
		}
	})

	b.Run("small/half_changed", func(b *testing.B) {
		old := buildScreen(24, "old-")
		new_ := buildScreen(12, "old-") + "\n" + buildScreen(12, "new-")
		for b.Loop() {
			screenDiff(old, new_, msgfmt.AgentTypeCustom)
		}
	})

	b.Run("large/200_lines", func(b *testing.B) {
		old := buildScreen(200, "prev-")
		new_ := buildScreen(100, "prev-") + "\n" + buildScreen(100, "resp-")
		for b.Loop() {
			screenDiff(old, new_, msgfmt.AgentTypeCustom)
		}
	})
}

func BenchmarkTrimPreviousMessageOverlap(b *testing.B) {
	makeParagraph := func(lineCount int, prefix string) string {
		lines := make([]string, lineCount)
		for i := range lines {
			lines[i] = prefix + strings.Repeat("a", 70)
		}
		return strings.Join(lines, "\n")
	}

	b.Run("no_overlap/short", func(b *testing.B) {
		prev := "previous message"
		new_ := "new content"
		for b.Loop() {
			trimPreviousMessageOverlap(prev, new_)
		}
	})

	b.Run("overlap_5_lines", func(b *testing.B) {
		shared := makeParagraph(5, "shared-")
		prev := makeParagraph(20, "old-") + "\n" + shared
		new_ := shared + "\n" + makeParagraph(15, "new-")
		for b.Loop() {
			trimPreviousMessageOverlap(prev, new_)
		}
	})

	b.Run("overlap_50_lines", func(b *testing.B) {
		shared := makeParagraph(50, "shared-")
		prev := makeParagraph(100, "old-") + "\n" + shared
		new_ := shared + "\n" + makeParagraph(50, "new-")
		for b.Loop() {
			trimPreviousMessageOverlap(prev, new_)
		}
	})
}
