package shellterm

import "bytes"

// scrollback keeps the recent output of the terminal in a ring, so adding
// a chunk takes time in proportion to the chunk, not to the scrollback.
// What it holds is a suffix of the output of at most size bytes. Once
// output was dropped it starts at a line rather than inside an escape
// sequence: whenever the output outgrows size, the start moves past the
// first newline of the last size bytes (or to the last size bytes, when
// they have no newline).
type scrollback struct {
	size int
	// buf holds the output at offset o (counted from the first output) at
	// buf[o%size]. It grows to size, then wraps.
	buf []byte
	// start and end are the offsets of what is kept.
	start, end int64
	// noNewline is an offset before which, from start on, there is no
	// newline: the search for one doesn't look there again.
	noNewline int64
}

func newScrollback(size int) *scrollback {
	return &scrollback{size: size}
}

// add appends data, dropping the oldest output beyond size.
func (s *scrollback) add(data []byte) {
	size := int64(s.size)
	end := s.end + int64(len(data))
	if int64(len(data)) > size {
		// Only the last size bytes of data can be kept.
		data = data[int64(len(data))-size:]
	}
	s.write(end-int64(len(data)), data)
	s.end = end
	if s.end-s.start <= size {
		return
	}
	start := s.end - size
	from := max(start, s.noNewline)
	if i := s.indexNewline(from); i >= 0 {
		s.start, s.noNewline = i+1, i+1
		return
	}
	s.start, s.noNewline = start, s.end
}

// write stores data at offset off, which is s.end or, when data is the
// end of a chunk larger than the ring, after it.
func (s *scrollback) write(off int64, data []byte) {
	if off+int64(len(data)) <= int64(s.size) {
		// The ring hasn't filled yet: off is len(s.buf).
		s.buf = append(s.buf, data...)
		return
	}
	if len(s.buf) < s.size {
		s.buf = append(s.buf, make([]byte, s.size-len(s.buf))...)
	}
	for len(data) > 0 {
		n := copy(s.buf[off%int64(s.size):], data)
		data = data[n:]
		off += int64(n)
	}
}

// segments returns what is kept from offset from on, in at most two
// slices of buf.
func (s *scrollback) segments(from int64) (first, second []byte) {
	if from >= s.end {
		return nil, nil
	}
	size := int64(s.size)
	i, j := int(from%size), int(s.end%size)
	if j == 0 {
		j = len(s.buf)
	}
	if i < j {
		return s.buf[i:j], nil
	}
	return s.buf[i:], s.buf[:j]
}

// indexNewline returns the offset of the first newline from offset from
// on, or -1.
func (s *scrollback) indexNewline(from int64) int64 {
	first, second := s.segments(from)
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		return from + int64(i)
	}
	if i := bytes.IndexByte(second, '\n'); i >= 0 {
		return from + int64(len(first)) + int64(i)
	}
	return -1
}

// bytes returns a copy of what is kept.
func (s *scrollback) bytes() []byte {
	first, second := s.segments(s.start)
	out := make([]byte, 0, len(first)+len(second))
	return append(append(out, first...), second...)
}
