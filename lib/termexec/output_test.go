package termexec

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/ActiveState/termtest/xpty"
	"github.com/ActiveState/vt10x"
	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runeRecorder records the runes written to it.
type runeRecorder struct{ runes []rune }

func (r *runeRecorder) WriteRune(c rune) { r.runes = append(r.runes, c) }

func TestWriteOutput(t *testing.T) {
	t.Parallel()
	var rec runeRecorder
	injector := &wideCharInjector{}
	data := []byte("a你")
	used := writeOutput(&rec, injector, data[:2], false)
	assert.Equal(t, 1, used, "an incomplete sequence waits for more output")
	assert.Equal(t, []rune{'a'}, rec.runes)
	used = writeOutput(&rec, injector, data[used:], false)
	assert.Equal(t, 3, used)
	assert.Equal(t, []rune{'a', '你', widePadRune}, rec.runes, "a wide rune is padded")

	rec.runes = nil
	used = writeOutput(&rec, injector, []byte{'x', 0xff, 'y', 0xe4}, true)
	assert.Equal(t, 4, used, "the last chunk is used whole")
	assert.Equal(t, []rune{'x', '�', 'y', '�'}, rec.runes, "bytes that aren't UTF-8 become U+FFFD")
}

// chunks hands out its parts one Read at a time.
type chunks struct{ parts []string }

func (c *chunks) Read(p []byte) (int, error) {
	if len(c.parts) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.parts[0])
	c.parts[0] = c.parts[0][n:]
	if c.parts[0] == "" {
		c.parts = c.parts[1:]
	}
	return n, nil
}

func newTestProcess(t testing.TB, cols, rows int) (*Process, *vt10x.State) {
	t.Helper()
	var state vt10x.State
	term, err := vt10x.Create(&state, nopRWC{})
	require.NoError(t, err)
	term.Resize(cols, rows)
	return &Process{xp: &xpty.Xpty{Term: term}, screenUpdates: make(chan struct{}, 1), clock: quartz.NewReal()}, &state
}

// The terminal shows the same whether output comes in one chunk or with
// runes split across reads.
func TestReadOutput(t *testing.T) {
	t.Parallel()
	text := "hello 你好\r\ncafé 😀 done"
	p, state := newTestProcess(t, 40, 5)
	assert.ErrorIs(t, p.readOutput(&chunks{parts: splitEvery(text, 1)}), io.EOF)
	split := firstLines(state)

	q, whole := newTestProcess(t, 40, 5)
	assert.ErrorIs(t, q.readOutput(strings.NewReader(text)), io.EOF)
	assert.Equal(t, firstLines(whole), split)
	assert.Contains(t, split, "hello 你好")
	assert.Contains(t, split, "café 😀 done")
}

func splitEvery(s string, n int) []string {
	b := []byte(s)
	var out []string
	for len(b) > 0 {
		k := min(n, len(b))
		out = append(out, string(b[:k]))
		b = b[k:]
	}
	return out
}

func firstLines(state *vt10x.State) string {
	return strings.TrimRight(stripWidePadding(state.String()), " \n")
}

var benchOutput = strings.Repeat("Agent output with some text 你好, and a bit more of it\r\n", 1400)

// BenchmarkOutput compares xpty's ReadRune, a goroutine, two channels and
// a timer per rune, with reading the output in chunks.
func BenchmarkOutput(b *testing.B) {
	b.Logf("%d KB of output", len(benchOutput)>>10)
	b.Run("per rune", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p, _ := newTestProcess(b, 80, 1000)
			pp := xpty.NewPassthroughPipe(bufio.NewReaderSize(strings.NewReader(benchOutput), 100))
			injector := &wideCharInjector{}
			for {
				r, _, err := pp.ReadRune()
				if err != nil {
					break
				}
				p.xp.Term.WriteRune(r)
				if injector.shouldPad(r) {
					p.xp.Term.WriteRune(widePadRune)
				}
			}
		}
	})
	b.Run("chunks", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p, _ := newTestProcess(b, 80, 1000)
			_ = p.readOutput(bufio.NewReaderSize(strings.NewReader(benchOutput), 100))
		}
	})
}
