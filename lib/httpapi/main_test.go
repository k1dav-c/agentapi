package httpapi

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points TMPDIR at a directory of the run's own and removes it
// after the tests: servers the tests don't stop leave their temporary
// upload directories there, not in the machine's /tmp.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentapi-httpapi-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
