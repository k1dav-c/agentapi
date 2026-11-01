//go:build !linux && !windows

package restart

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// processOf describes the process pid as ps does: its command line split
// at spaces, which is enough to tell agentapi server, and no program path.
func processOf(pid int) (process, error) {
	out, err := exec.Command("ps", "-o", "uid=", "-o", "ppid=", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return process{}, fmt.Errorf("ps -p %d: %w", pid, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return process{}, fmt.Errorf("ps -p %d said %q", pid, out)
	}
	uid, err := strconv.Atoi(fields[0])
	if err != nil {
		return process{}, fmt.Errorf("ps -p %d said %q", pid, out)
	}
	ppid, _ := strconv.Atoi(fields[1])
	return process{uid: uid, ppid: ppid, args: fields[2:]}, nil
}
