package host

import (
	"bytes"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// runs reports whether pid's command line has marker as one of its
// arguments, so a PID the system gave another process since (another job's
// included, whose instance ID marker begins) isn't taken for the job's
// AgentAPI.
func runs(pid int, marker string) bool {
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	if marker == "" {
		return true
	}
	for _, arg := range bytes.Split(cmdline, []byte{0}) {
		if string(arg) == marker {
			return true
		}
	}
	return false
}

// parentOf is pid's parent, from /proc; 0 when unknown.
func parentOf(pid int) int {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The command's name, in parentheses, may hold anything: the fields
	// start after its last ")".
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

// descendsFrom reports whether pid is ancestor or runs below it.
func descendsFrom(pid, ancestor int) bool {
	for range 64 {
		if pid == ancestor {
			return true
		}
		if pid <= 1 {
			return false
		}
		pid = parentOf(pid)
	}
	return false
}

// peerPID is the process at the other end of a unix socket; 0 when
// unknown.
func peerPID(conn net.Conn) int {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = raw.Control(func(fd uintptr) {
		if cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}
