package msgfmt

import (
	"strings"
	"testing"
)

func BenchmarkNormalizeAndGetRuneLineMapping(b *testing.B) {
	// Simulate a ~200-line screen of mixed ASCII and CJK content,
	// representative of Codex output with status lines.
	content := strings.Repeat("Agent output line with some text 你好世界\n", 200)
	b.ReportAllocs()
	for b.Loop() {
		normalizeAndGetRuneLineMapping(content)
	}
}
