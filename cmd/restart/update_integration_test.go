package restart

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/selfupdate/selfupdatetest"
)

// An AgentAPI started with --allow-update updates itself when a client
// asks: it installs the latest release in place of its program, checked
// against the release's checksums.txt, and restarts in place into it once
// the agent is idle, keeping its PID and, with its state database, its
// conversation. The release is a fake GitHub's, whose URL the test stamps
// into the build; everything else is the test's too: its home, registry,
// config file, PID file and port.
func TestUpdateThroughTheAPI(t *testing.T) {
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
	next := filepath.Join(scratch, "release", "agentapi")
	build(ctx, t, root, next, "2.2.0")
	binary, err := os.ReadFile(next)
	require.NoError(t, err)
	release := selfupdatetest.NewRelease(t, "2.2.0", binary)
	bin := filepath.Join(scratch, "bin", "agentapi")
	build(ctx, t, root, bin, "2.1.0", "-X github.com/coder/agentapi/lib/selfupdate.githubAPI="+release.URL)
	home := filepath.Join(scratch, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	reg := filepath.Join(scratch, "instances")
	pidFile := filepath.Join(scratch, "agentapi.pid")
	port := freePort(t)

	server := exec.Command(bin, "server", "--allow-update", "--bind", "127.0.0.1", "--port", strconv.Itoa(port),
		"--registry-dir", reg, "--config", filepath.Join(home, "config.yaml"), "--pid-file", pidFile,
		"--state-db", filepath.Join(scratch, "state", "{port}.db"), "--type", "custom", "--", "native-demo")
	server.Env = scratchEnv(home)
	server.Dir = home
	var logs bytes.Buffer
	server.Stdout, server.Stderr = &logs, &logs
	require.NoError(t, server.Start())
	exited := make(chan error, 1)
	go func() { exited <- server.Wait() }()
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
	assert.Equal(t, "2.1.0", before.Version)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	var summary instanceapi.SummaryBody
	getJSON(t, base+"/summary", &summary)
	require.NotNil(t, summary.Update)
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsConversation: true}, *summary.Update)
	resp, err := http.Post(base+"/message", "application/json", strings.NewReader(`{"content":"hello","type":"user"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Eventually(t, func() bool { return len(messages(t, base)) == 2 && agentStable(t, base) }, 30*time.Second, 50*time.Millisecond)
	conversation := messages(t, base)

	resp, err = http.Post(base+"/update", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	var accepted struct {
		State  string `json:"state"`
		Target string `json:"target"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&accepted))
	_ = resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Equal(t, "2.2.0", accepted.Target)

	after := awaitEntry(ctx, t, reg, pid, before.ServerID)
	assert.Equal(t, "2.2.0", after.Version, "the release")
	var status struct {
		ServerID string `json:"server_id"`
		Version  string `json:"agentapi_version"`
	}
	getJSON(t, base+"/status", &status)
	assert.Equal(t, after.ServerID, status.ServerID)
	assert.Equal(t, "2.2.0", status.Version)
	assert.Equal(t, conversation, messages(t, base), "the state database kept the conversation")
	getJSON(t, base+"/summary", &summary)
	assert.Equal(t, instanceapi.UpdateStatus{Allowed: true, State: instanceapi.UpdateStateIdle, KeepsConversation: true}, *summary.Update)
	written, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(pid), strings.TrimSpace(string(written)), "the PID file stays valid")
	installed, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, sha256.Sum256(binary), sha256.Sum256(installed), "the release's binary is installed")
	assert.Equal(t, 1, release.Downloads())
	assert.FileExists(t, filepath.Join(scratch, "bin", ".agentapi.lock"))
	select {
	case err := <-exited:
		t.Fatalf("the process exited: %v", err)
	default:
	}
}
