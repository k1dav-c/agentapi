//go:build unix

package restart

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A token a bare --api-token generated stays the same across a restart in
// place, which runs the same arguments: clients stay signed in.
func TestGeneratedAPITokenAcrossRestart(t *testing.T) {
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
	build(ctx, t, root, bin, "1.0.0")
	home := filepath.Join(scratch, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	reg := filepath.Join(scratch, "instances")
	port := freePort(t)

	server := exec.Command(bin, "server", "--api-token", "--bind", "127.0.0.1", "--port", strconv.Itoa(port),
		"--registry-dir", reg, "--config", filepath.Join(home, "config.yaml"), "--type", "custom", "--", "native-demo")
	server.Env = scratchEnv(home)
	server.Dir = home
	var logs syncBuffer
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
	tokens := func() []string {
		var out []string
		for _, line := range strings.Split(logs.String(), "\n") {
			if token, ok := strings.CutPrefix(line, "API token: "); ok {
				out = append(out, token)
			}
		}
		return out
	}
	require.Len(t, tokens(), 1)
	token := tokens()[0]

	require.NoError(t, server.Process.Signal(syscall.SIGUSR2))
	awaitEntry(ctx, t, reg, pid, before.ServerID)
	assert.Equal(t, []string{token, token}, tokens())
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/status", port), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the token still signs in")
}

// syncBuffer is a bytes.Buffer that a process writes to while the test
// reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
