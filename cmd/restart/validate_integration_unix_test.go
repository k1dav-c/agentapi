//go:build unix

package restart

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/registry"
)

// Before it stops to restart in place, an AgentAPI has the new binary check
// the settings it would start with (--validate): one it would refuse keeps
// the server serving, as the same server, and its log says why. Once the
// settings are fixed, it restarts into the new binary.
func TestRestartRefusedByValidate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds AgentAPI")
	}
	if runtime.GOOS != "linux" {
		t.Skip("checked on Linux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	scratch := t.TempDir()
	bin := filepath.Join(scratch, "bin", "agentapi")
	build(ctx, t, root, bin, "1.0.0-a")
	home := filepath.Join(scratch, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	reg := filepath.Join(scratch, "instances")
	config := filepath.Join(home, "config.yaml")
	port := freePort(t)
	env := scratchEnv(home)

	server := exec.Command(bin, "server", "--api-token", "--bind", "127.0.0.1", "--port", strconv.Itoa(port),
		"--registry-dir", reg, "--config", config, "--state-db", filepath.Join(scratch, "state", "{port}.db"),
		"--type", "custom", "--", "native-demo")
	server.Env = env
	server.Dir = home
	var logs syncBuffer
	server.Stdout, server.Stderr = &logs, &logs
	require.NoError(t, server.Start())
	// exited is closed once the server exited, with waitErr.
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = server.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = server.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = server.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	pid := server.Process.Pid
	before := awaitEntry(ctx, t, reg, pid, "")
	var token string
	for _, line := range strings.Split(logs.String(), "\n") {
		if found, ok := strings.CutPrefix(line, "API token: "); ok {
			token = found
		}
	}
	require.NotEmpty(t, token)
	serverID := func() string {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/status", port), nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var status struct {
			ServerID string `json:"server_id"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
		return status.ServerID
	}
	require.Equal(t, before.ServerID, serverID())

	// An update put a new binary in place, and the config file (agentapi
	// hub join writes it) got a setting no AgentAPI starts with.
	next := filepath.Join(scratch, "bin", "agentapi.new")
	build(ctx, t, root, next, "1.0.0-b")
	require.NoError(t, os.Rename(next, bin))
	require.NoError(t, os.WriteFile(config, []byte("retry-delays: bogus\n"), 0o600))

	const refused = "Received SIGUSR2, but can't restart in place; still serving"
	require.NoError(t, server.Process.Signal(syscall.SIGUSR2))
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), refused) }, time.Minute, 50*time.Millisecond)
	assert.Contains(t, logs.String(), `wouldn't start: invalid --retry-delays`)
	assert.Equal(t, before.ServerID, serverID(), "the same server serves on")
	entries, err := registry.List(reg)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, before.ServerID, entries[0].ServerID)
	assert.Equal(t, "1.0.0-a", entries[0].Version)

	// agentapi restart says that it didn't restart.
	restart := exec.CommandContext(ctx, bin, "restart", "--registry-dir", reg, "--timeout", "3s", strconv.Itoa(port))
	restart.Env = env
	out, err := restart.CombinedOutput()
	require.Error(t, err, "%s", out)
	assert.Contains(t, string(out), "it still runs as before")
	assert.Equal(t, 2, strings.Count(logs.String(), refused))
	assert.Equal(t, before.ServerID, serverID())

	// With the setting gone, the new binary takes over, with the state
	// database the server has open.
	require.NoError(t, os.Remove(config))
	require.NoError(t, server.Process.Signal(syscall.SIGUSR2))
	after := awaitEntry(ctx, t, reg, pid, before.ServerID)
	assert.Equal(t, "1.0.0-b", after.Version)
	assert.Equal(t, after.ServerID, serverID(), "the generated token still signs in")
	assert.Contains(t, logs.String(), "validated=true", "the new binary checked the settings")
	select {
	case <-exited:
		t.Fatalf("the process exited: %v", waitErr)
	default:
	}
}
