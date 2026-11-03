package shellterm

import (
	"bytes"
	"testing"
)

// BenchmarkScrollbackFull appends 4 KiB of output, in lines, to a full
// 256 KiB scrollback; it used to copy the whole scrollback every time.
func BenchmarkScrollbackFull(b *testing.B) {
	term := New(Config{})
	chunk := bytes.Repeat([]byte("0123456789012345678901234567890123456789012345678901234567890123456789012345678\n"), 4096/80)
	for range 2 * defaultScrollback / len(chunk) {
		term.appendLocked(chunk)
	}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		term.appendLocked(chunk)
	}
}
