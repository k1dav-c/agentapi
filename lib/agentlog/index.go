package agentlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// LogReader is the state of reading one session log: it reads the log's
// lines in order, and goes on where it stopped once the log grew.
type LogReader interface {
	// Line reads the log's next complete line, which it must not keep:
	// the index reuses its bytes. It returns false once it wants no more
	// of the log (one that turned out not to be what it reads).
	Line(line []byte) bool
	// Size is about how many bytes of memory the reader holds.
	Size() int
}

// Index keeps what was read of a set of session logs, file by file, so
// that a log is read again only from where it was left: an agent appends
// to its log as it works, and the log of a long session runs to hundreds
// of megabytes.
//
// For each file it keeps how far it was read and the reader that read it.
// Only complete lines are read; a line the agent is still writing waits
// for the next read. A file that shrank, was replaced (another inode or
// creation time) or rewritten (its bytes before where its reader stopped
// changed) is read again from the start, by a new reader. Its modification
// time can't tell: it is coarse, and most appends keep it.
//
// The index keeps at most maxFiles files, and readers of about maxBytes:
// past either, it forgets the files read longest ago. It forgets a file
// that is gone when it reads it, and the files Retain doesn't keep.
type Index[R LogReader] struct {
	newReader func(path string) R
	maxFiles  int
	maxBytes  int

	mu    sync.Mutex
	files map[string]*indexed[R]
	bytes int
	clock uint64
}

// indexed is what an Index keeps of one file.
type indexed[R LogReader] struct {
	// mu is held while the file is read and its reader used.
	mu     sync.Mutex
	reader R
	opened bool
	// done says the reader wants no more of the file.
	done bool
	// info is the file as it was when last read, offset how much of it
	// the reader read, and tail the bytes just before offset.
	info   FileInfo
	offset int64
	tail   []byte

	// size is the reader's Size and used when the file was last read,
	// both guarded by the index's mu.
	size int
	used uint64
}

// tailLength is how many bytes before the offset are compared to notice
// a file rewritten in place.
const tailLength = 64

// NewIndex returns an index whose readers newReader makes, which keeps at
// most maxFiles files and readers of about maxBytes.
func NewIndex[R LogReader](newReader func(path string) R, maxFiles, maxBytes int) *Index[R] {
	return &Index[R]{
		newReader: newReader,
		maxFiles:  maxFiles,
		maxBytes:  maxBytes,
		files:     make(map[string]*indexed[R]),
	}
}

// Read reads what was appended to the file at path since it was last
// read, and calls fn with the file's reader and description while no other
// Read uses the reader. It returns the error of a file that can't be read,
// which it forgets.
func (x *Index[R]) Read(path string, fn func(R, FileInfo)) error {
	e := x.entry(path)
	e.mu.Lock()
	err := e.update(path, x.newReader)
	size := 0
	if err == nil {
		if fn != nil {
			fn(e.reader, e.info)
		}
		size = e.reader.Size()
	}
	e.mu.Unlock()
	x.settle(path, e, size, err)
	return err
}

// Retain forgets the files keep doesn't want.
func (x *Index[R]) Retain(keep func(path string) bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for path, e := range x.files {
		if !keep(path) {
			delete(x.files, path)
			x.bytes -= e.size
		}
	}
}

// Len is how many files the index keeps.
func (x *Index[R]) Len() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.files)
}

// entry returns what the index keeps of path, new if it keeps nothing.
func (x *Index[R]) entry(path string) *indexed[R] {
	x.mu.Lock()
	defer x.mu.Unlock()
	e, ok := x.files[path]
	if !ok {
		e = &indexed[R]{}
		x.files[path] = e
	}
	x.clock++
	e.used = x.clock
	return e
}

