package jsonlwatcher

import (
	"time"

	"golang.org/x/sys/unix"
)

// birthTime is when the file at path was created, where the file system
// records it.
func birthTime(path string) (time.Time, bool) {
	var stx unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, 0, unix.STATX_BTIME, &stx) != nil || stx.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}, false
	}
	return time.Unix(stx.Btime.Sec, int64(stx.Btime.Nsec)), true
}
