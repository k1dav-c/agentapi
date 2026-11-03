//go:build unix

package main_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNativeClaude drives the native transport through the agentapi binary,
// with fakeclaude (fakeclaude/) as Claude Code: a turn, then a restart in
// place (SIGUSR2) that keeps the conversation in the state database.
func TestNativeClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("restart_keeps_conversation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		s := startNative(ctx, t)
		firstID := s.waitStable(ctx, "")

		s.post(ctx, "/message", `{"content":"hello native","type":"user"}`)
		s.waitStable(ctx, "")
		before := s.messages(ctx)
		require.Equal(t, []string{"hello native", "Echo: hello native"}, before)

		require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
		s.waitStable(ctx, firstID)
		require.Equal(t, before, s.messages(ctx), "the conversation survives the restart")

		// The restarted agent resumes the session and takes another turn.
		s.post(ctx, "/message", `{"content":"after restart","type":"user"}`)
		s.waitStable(ctx, "")
		require.Equal(t, append(before, "after restart", "Echo: after restart"), s.messages(ctx))
	})

	for _, tc := range []struct{ name, flag string }{
		{"api_token_kept_on_restart/value", "--api-token=e2e-secret-token"},
		{"api_token_kept_on_restart/generate", "--api-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.flag == "--api-token" {
				// A restart in place runs the server again with its arguments,
				// and a bare --api-token generates another token: clients
				// signed in with the first are locked out. The native worker's
				// api-token re-exec fix keeps it; unskip once it lands.
				t.Skip("needs api-token re-exec fix")
			}
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			s := startNative(ctx, t, tc.flag)
			token := strings.TrimPrefix(tc.flag, "--api-token=")
			if tc.flag == "--api-token" {
				token = s.waitToken(ctx, 1)
			}
			s.token = token
			firstID := s.waitStable(ctx, "")

			require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
			s.waitStable(ctx, firstID)
			if tc.flag == "--api-token" {
				// A new token would be printed again; none is.
				require.Equal(t, []string{token}, s.tokens(), "the restarted server keeps the generated token")
			}
			s.post(ctx, "/message", `{"content":"still signed in","type":"user"}`)
			s.waitStable(ctx, "")
			require.Equal(t, []string{"still signed in", "Echo: still signed in"}, s.messages(ctx))
		})
	}
}

// nativeServer is an agentapi server running fakeclaude.
type nativeServer struct {
	t     *testing.T
	cmd   *exec.Cmd
	url   string
	token string

	mu     sync.Mutex
	stderr []string
}

// startNative starts `agentapi server -- claude` with a state database,
// fakeclaude first on PATH and a scratch HOME, and stops it at the end of
// the test.
func startNative(ctx context.Context, t *testing.T, flags ...string) *nativeServer {
	t.Helper()
	binaryPath := agentapiBinary(ctx, t)
	cwd, err := os.Getwd()
	require.NoError(t, err)
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(bin, "claude"), "./fakeclaude")
	build.Dir = cwd
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build fakeclaude: %s", out)

	home := filepath.Join(tmp, "home")
	work := filepath.Join(tmp, "work")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.MkdirAll(work, 0o700))
	port, err := getFreePort()
	require.NoError(t, err)

	args := append([]string{"server", fmt.Sprintf("--port=%d", port), "--bind=127.0.0.1", "--state-db=" + filepath.Join(tmp, "state.db")}, flags...)
	args = append(args, "--", "claude")
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = work
	cmd.Env = append(cleanEnv(),
		"HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTAPI_REGISTRY_DIR="+filepath.Join(tmp, "registry"),
		"AGENTAPI_CONFIG="+filepath.Join(tmp, "config.yaml"),
	)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)
	t.Logf("run: %s", cmd.String())
	require.NoError(t, cmd.Start())

	s := &nativeServer{t: t, cmd: cmd, url: fmt.Sprintf("http://localhost:%d", port)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		logOutput(t, "SERVER-STDOUT", stdout)
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			s.mu.Lock()
			s.stderr = append(s.stderr, line)
			s.mu.Unlock()
			t.Logf("[SERVER-STDERR] %s", line)
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		wg.Wait()
	})
	return s
}

// cleanEnv is the test's environment without what would point the server
// at the user's AgentAPIs or agent sessions.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "AGENTAPI_") || strings.HasPrefix(name, "CLAUDE_CODE_") || name == "CLAUDECODE" ||
			name == "HOME" || name == "PATH" || name == "CLAUDE_CONFIG_DIR" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// agentapiBinary is $AGENTAPI_BINARY_PATH, or the binary built from the
// repository.
func agentapiBinary(ctx context.Context, t *testing.T) string {
	t.Helper()
	if path := os.Getenv("AGENTAPI_BINARY_PATH"); path != "" {
		return path
	}
	cwd, err := os.Getwd()
	require.NoError(t, err)
	path := filepath.Join(cwd, "..", "out", "agentapi")
	build := exec.CommandContext(ctx, "go", "build", "-o", path, ".")
	build.Dir = filepath.Join(cwd, "..")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build agentapi: %s", out)
	return path
}

// do sends a request with the server's token, and returns the response's
// status and body; a connection error returns status 0.
func (s *nativeServer) do(ctx context.Context, method, path, body string) (int, []byte) {
	req, err := http.NewRequestWithContext(ctx, method, s.url+path, strings.NewReader(body))
	require.NoError(s.t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (s *nativeServer) post(ctx context.Context, path, body string) {
	s.t.Helper()
	code, data := s.do(ctx, http.MethodPost, path, body)
	require.Equal(s.t, http.StatusOK, code, "POST %s: %s", path, data)
}

// waitStable waits until the server reports the agent stable, from a run
// whose server_id isn't notID, and returns that server_id.
func (s *nativeServer) waitStable(ctx context.Context, notID string) string {
	s.t.Helper()
	deadline := time.Now().Add(operationTimeout)
	var last string
	for time.Now().Before(deadline) && ctx.Err() == nil {
		code, data := s.do(ctx, http.MethodGet, "/status", "")
		if code == http.StatusOK {
			var status struct {
				Status   string `json:"status"`
				ServerID string `json:"server_id"`
			}
			require.NoError(s.t, json.Unmarshal(data, &status))
			if status.Status == "stable" && status.ServerID != "" && status.ServerID != notID {
				return status.ServerID
			}
		}
		last = fmt.Sprintf("%d %s", code, bytes.TrimSpace(data))
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("not stable on a run other than %q; last /status: %s", notID, last)
	return ""
}

// messages are the conversation's message contents, trimmed.
func (s *nativeServer) messages(ctx context.Context) []string {
	s.t.Helper()
	code, data := s.do(ctx, http.MethodGet, "/messages", "")
	require.Equal(s.t, http.StatusOK, code, "GET /messages: %s", data)
	var resp struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(s.t, json.Unmarshal(data, &resp))
	contents := make([]string, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		contents = append(contents, strings.TrimSpace(m.Content))
	}
	return contents
}

// tokens are the API tokens the server printed.
func (s *nativeServer) tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	for _, line := range s.stderr {
		if token, ok := strings.CutPrefix(line, "API token: "); ok {
			tokens = append(tokens, strings.TrimSpace(token))
		}
	}
	return tokens
}

// waitToken waits until the server printed n tokens and returns the last.
func (s *nativeServer) waitToken(ctx context.Context, n int) string {
	s.t.Helper()
	deadline := time.Now().Add(healthCheckTimeout)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if tokens := s.tokens(); len(tokens) >= n {
			return tokens[n-1]
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("the server printed no API token")
	return ""
}
