// Package sse reads and writes server-sent events (text/event-stream) as
// the HTML standard defines them: an event is the fields up to a blank
// line, and its data the data: lines of it, joined by newlines.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Event is an event of a stream.
type Event struct {
	// Type is the event's event: field; "message" when it has none.
	Type string
	// Data is its data: lines, joined by newlines.
	Data string
	// ID is the stream's last event ID, as of this event.
	ID string
}

// ErrTooLong is an event larger than the reader takes.
var ErrTooLong = errors.New("sse: event too long")

// Reader reads the events of a stream.
type Reader struct {
	scanner *bufio.Scanner
	max     int
	lastID  string
	first   bool
}

// NewReader reads the events of r, each of at most maxSize bytes of data (and
// no line longer than that).
func NewReader(r io.Reader, maxSize int) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, min(maxSize, 64<<10)), maxSize)
	scanner.Split(scanLines)
	return &Reader{scanner: scanner, max: maxSize, first: true}
}

// Next returns the next event that has data. It returns io.EOF once the
// stream ends; an event the stream cut short is dropped, as the standard
// says.
func (r *Reader) Next() (Event, error) {
	var (
		typ     string
		data    strings.Builder
		hasData bool
	)
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if r.first {
			line = strings.TrimPrefix(line, "\ufeff")
			r.first = false
		}
		if line == "" {
			if !hasData {
				typ = ""
				continue
			}
			if typ == "" {
				typ = "message"
			}
			return Event{Type: typ, Data: strings.TrimSuffix(data.String(), "\n"), ID: r.lastID}, nil
		}
		if strings.HasPrefix(line, ":") {
			continue // a comment
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			typ = value
		case "data":
			if data.Len()+len(value)+1 > r.max {
				return Event{}, ErrTooLong
			}
			data.WriteString(value)
			data.WriteByte('\n')
			hasData = true
		case "id":
			if !strings.ContainsRune(value, 0) {
				r.lastID = value
			}
		}
	}
	if err := r.scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return Event{}, ErrTooLong
		}
		return Event{}, err
	}
	return Event{}, io.EOF
}

// scanLines splits a stream into lines ended by CRLF, LF or CR.
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	i := bytes.IndexAny(data, "\r\n")
	switch {
	case i < 0:
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	case data[i] == '\n':
		return i + 1, data[:i], nil
	case i+1 < len(data):
		if data[i+1] == '\n' {
			return i + 2, data[:i], nil
		}
		return i + 1, data[:i], nil
	case atEOF:
		return i + 1, data[:i], nil
	}
	// A CR at the end of what was read: an LF may follow.
	return 0, nil, nil
}

// Write writes an event of type typ (none when empty) with data, a data:
// line for each of its lines.
func Write(w io.Writer, typ string, data []byte) error {
	if strings.ContainsAny(typ, "\r\n") {
		return fmt.Errorf("sse: event type %q has a line break", typ)
	}
	var b bytes.Buffer
	if typ != "" {
		b.WriteString("event: ")
		b.WriteString(typ)
		b.WriteByte('\n')
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		for part := range bytes.SplitSeq(line, []byte("\r")) {
			b.WriteString("data: ")
			b.Write(part)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}
