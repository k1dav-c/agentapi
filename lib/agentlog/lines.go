package agentlog

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
)

// MaxLineLength bounds a session log line; a longer one (a huge tool
// result) is skipped.
const MaxLineLength = 64 * 1024 * 1024

// ScanFile calls fn with each line of a session log until it returns
// false. fn must not keep the line, whose bytes are reused. It reports
// whether the log could be read.
func ScanFile(path string, fn func(line []byte) bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64*1024)
	var buf []byte
	for {
		line, err := readLine(r, buf[:0])
		buf = line
		if len(bytes.TrimSpace(line)) > 0 && !fn(line) {
			return true
		}
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// ReadLine reads a line, which can be longer than the reader's buffer. A
// line longer than MaxLineLength is returned empty.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	return readLine(r, nil)
}

// readLine is ReadLine into line's bytes.
func readLine(r *bufio.Reader, line []byte) ([]byte, error) {
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		switch {
		case tooLong:
		case len(line)+len(chunk) <= MaxLineLength:
			line = append(line, chunk...)
		default:
			// The rest of the line is dropped too.
			tooLong, line = true, line[:0:0]
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}
