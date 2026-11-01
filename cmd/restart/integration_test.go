package restart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
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

// An AgentAPI restarts in place into the binary an update put where it
// was started from: the same process, a new server ID and version, its
// conversation kept by its state database. Everything it uses is the
// test's: its home, registry, config file, PID file and port.
func TestRestartInPlace(t *testing.T) {
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
	pidFile := filepath.Join(scratch, "agentapi.pid")
	port := freePort(t)
	env := scratchEnv(home)

	server := exec.Command(bin, "server", "--bind", "127.0.0.1", "--port", strconv.Itoa(port),
		"--registry-dir", reg, "--config", filepath.Join(home, "config.yaml"), "--pid-file", pidFile,
		"--state-db", filepath.Join(scratch, "state", "{port}.db"), "--type", "custom", "--", "native-demo")
	server.Env = env
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
	assert.Equal(t, "1.0.0-a", before.Version)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	resp, err := http.Post(base+"/message", "application/json", strings.NewReader(`{"content":"hello","type":"user"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Eventually(t, func() bool { return len(messages(t, base)) == 2 && agentStable(t, base) }, 30*time.Second, 50*time.Millisecond)
	conversation := messages(t, base)

	// The update writes the new binary next to the old one and renames it
	// over it, as agentapi update does.
	next := filepath.Join(scratch, "bin", "agentapi.new")
	build(ctx, t, root, next, "1.0.0-b")
	require.NoError(t, os.Rename(next, bin))

	restart := exec.CommandContext(ctx, bin, "restart", "--registry-dir", reg, "--timeout", "1m", strconv.Itoa(port))
	restart.Env = env
	out, err := restart.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "serves again")

	after := awaitEntry(ctx, t, reg, pid, before.ServerID)
	assert.Equal(t, "1.0.0-b", after.Version, "the new binary")
	var status struct {
		ServerID string `json:"server_id"`
		Version  string `json:"agentapi_version"`
	}
	getJSON(t, base+"/status", &status)
	assert.Equal(t, after.ServerID, status.ServerID)
	assert.Equal(t, "1.0.0-b", status.Version)
	assert.Equal(t, conversation, messages(t, base), "the state database kept the conversation")
	written, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(pid), strings.TrimSpace(string(written)), "the PID file stays valid")

	// agentapi update --restart restarts the AgentAPIs that run the binary
	// it replaced, and the handler works again after a restart.
	var report bytes.Buffer
	require.NoError(t, Run(ctx, Options{All: true, Executable: bin, RegistryDir: reg, Out: &report}), report.String())
	again := awaitEntry(ctx, t, reg, pid, after.ServerID)
	assert.Equal(t, "1.0.0-b", again.Version)
	select {
	case err := <-exited:
		t.Fatalf("the process exited: %v", err)
	default:
	}
}

// build builds AgentAPI from root into path, stamped with version.
func build(ctx context.Context, t *testing.T, root, path, version string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", path,
		"-ldflags", "-X github.com/coder/agentapi/internal/version.Version="+version, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go build: %s", out)
}

// scratchEnv is this process's environment with home as HOME, without the
// variables of AgentAPI and of a Claude Code session it may run under.
func scratchEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(key, "AGENTAPI_"), strings.HasPrefix(key, "CLAUDE_CODE_"),
			key == "CLAUDECODE", key == "CLAUDE_PID", key == "AI_AGENT", key == "HOME":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// awaitEntry waits for the registry entry of process pid whose server ID
// isn't not.
func awaitEntry(ctx context.Context, t *testing.T, dir string, pid int, not string) registry.Entry {
	t.Helper()
	for {
		entries, _ := registry.List(dir)
		for _, entry := range entries {
			if entry.PID == pid && entry.ServerID != not {
				return entry
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no registry entry of process %d", pid)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
}

func messages(t *testing.T, base string) []string {
	t.Helper()
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	getJSON(t, base+"/messages", &body)
	out := make([]string, 0, len(body.Messages))
	for _, m := range body.Messages {
		out = append(out, m.Content)
	}
	return out
}

func agentStable(t *testing.T, base string) bool {
	t.Helper()
	var status struct {
		Status string `json:"status"`
	}
	getJSON(t, base+"/status", &status)
	return status.Status == "stable"
}
