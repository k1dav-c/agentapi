package agentlog

import (
	"bytes"
	"encoding/json"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Field returns the raw JSON value of a top-level key of the object a log
// line holds, without decoding the rest of the line: a line can be
// megabytes of tool output, and most readers need one or two fields of
// it. ok is false when the line has no such key or isn't an object. It
// doesn't validate the line beyond what finding the key takes.
func Field(line []byte, key string) (value []byte, ok bool) {
	i := skipSpace(line, 0)
	if i >= len(line) || line[i] != '{' {
		return nil, false
	}
	i++
	for {
		i = skipSpace(line, i)
		if i >= len(line) || line[i] != '"' {
			return nil, false
		}
		end := skipString(line, i)
		if end < 0 {
			return nil, false
		}
		name := line[i+1 : end-1]
		i = skipSpace(line, end)
		if i >= len(line) || line[i] != ':' {
			return nil, false
		}
		i = skipSpace(line, i+1)
		valueEnd := skipValue(line, i)
		if valueEnd < 0 {
			return nil, false
		}
		if string(name) == key {
			return line[i:valueEnd], true
		}
		i = skipSpace(line, valueEnd)
		if i >= len(line) || line[i] != ',' {
			return nil, false
		}
		i++
	}
}

// Elements calls fn with each element of a JSON array, as Field finds
// values, until it returns false. It reports whether value is an array.
func Elements(value []byte, fn func(elem []byte) bool) bool {
	i := skipSpace(value, 0)
	if i >= len(value) || value[i] != '[' {
		return false
	}
	i = skipSpace(value, i+1)
	if i < len(value) && value[i] == ']' {
		return true
	}
	for i < len(value) {
		end := skipValue(value, i)
		if end < 0 {
			return false
		}
		if !fn(value[i:end]) {
			return true
		}
		i = skipSpace(value, end)
		if i >= len(value) || value[i] != ',' {
			return i < len(value) && value[i] == ']'
		}
		i = skipSpace(value, i+1)
	}
	return false
}

// StringField returns the value of a top-level string field of a log
// line, as Field finds it.
func StringField(line []byte, key string) (string, bool) {
	value, ok := Field(line, key)
	if !ok || len(value) < 2 || value[0] != '"' {
		return "", false
	}
	if bytes.IndexByte(value, '\\') < 0 {
		return string(value[1 : len(value)-1]), true
	}
	var s string
	if json.Unmarshal(value, &s) != nil {
		return "", false
	}
	return s, true
}

// Unquote decodes a JSON string value as json.Unmarshal does, in one pass
// over it where json.Unmarshal takes two (it validates the value first):
// tool output fills most of a long log. It returns false for a value that
// isn't a string.
func Unquote(value []byte) (string, bool) {
	if len(value) < 2 || value[0] != '"' || skipString(value, 0) != len(value) {
		return "", false
	}
	s := value[1 : len(value)-1]
	i := bytes.IndexByte(s, '\\')
	if i < 0 {
		if !utf8.Valid(s) {
			return unquoteSlowly(value)
		}
		return string(s), true
	}
	b := make([]byte, 0, len(s))
	for i >= 0 {
		b = append(b, s[:i]...)
		s = s[i:]
		if len(s) < 2 {
			return unquoteSlowly(value)
		}
		switch s[1] {
		case '"', '\\', '/':
			b, s = append(b, s[1]), s[2:]
		case 'b':
			b, s = append(b, '\b'), s[2:]
		case 'f':
			b, s = append(b, '\f'), s[2:]
		case 'n':
			b, s = append(b, '\n'), s[2:]
		case 'r':
			b, s = append(b, '\r'), s[2:]
		case 't':
			b, s = append(b, '\t'), s[2:]
		case 'u':
			r, n := unquoteRune(s)
			if n == 0 {
				return unquoteSlowly(value)
			}
			b, s = utf8.AppendRune(b, r), s[n:]
		default:
			return unquoteSlowly(value)
		}
		i = bytes.IndexByte(s, '\\')
	}
	b = append(b, s...)
	if !utf8.Valid(b) {
		return unquoteSlowly(value)
	}
	return string(b), true
}

// unquoteRune decodes the \uXXXX escape s starts with, and the low half
// of a surrogate pair that follows it, and returns how many bytes they
// took, or 0 for a malformed escape.
func unquoteRune(s []byte) (rune, int) {
	r := hex4(s)
	if r < 0 {
		return 0, 0
	}
	if !utf16.IsSurrogate(r) {
		return r, 6
	}
	if len(s) >= 12 && s[6] == '\\' && s[7] == 'u' {
		if low := hex4(s[6:]); low >= 0 {
			if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
				return pair, 12
			}
		}
	}
	return utf8.RuneError, 6
}

// hex4 decodes the four hex digits of the \uXXXX escape s starts with,
// or returns -1.
func hex4(s []byte) rune {
	if len(s) < 6 {
		return -1
	}
	var r rune
	for _, c := range s[2:6] {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c -= 'a' - 10
		case c >= 'A' && c <= 'F':
			c -= 'A' - 10
		default:
			return -1
		}
		r = r<<4 | rune(c)
	}
	return r
}

// unquoteSlowly is Unquote by json.Unmarshal, for what Unquote doesn't
// take on: invalid UTF-8, which json.Unmarshal replaces, and malformed
// escapes.
func unquoteSlowly(value []byte) (string, bool) {
	var s string
	if json.Unmarshal(value, &s) != nil {
		return "", false
	}
	return s, true
}

// LineTime is the timestamp of a log line, if it has one.
func LineTime(line []byte) (time.Time, bool) {
	value, ok := StringField(line, "timestamp")
	if !ok {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	return at, err == nil
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index after the string that starts at b[i], or
// -1 when it doesn't end.
func skipString(b []byte, i int) int {
	j := i + 1
	for {
		k := bytes.IndexByte(b[j:], '"')
		if k < 0 {
			return -1
		}
		j += k
		escapes := 0
		for p := j - 1; p > i && b[p] == '\\'; p-- {
			escapes++
		}
		if escapes%2 == 0 {
			return j + 1
		}
		j++
	}
}

// skipValue returns the index after the JSON value that starts at b[i], or
// -1 when it doesn't end.
func skipValue(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); {
			switch b[j] {
			case '"':
				end := skipString(b, j)
				if end < 0 {
					return -1
				}
				j = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1
				}
			}
			j++
		}
		return -1
	}
	// A number, true, false or null.
	j := i
	for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\t' && b[j] != '\n' && b[j] != '\r' {
		j++
	}
	if j == i {
		return -1
	}
	return j
}
