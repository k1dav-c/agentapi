//go:build !windows && !linux

package host

import (
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// runs reports whether pid's command line, as ps tells it, has marker as
// one of its words (instance IDs have no spaces).
func runs(pid int, marker string) bool {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	if marker == "" {
		return true
	}
	for _, word := range strings.Fields(string(out)) {
		if word == marker {
			return true
		}
	}
	return false
}

// peerPID isn't known here: the token's caller isn't checked (the
// socket's 0600 limits it to the launcher's user).
func peerPID(net.Conn) int { return 0 }

func descendsFrom(int, int) bool { return true }
