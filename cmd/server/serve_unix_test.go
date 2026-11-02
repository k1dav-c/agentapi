//go:build unix

package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/selfupdate"
	"github.com/coder/agentapi/lib/selfupdate/selfupdatetest"
)

// serveWith runs serve like serveNative, with self.
func serveWith(ctx context.Context, t *testing.T, self *selfExec) *servedNative {
	t.Helper()
	s := serveNative(ctx, t, &nativetest.Factory{}, self)
	s.awaitLifecycle(httpapi.LifecycleReady)
	return s
}

// SIGUSR2 stops the server as for a shutdown, closing the agent, and serve
// returns errRestart, after which the server runs its program again (not
// here: exec would replace the test).
func TestServeStopsToRestartOnSIGUSR2(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	self := &selfExec{path: program(t, t.TempDir(), "agentapi", `echo "agentapi version 2.1.0"`)}
	s := serveWith(ctx, t, self)

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))
	select {
	case err := <-s.served:
		require.ErrorIs(t, err, errRestart)
	case <-time.After(10 * time.Second):
		t.Fatal("the server didn't stop")
	}
	assert.True(t, s.factory.Last().Closed(), "the agent was closed")
	_, err := s.do("GET", "/status")
	assert.Error(t, err, "the listener is closed")
}

// An update through the API restarts the server as SIGUSR2 does, once it
// installed the release in place of the program: serve returns
// errRestart, after which the new program runs.
func TestServeRestartsForAnUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	self := &selfExec{path: selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), "2.1.0")}
	release := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	s := serveNativeWith(ctx, t, &nativetest.Factory{}, self, func(config *httpapi.ServerConfig) {
		config.Update = httpapi.UpdateConfig{
			Allowed: true, Program: self.path, Releases: release.Source(), Version: "2.1.0",
			Restart: func(ctx context.Context) error { return self.restart(ctx, "update") },
		}
	})
	s.awaitLifecycle(httpapi.LifecycleReady)

	resp, err := http.Post(s.url+"/update", "application/json", strings.NewReader(`{"when":"now"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	select {
	case err := <-s.served:
		require.ErrorIs(t, err, errRestart)
	case <-time.After(10 * time.Second):
		t.Fatal("the server didn't stop")
	}
	assert.True(t, s.factory.Last().Closed(), "the agent was closed")
	version, err := selfupdate.ProgramVersion(ctx, self.path)
	require.NoError(t, err)
	assert.Equal(t, "2.2.0", version.String(), "the program it runs next")
}

// A program that doesn't run keeps the server serving.
func TestServeKeepsServingIfTheProgramDoesntRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	self := &selfExec{path: program(t, t.TempDir(), "agentapi", "exit 1")}
	s := serveWith(ctx, t, self)

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR2))
	select {
	case err := <-s.served:
		t.Fatalf("the server stopped: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	assert.Equal(t, httpapi.LifecycleReady, s.lifecycle())
	cancel()
	select {
	case err := <-s.served:
		require.NoError(t, err, "a shutdown, not a restart")
	case <-time.After(10 * time.Second):
		t.Fatal("the server didn't stop")
	}
}
