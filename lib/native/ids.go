package native

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// RequestPrefix returns a random prefix for the ids an adapter makes (of
// requests, say), so that they don't repeat those of an earlier adapter of
// the same Factory (see Sink).
func RequestPrefix() string {
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	return hex.EncodeToString(nonce[:])
}

// UnixMilli is the time of a protocol's Unix milliseconds, or zero for
// none.
func UnixMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
