package registry

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// processStart is the process's start time from the kernel's process
// table, to the microsecond.
func processStart(pid int) string {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_pid != int32(pid) {
		return ""
	}
	t := info.Proc.P_starttime
	return strconv.FormatInt(t.Sec, 10) + "." + strconv.FormatInt(int64(t.Usec), 10)
}
