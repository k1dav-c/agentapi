package agentlog

import (
	"bytes"
	"time"
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

// StringField returns the value of a top-level string field of a log
// line, as Field finds it. Values with escapes are returned as they are
// written, which the record types and timestamps it is for never have.
func StringField(line []byte, key string) (string, bool) {
	value, ok := Field(line, key)
	if !ok || len(value) < 2 || value[0] != '"' {
		return "", false
	}
	return string(value[1 : len(value)-1]), true
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
