package agentlog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineList keeps the lines it read, and wants no more after a "stop".
type lineList struct{ lines []string }

func (l *lineList) Line(line []byte) bool {
	l.lines = append(l.lines, strings.TrimSpace(string(line)))
	return string(line) != "stop\n"
}

func (l *lineList) Size() int { return 100 * len(l.lines) }

// readIndex reads path with x and returns the lines its reader holds.
func readIndex(t *testing.T, x *Index[*lineList], path string) []string {
	t.Helper()
	var lines []string
	require.NoError(t, x.Read(path, func(l *lineList, _ FileInfo) { lines = append([]string(nil), l.lines...) }))
	return lines
}

func appendTo(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestIndexReadsWhatWasAppended(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	opened := 0
	x := NewIndex(func(string) *lineList { opened++; return &lineList{} }, 10, 1<<20)

	appendTo(t, path, `{"n":1}`+"\n"+`{"n":2}`+"\n"+`{"n":`)
	assert.Equal(t, []string{`{"n":1}`, `{"n":2}`}, readIndex(t, x, path), "the line being written waits")
	// Appends that keep the modification time, as most do on a coarse
	// clock, are read all the same.
	before, err := os.Stat(path)
	require.NoError(t, err)
	appendTo(t, path, `3}`+"\n"+`{"n":4}`)
	require.NoError(t, os.Chtimes(path, before.ModTime(), before.ModTime()))
	assert.Equal(t, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`, `{"n":4}`}, readIndex(t, x, path), "a last line that is a whole JSON value is complete")
	appendTo(t, path, "\n"+`{"n":5}`+"\n")
	assert.Equal(t, []string{`{"n":1}`, `{"n":2}`, `{"n":3}`, `{"n":4}`, `{"n":5}`}, readIndex(t, x, path))
	assert.Equal(t, 1, opened, "one reader read it all")
}

func TestIndexReadsAgainWhatChanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	x := NewIndex(func(string) *lineList { return &lineList{} }, 10, 1<<20)

	require.NoError(t, os.WriteFile(path, []byte("a\nb\nc\n"), 0o600))
	assert.Equal(t, []string{"a", "b", "c"}, readIndex(t, x, path))

	// Shrunk.
	require.NoError(t, os.WriteFile(path, []byte("d\n"), 0o600))
	assert.Equal(t, []string{"d"}, readIndex(t, x, path))

	// Rewritten in place, longer than before.
	require.NoError(t, os.WriteFile(path, []byte("e\nf\ng\n"), 0o600))
	assert.Equal(t, []string{"e", "f", "g"}, readIndex(t, x, path))

	// Replaced by another file.
	other := filepath.Join(dir, "other.jsonl")
	require.NoError(t, os.WriteFile(other, []byte("e\nf\ng\nh\n"), 0o600))
	require.NoError(t, os.Rename(other, path))
	assert.Equal(t, []string{"e", "f", "g", "h"}, readIndex(t, x, path))

	// Gone.
	require.NoError(t, os.Remove(path))
	assert.Error(t, x.Read(path, nil))
	assert.Zero(t, x.Len(), "a file that is gone is forgotten")
}

func TestIndexStopsWhereTheReaderDoes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	x := NewIndex(func(string) *lineList { return &lineList{} }, 10, 1<<20)
	require.NoError(t, os.WriteFile(path, []byte("a\nstop\nb\n"), 0o600))
	assert.Equal(t, []string{"a", "stop"}, readIndex(t, x, path))
	appendTo(t, path, "c\n")
	assert.Equal(t, []string{"a", "stop"}, readIndex(t, x, path))
}

func TestIndexSkipsLinesTooLong(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	x := NewIndex(func(string) *lineList { return &lineList{} }, 10, 1<<20)
	long := strings.Repeat("x", 200*1024)
	require.NoError(t, os.WriteFile(path, []byte("a\n"+long+"\nb\n"), 0o600))
	lines := readIndex(t, x, path)
	require.Len(t, lines, 3, "a line longer than the read buffer")
	assert.Equal(t, long, lines[1])
}

func TestIndexIsBounded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	x := NewIndex(func(string) *lineList { return &lineList{} }, 3, 1<<20)
	var paths []string
	for _, name := range []string{"a", "b", "c", "d"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("line\n"), 0o600))
		paths = append(paths, path)
		require.NoError(t, x.Read(path, nil))
	}
	assert.Equal(t, 3, x.Len(), "the file read longest ago is forgotten")

	// About the memory the readers hold.
	x = NewIndex(func(string) *lineList { return &lineList{} }, 10, 250)
	for _, path := range paths {
		require.NoError(t, x.Read(path, nil))
	}
	assert.Equal(t, 2, x.Len())

	x.Retain(func(path string) bool { return path == paths[3] })
	assert.Equal(t, 1, x.Len())
}

func TestIndexReadsAFileOnceAtATime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("line\n", 1000)), 0o600))
	x := NewIndex(func(string) *lineList { return &lineList{} }, 10, 1<<30)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, x.Read(path, func(l *lineList, info FileInfo) {
				assert.Len(t, l.lines, 1000)
				assert.Equal(t, int64(5000), info.Size)
				assert.WithinDuration(t, time.Now(), info.ModTime, time.Minute)
			}))
		}()
	}
	wg.Wait()
}
