package termexec

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// widePadRune is a private-use rune injected into the terminal emulator
// after every double-width rune, and stripped from ReadScreen output.
//
// The vt10x emulator advances the cursor by exactly one column per rune,
// but terminal applications (e.g. Claude Code) lay out text treating
// double-width runes (CJK, some symbols) as occupying two columns and
// reposition the cursor with absolute escape sequences. The mismatch
// makes repositioned writes land too far right, leaving spurious gaps in
// the middle of lines. Injecting a one-column padding rune after each
// double-width rune keeps the emulator's cursor in sync with the
// application's column math, mirroring how real terminals dedicate two
// cells to a wide glyph.
const widePadRune = '\uE000'

// escState tracks vt10x's escape sequence parser states. The injector
// mirrors the state machine in vt10x's parse.go so that padding is only
// injected for runes that are actually printed to the grid (ground
// state), never for bytes that are part of an escape sequence.
type escState int

const (
	escGround  escState = iota
	escEsc              // after ESC
	escCSI              // after ESC [
	escStr              // OSC/DCS/APC/PM string payload
	escStrEnd           // after ESC inside a string payload
	escOneChar          // ESC # or ESC ( : consumes exactly one more rune
)

// wideCharInjector decides whether a rune written to the terminal should
// be followed by a widePadRune.
type wideCharInjector struct {
	state  escState
	csiLen int
}

// shouldPad advances the parser state with r and reports whether r was a
// double-width rune printed in ground state.
func (w *wideCharInjector) shouldPad(r rune) bool {
	switch w.state {
	case escGround:
		if isControlCode(r) {
			w.handleControlCode(r)
			return false
		}
		return runewidth.RuneWidth(r) == 2

	case escEsc:
		if isControlCode(r) {
			w.handleControlCode(r)
			return false
		}
		switch r {
		case '[':
			w.state = escCSI
			w.csiLen = 0
		case 'P', '_', '^', ']', 'k':
			w.state = escStr
		case '#', '(':
			w.state = escOneChar
		default:
			// Includes ')', '*', '+' which vt10x treats as complete
			// sequences (the designator char is processed in ground
			// state), and all single-char sequences (D, E, M, 7, 8, ...).
			w.state = escGround
		}

	case escCSI:
		if isControlCode(r) {
			w.handleControlCode(r)
			return false
		}
		w.csiLen++
		if (r >= 0x40 && r <= 0x7E) || w.csiLen >= 256 {
			w.state = escGround
		}

	case escStr:
		switch r {
		case '\033':
			w.state = escStrEnd
		case '\a':
			w.state = escGround
		}

	case escStrEnd:
		if isControlCode(r) {
			w.handleControlCode(r)
			return false
		}
		w.state = escGround

	case escOneChar:
		if isControlCode(r) {
			w.handleControlCode(r)
			return false
		}
		w.state = escGround
	}
	return false
}

// handleControlCode mirrors vt10x's handleControlCodes: ESC switches to
// the escape state from any state where control codes are interpreted;
// all other control codes leave the parser state unchanged.
func (w *wideCharInjector) handleControlCode(r rune) {
	if r == '\033' {
		w.state = escEsc
	}
}

// isControlCode matches vt10x's definition.
func isControlCode(r rune) bool {
	return r < 0x20 || r == 0177
}

// stripWidePadding removes the injected padding runes from a screen
// snapshot before it's handed to consumers.
func stripWidePadding(screen string) string {
	return strings.ReplaceAll(screen, string(widePadRune), "")
}
