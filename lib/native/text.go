package native

import "strings"

// OneLine returns the first line of s cut to at most max runes, with an
// ellipsis for what is left out: a title for a command, or a log line.
func OneLine(s string, max int) string {
	line, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	runes := []rune(strings.TrimSpace(line))
	switch {
	case len(runes) > max:
		return string(runes[:max-1]) + "…"
	case strings.TrimSpace(rest) != "":
		return string(runes) + " …"
	}
	return string(runes)
}
