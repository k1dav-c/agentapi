package termexec

import (
	"strings"
	"testing"
)

func BenchmarkWideCharInjector(b *testing.B) {
	b.Run("ascii_only", func(b *testing.B) {
		// Simulate 24 lines of pure ASCII terminal output.
		text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 24)
		runes := []rune(text)
		for b.Loop() {
			inj := &wideCharInjector{}
			for _, r := range runes {
				inj.shouldPad(r)
			}
		}
	})

	b.Run("mixed_cjk", func(b *testing.B) {
		// Mix of ASCII and CJK characters, typical for a multilingual terminal.
		text := strings.Repeat("Hello 你好世界 test テスト 끝 ", 50)
		runes := []rune(text)
		for b.Loop() {
			inj := &wideCharInjector{}
			for _, r := range runes {
				inj.shouldPad(r)
			}
		}
	})

	b.Run("with_escape_sequences", func(b *testing.B) {
		// Simulate terminal output with ANSI escape sequences (colors, cursor moves).
		line := "\033[1;32mHello\033[0m \033[34m世界\033[0m \033[1A\033[2K"
		text := strings.Repeat(line, 50)
		runes := []rune(text)
		for b.Loop() {
			inj := &wideCharInjector{}
			for _, r := range runes {
				inj.shouldPad(r)
			}
		}
	})
}

func BenchmarkStripWidePadding(b *testing.B) {
	b.Run("no_padding", func(b *testing.B) {
		screen := strings.Repeat("Normal terminal output line\n", 24)
		for b.Loop() {
			stripWidePadding(screen)
		}
	})

	b.Run("with_padding", func(b *testing.B) {
		// Simulate a screen with wide padding runes scattered in.
		line := "Hello " + string(widePadRune) + "你好" + string(widePadRune) + " world\n"
		screen := strings.Repeat(line, 24)
		for b.Loop() {
			stripWidePadding(screen)
		}
	})
}
