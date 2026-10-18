package native

import "strings"

// MaxTitleRunes bounds a request's title, which the chat shows as the
// card's heading; the rest is in its input or detail.
const MaxTitleRunes = 120

// OneLine returns the first line of s in at most limit runes (two or
// more), with an ellipsis for what is left out: a title for a command, or a
// log line.
func OneLine(s string, limit int) string {
	line, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	runes := []rune(strings.TrimSpace(line))
	more := strings.TrimSpace(rest) != ""
	switch {
	case len(runes) > limit, more && len(runes) > limit-2:
		return string(runes[:limit-1]) + "…"
	case more:
		return string(runes) + " …"
	}
	return string(runes)
}