// settle accounts for what reading e took, or forgets e when it failed,
// and forgets the files read longest ago while the index holds too much.
func (x *Index[R]) settle(path string, e *indexed[R], size int, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.files[path] != e {
		// Forgotten while it was read.
		return
	}
	if err != nil {
		delete(x.files, path)
		x.bytes -= e.size
		return
	}
	x.bytes += size - e.size
	e.size = size
	for len(x.files) > 1 && (len(x.files) > x.maxFiles || x.bytes > x.maxBytes) {
		oldest, oldestPath := e, path
		for p, o := range x.files {
			if o.used < oldest.used {
				oldest, oldestPath = o, p
			}
		}
		if oldest == e {
			return
		}
		delete(x.files, oldestPath)
		x.bytes -= oldest.size
	}
}

// update brings e up to date with the file at path.
func (e *indexed[R]) update(path string, newReader func(string) R) error {
	info, err := Stat(path)
	if err != nil {
		return err
	}
	switch {
	case !e.opened || !info.SameFile(e.info) || info.Size < e.info.Size || info.Size < e.offset:
		e.reset(path, newReader)
	case info.Size == e.info.Size && info.ModTime.Equal(e.info.ModTime):
		// Nothing was appended: an append makes the file longer.
		return nil
	}
	if e.done {
		e.info = info
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if e.offset > 0 && !e.sameTail(f) {
		e.reset(path, newReader)
	}
	if _, err := f.Seek(e.offset, io.SeekStart); err != nil {
		return err
	}
	consumed, more, err := feed(bufio.NewReaderSize(f, 64*1024), e.reader)
	e.offset += consumed
	e.done = !more
	e.info = info
	e.tail = nil
	if e.offset > 0 {
		start := max(0, e.offset-tailLength)
		tail := make([]byte, e.offset-start)
		if n, _ := f.ReadAt(tail, start); n == len(tail) {
			e.tail = tail
		}
	}
	return err
}

// reset starts reading the file again from its start.
func (e *indexed[R]) reset(path string, newReader func(string) R) {
	e.reader, e.opened, e.done = newReader(path), true, false
	e.offset, e.tail = 0, nil
	e.info = FileInfo{}
}

// sameTail reports whether the bytes before e's offset are still those it
// read: a file rewritten in place (Pi does so to migrate a session) keeps
// its inode, and can come out as long.
func (e *indexed[R]) sameTail(f *os.File) bool {
	if len(e.tail) == 0 {
		return false
	}
	now := make([]byte, len(e.tail))
	n, _ := f.ReadAt(now, e.offset-int64(len(e.tail)))
	return n == len(now) && bytes.Equal(now, e.tail)
}

// feed reads the complete lines of r into reader, and returns how many
// bytes they took and whether the reader wants more. A line longer than
// MaxLineLength is skipped.
func feed[R LogReader](r *bufio.Reader, reader R) (int64, bool, error) {
	var (
		consumed int64
		// long holds the start of a line longer than the buffer, n counts
		// its bytes, and tooLong says it is longer than MaxLineLength.
		long    []byte
		n       int
		tooLong bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		n += len(chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			if !tooLong && n <= MaxLineLength {
				long = append(long, chunk...)
			} else {
				tooLong, long = true, long[:0]
			}
			continue
		}
		line := chunk
		if len(long) > 0 || tooLong {
			if !tooLong && n <= MaxLineLength {
				long = append(long, chunk...)
				line = long
			} else {
				tooLong = true
			}
		}
		complete := err == nil
		if errors.Is(err, io.EOF) && n > 0 && !tooLong {
			// The last line has no newline yet. It is complete if it is a
			// whole JSON value, which no part of a line is.
			complete = json.Valid(bytes.TrimSpace(line))
		}
		if !complete {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return consumed, true, err
		}
		consumed += int64(n)
		if !tooLong && len(bytes.TrimSpace(line)) > 0 && !reader.Line(line) {
			return consumed, false, nil
		}
		long, n, tooLong = long[:0], 0, false
		if err != nil {
			return consumed, true, nil
		}
	}
}
