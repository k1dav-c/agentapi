package convstate

import (
	"strings"
	"unicode/utf8"
)

// shortText returns s on one line, cut to at most n runes.
func shortText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

// cut returns s cut to at most n bytes, on a character boundary, with an
// ellipsis when it was cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
