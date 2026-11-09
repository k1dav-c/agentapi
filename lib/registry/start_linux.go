package registry

import (
	"os"
	"strconv"
	"strings"
)

// processStart is field 22 of /proc/<pid>/stat: the clock ticks from boot
// to when the process started.
func processStart(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name, field 2, is in parentheses and may hold spaces:
	// the fields after it start after the last ")".
	i := strings.LastIndexByte(string(data), ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(string(data[i+1:]))
	// fields[0] is field 3, the state.
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
