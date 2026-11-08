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
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNativeClaude drives the native transport through the agentapi binary,
// with fakeclaude (fakeclaude/) as Claude Code: a turn, then a restart in
// place (SIGUSR2) that keeps the conversation, from the state database or,
// without one, from the session the agent resumes.
func TestNativeClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// With a state database, the conversation is restored from it; without
	// one, the restarted program resumes the agent's session and reads the
	// conversation from the session's log.
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"restart_keeps_conversation/state_db", nil},
		{"restart_keeps_conversation/no_state_db", []string{"--state-db="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			s := startNative(ctx, t, tc.flags...)
			firstID := s.waitStable(ctx, "")

			s.post(ctx, "/message", `{"content":"hello native","type":"user"}`)
			s.waitStable(ctx, "")
			before := s.messages(ctx)
			require.Equal(t, []string{"hello native", "Echo: hello native"}, before)
			session := s.sessionID(ctx)
			require.NotEmpty(t, session)

			require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
			s.waitStable(ctx, firstID)
			require.Equal(t, before, s.messages(ctx), "the conversation survives the restart")
			require.Equal(t, session, s.sessionID(ctx), "the agent resumes its session")

			// The restarted agent takes another turn in the session.
			s.post(ctx, "/message", `{"content":"after restart","type":"user"}`)
			s.waitStable(ctx, "")
			require.Equal(t, append(before, "after restart", "Echo: after restart"), s.messages(ctx))
		})
	}

	t.Run("restart_twice_keeps_conversation/no_state_db", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		s := startNative(ctx, t, "--state-db=")
		firstID := s.waitStable(ctx, "")

		s.post(ctx, "/message", `{"content":"hello native","type":"user"}`)
		s.waitStable(ctx, "")
		before := s.messages(ctx)
		require.Equal(t, []string{"hello native", "Echo: hello native"}, before)

		require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
		secondID := s.waitStable(ctx, firstID)
		s.post(ctx, "/message", `{"content":"after restart","type":"user"}`)
		s.waitStable(ctx, "")
		// A second restart carries the session again.
		require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
		s.waitStable(ctx, secondID)
		require.Equal(t, append(before, "after restart", "Echo: after restart"), s.messages(ctx))
	})

	// The restarted program gets the same --initial-prompt, which it
	// doesn't send again: with a state database the restored conversation
	// has it already; without one, the resumed session does.
	for _, tc := range []struct {
		name  string
		flags []string
		after []string
	}{
		{"initial_prompt_once/state_db", nil, []string{"first prompt", "Echo: first prompt"}},
		{"initial_prompt_once/no_state_db", []string{"--state-db="}, []string{"first prompt", "Echo: first prompt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			s := startNative(ctx, t, append(tc.flags, "--initial-prompt=first prompt")...)
			firstID := s.waitStable(ctx, "")
			s.waitMessages(ctx, []string{"first prompt", "Echo: first prompt"})

			require.NoError(t, s.cmd.Process.Signal(syscall.SIGUSR2))
			s.waitStable(ctx, firstID)
			// A prompt's turn begins before the agent is reported stable.
			require.Equal(t, tc.after, s.messages(ctx))
		})
	}

	for _, tc := range []struct{ name, flag string }{
		{"api_token_kept_on_restart/value", "--api-token=e2e-secret-token"},
		{"api_token_kept_on_restart/generate", "--api-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				// The restarted server prints its token again: the same one.
				for _, printed := range s.tokens() {
					require.Equal(t, token, printed, "the restarted server keeps the generated token")
				}
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
		stopServer(cmd)
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

// sessionID is the agent's session, as /status reports it.
func (s *nativeServer) sessionID(ctx context.Context) string {
	s.t.Helper()
	code, data := s.do(ctx, http.MethodGet, "/status", "")
	require.Equal(s.t, http.StatusOK, code, "GET /status: %s", data)
	var status struct {
		AgentSessionID string `json:"agent_session_id"`
	}
	require.NoError(s.t, json.Unmarshal(data, &status))
	return status.AgentSessionID
}

// waitMessages waits until the conversation's messages are want.
func (s *nativeServer) waitMessages(ctx context.Context, want []string) {
	s.t.Helper()
	deadline := time.Now().Add(operationTimeout)
	var got []string
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if got = s.messages(ctx); slices.Equal(got, want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatalf("messages: want %q, got %q", want, got)
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
