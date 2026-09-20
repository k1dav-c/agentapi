package termexec

import (
	"strings"
	"testing"

	"github.com/ActiveState/vt10x"
	"github.com/stretchr/testify/require"
)

func TestRenderScreenMatchesTerminal(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)
	for _, input := range []string{"", "hello\r\nworld", "\r\n你好 café 😀", "\x1b[2J\x1b[Hred\x1b[31m text\x1b[0m", strings.Repeat("scroll\r\n", 30), "\x1b[?1049halternate", "\x1b[?1049l"} {
		feed(input)
		// renderScreen emits rows 0..max(cursorY, lastContentRow),
		// so compare the prefix of the full terminal output.
		got := renderScreen(state)
		full := stripWidePadding(state.String())
		require.Equal(t, full[:len(got)], got)
	}
}

func TestRenderScreenContentBelowCursor(t *testing.T) {
	// Simulate TUI agents that write content below the cursor position
	// (e.g. Claude Code's Ink framework using absolute cursor movement).
	state, feed := newTestTerm(t, 80, 24)
	// Write text on row 0, then move cursor to row 5 and write, then
	// move cursor back to row 0. Content exists on row 5 but cursor is
	// at row 0.
	feed("top line")
	feed("\x1b[6;1Hbottom line") // move to row 6, col 1
	feed("\x1b[1;1H")             // move cursor back to row 1, col 1

	got := renderScreen(state)
	require.Contains(t, got, "top line", "should include content at cursor row")
	require.Contains(t, got, "bottom line", "should include content below cursor")
}

var renderedBenchmark string

func BenchmarkRenderScreen(b *testing.B) {
	for _, tc := range []struct {
		name string
		rows int
	}{
		{"700rows", 700},
		{"50rows", 50},
	} {
		b.Run(tc.name, func(b *testing.B) {
			var state vt10x.State
			term, err := vt10x.Create(&state, nopRWC{})
			if err != nil {
				b.Fatal(err)
			}
			term.Resize(80, 1000)
			for _, r := range strings.Repeat("Agent output with some text 你好\r\n", tc.rows) {
				term.WriteRune(r)
			}
			b.Run("previous", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					renderedBenchmark = stripWidePadding(state.String())
				}
			})
			b.Run("direct", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					renderedBenchmark = renderScreen(&state)
				}
			})
			b.Run("hwm-cached", func(b *testing.B) {
				hwm := 0
				// Prime the high-water mark with one render.
				renderScreenWithHWM(&state, &hwm)
				b.ReportAllocs()
				for b.Loop() {
					renderedBenchmark = renderScreenWithHWM(&state, &hwm)
				}
			})
		})
	}
}
