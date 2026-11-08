package host

import (
	"net"
	"os/exec"
)

// Supported is whether the launcher runs on this platform.
const Supported = false

func startDetached(*exec.Cmd) error { return errUnsupportedPlatform }
func alive(int, string) bool        { return false }
func terminate(int) error           { return errUnsupportedPlatform }
func kill(int)                      {}
func peerPID(net.Conn) int          { return 0 }
func descendsFrom(int, int) bool    { return true }
func runs(int, string) bool         { return false }
