package main_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testTimeout        = 30 * time.Second
	operationTimeout   = 10 * time.Second
	healthCheckTimeout = 10 * time.Second
)

// agentapiBinary is $AGENTAPI_BINARY_PATH, or the binary built from the
// repository.
func agentapiBinary(ctx context.Context, t testing.TB) string {
	t.Helper()
	if path := os.Getenv("AGENTAPI_BINARY_PATH"); path != "" {
		return path
	}
	cwd, err := os.Getwd()
	require.NoError(t, err)
	path := filepath.Join(cwd, "..", "out", "agentapi")
	t.Logf("Building binary at %s", path)
	build := exec.CommandContext(ctx, "go", "build", "-o", path, ".")
	build.Dir = filepath.Join(cwd, "..")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build agentapi: %s", out)
	return path
}

// stopServer interrupts the server, which saves its state as it shuts
// down, and kills it if it hasn't exited after 10 seconds.
func stopServer(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// logOutput logs process output with prefix
func logOutput(t testing.TB, prefix string, r io.Reader) {
	t.Helper()
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		t.Logf("[%s] %s", prefix, scanner.Text())
	}
}

// getFreePort returns a free TCP port
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()

	return l.Addr().(*net.TCPAddr).Port, nil
}
