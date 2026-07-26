package termexec

import (
	"strings"
	"testing"

	"github.com/ActiveState/vt10x"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nopRWC struct{}

func (nopRWC) Read(p []byte) (int, error)  { return 0, nil }
func (nopRWC) Write(p []byte) (int, error) { return len(p), nil }
func (nopRWC) Close() error                { return nil }

// newTestTerm creates a vt10x terminal and a feed function that mirrors
// the termexec read loop: every rune is written to the terminal and
// followed by widePadRune when the injector asks for it.
func newTestTerm(t *testing.T, cols, rows int) (*vt10x.State, func(string)) {
	t.Helper()
	var state vt10x.State
	term, err := vt10x.Create(&state, nopRWC{})
	require.NoError(t, err)
	term.Resize(cols, rows)

	injector := &wideCharInjector{}
	feed := func(s string) {
		for _, r := range s {
			term.WriteRune(r)
			if injector.shouldPad(r) {
				term.WriteRune(widePadRune)
			}
		}
	}
	return &state, feed
}

func firstLine(state *vt10x.State) string {
	screen := stripWidePadding(state.String())
	return strings.TrimRight(strings.Split(screen, "\n")[0], " ")
}

func TestWideCharInjector_AbsolutePositioningAfterCJK(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)

	// The application writes 4 CJK runes (8 display columns) and then
	// positions the cursor at column 9 (1-based) to continue the line,
	// as a wide-aware renderer would.
	feed("你好世界")
	feed("\033[1;9H")
	feed("X")

	assert.Equal(t, "你好世界X", firstLine(state))
}

func TestWideCharInjector_WithoutPaddingWouldMisalign(t *testing.T) {
	// Sanity check documenting the vt10x behavior this fix works around:
	// without padding, the same sequence leaves a 4-cell gap.
	var state vt10x.State
	term, err := vt10x.Create(&state, nopRWC{})
	require.NoError(t, err)
	term.Resize(80, 24)
	for _, r := range "你好世界\033[1;9HX" {
		term.WriteRune(r)
	}
	line := strings.TrimRight(strings.Split(state.String(), "\n")[0], " ")
	assert.Equal(t, "你好世界    X", line)
}

func TestWideCharInjector_LinearMixedTextUnchanged(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)

	feed("中文 and ascii 混合 text")

	assert.Equal(t, "中文 and ascii 混合 text", firstLine(state))
}

func TestWideCharInjector_NoPaddingInsideEscapeSequences(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)

	// Wide runes inside an OSC title string must not trigger padding.
	feed("\033]0;标题\a")
	feed("你")
	feed("\033[1;3H")
	feed("!")

	assert.Equal(t, "你!", firstLine(state))
}

func TestWideCharInjector_CSIWithControlCodesInside(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)

	// SGR sequences interleaved with CJK text.
	feed("\033[1m你\033[0m好")
	feed("\033[1;5H")
	feed("末")

	assert.Equal(t, "你好末", firstLine(state))
}

func TestWideCharInjector_CarriageReturnOverwrite(t *testing.T) {
	state, feed := newTestTerm(t, 80, 24)

	// Overwrite a wide line from the start; leftover padding from the
	// old content must not corrupt the result.
	feed("狀態更新中")
	feed("\r")
	feed("完成！")
	feed("\033[1;7H")
	feed("ok")

	assert.Equal(t, "完成！ok中", firstLine(state))
}

func TestStripWidePadding(t *testing.T) {
	assert.Equal(t, "你好", stripWidePadding("你好"))
	assert.Equal(t, "plain", stripWidePadding("plain"))
}
