package restart

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processOf describes the process pid from /proc.
func processOf(pid int) (process, error) {
	dir := "/proc/" + strconv.Itoa(pid)
	status, err := os.ReadFile(dir + "/status")
	if err != nil {
		return process{}, err
	}
	proc := process{uid: -1, ppid: -1}
	scanner := bufio.NewScanner(bytes.NewReader(status))
	for scanner.Scan() {
		key, value, _ := strings.Cut(scanner.Text(), ":")
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		switch key {
		case "Uid":
			// The real user ID comes first.
			proc.uid, _ = strconv.Atoi(fields[0])
		case "PPid":
			proc.ppid, _ = strconv.Atoi(fields[0])
		}
	}
	if proc.uid < 0 {
		return process{}, fmt.Errorf("%s/status has no Uid", dir)
	}
	cmdline, err := os.ReadFile(dir + "/cmdline")
	if err != nil {
		return process{}, err
	}
	if len(cmdline) == 0 {
		return process{}, errors.New("it has no command line (a zombie)")
	}
	proc.args = strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	// Other users' processes don't say; an update replaced the file of
	// those it updated.
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		proc.exe = strings.TrimSuffix(exe, " (deleted)")
	}
	return proc, nil
}
