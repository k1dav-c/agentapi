package agentlog

import (
	"time"

	"golang.org/x/sys/unix"
)

// BirthTime is when the file at path was created: macOS's stat has it
// (st_birthtimespec, Btim here).
func BirthTime(path string) (time.Time, bool) {
	var st unix.Stat_t
	if unix.Stat(path, &st) != nil || (st.Btim.Sec == 0 && st.Btim.Nsec == 0) {
		return time.Time{}, false
	}
	return time.Unix(st.Btim.Unix()), true
}
