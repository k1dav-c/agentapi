package shellterm

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// referenceScrollback is how the scrollback was kept before the ring: the
// whole of it copied whenever output was dropped.
type referenceScrollback struct {
	size int
	data []byte
}

func (r *referenceScrollback) add(data []byte) {
	r.data = append(r.data, data...)
	if over := len(r.data) - r.size; over > 0 {
		kept := r.data[over:]
		if i := bytes.IndexByte(kept, '\n'); i >= 0 {
			kept = kept[i+1:]
		}
		r.data = append([]byte(nil), kept...)
	}
}

// The ring keeps what the copying scrollback kept, chunk after chunk, as
// it wraps around many times: lines of any length, chunks of any size up
// to more than the scrollback, and runs without a newline.
func TestScrollbackMatchesReference(t *testing.T) {
	t.Parallel()
	for _, size := range []int{1, 7, 64, 1000} {
		rng := rand.New(rand.NewPCG(uint64(size), 1))
		ring, ref := newScrollback(size), &referenceScrollback{size: size}
		for step := range 3000 {
			chunk := make([]byte, rng.IntN(2*size+3))
			newlines := rng.IntN(4) // Some chunks have none.
			for i := range chunk {
				chunk[i] = 'a' + byte(i%26)
				if newlines > 0 && rng.IntN(size/newlines+1) == 0 {
					chunk[i] = '\n'
				}
			}
			ring.add(chunk)
			ref.add(chunk)
			require.Equal(t, string(ref.data), string(ring.bytes()), "size %d, step %d", size, step)
			require.LessOrEqual(t, len(ring.buf), size)
		}
		assert.Greater(t, ring.end, int64(10*size), "wrapped around")
	}
}

// After the ring wrapped, what is kept starts at a line and ends with the
// latest output.
func TestScrollbackWrapsAround(t *testing.T) {
	t.Parallel()
	s := newScrollback(16)
	s.add([]byte("one\ntwo\nthree\n"))
	assert.Equal(t, "one\ntwo\nthree\n", string(s.bytes()))
	s.add([]byte("four\nfive"))
	// The last 16 bytes are "o\nthree\nfour\nfive": it starts after the first
	// newline.
	assert.Equal(t, "three\nfour\nfive", string(s.bytes()))
	s.add([]byte("\nsix\n"))
	assert.Equal(t, "four\nfive\nsix\n", string(s.bytes()))
	// A chunk larger than the scrollback leaves its last line.
	s.add([]byte("0123456789\nabcdefghijklmnop\nxyz"))
	assert.Equal(t, "xyz", string(s.bytes()))
	// Without any newline, the last 16 bytes are kept.
	s.add(bytes.Repeat([]byte("z"), 20))
	assert.Equal(t, string(bytes.Repeat([]byte("z"), 16)), string(s.bytes()))
	assert.Len(t, s.buf, 16)
}
