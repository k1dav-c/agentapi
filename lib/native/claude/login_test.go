package claude

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

func TestParseAuthStatus(t *testing.T) {
	t.Parallel()
	// Shapes Claude Code 2.1.299 printed with an empty CLAUDE_CONFIG_DIR,
	// alone and with a credential in the environment (account fields
	// dropped).
	for _, tc := range []struct {
		name     string
		out      string
		loggedIn bool
		ok       bool
	}{
		{"empty config", `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`, false, true},
		{"ANTHROPIC_API_KEY", `{"loggedIn":true,"authMethod":"api_key","apiProvider":"firstParty","apiKeySource":"ANTHROPIC_API_KEY"}`, true, true},
		{"CLAUDE_CODE_OAUTH_TOKEN", `{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}`, true, true},
		{"Bedrock", `{"loggedIn":true,"authMethod":"third_party","apiProvider":"bedrock"}`, true, true},
		{"apiKeyHelper", `{"loggedIn":true,"authMethod":"api_key_helper","apiKeySource":"apiKeyHelper"}`, true, true},
		{"pretty-printed", "{\n  \"loggedIn\": true\n}\n", true, true},
		{"after a warning line", "warning: something\n{\"loggedIn\":false}\n", false, true},
		{"no output", "", false, false},
		{"older CLI", "error: unknown command 'auth'\n", false, false},
		{"text output", "Not logged in. Run claude auth login to authenticate.\n", false, false},
		{"other JSON", `{"status":"ok"}`, false, false},
		{"wrong type", `{"loggedIn":"yes"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			loggedIn, ok := parseAuthStatus([]byte(tc.out))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.loggedIn, loggedIn)
		})
	}
}

// authStatus is the type of Config.AuthStatus.
type authStatus = func(ctx context.Context, cmd procutil.Command) ([]byte, error)

func TestStartChecksLogin(t *testing.T) {
	t.Parallel()
	start := func(t *testing.T, args []string, status authStatus) ([]procutil.Command, bool, error) {
		t.Helper()
		var checks []procutil.Command
		spawned := false
		proc := newFakeProcess("")
		a := newTestAdapter(t, Config{
			Args: args,
			Env:  []string{"PATH=/bin", "CLAUDECODE=1", "ANTHROPIC_API_KEY=k"},
			CWD:  "/work",
			AuthStatus: func(ctx context.Context, c procutil.Command) ([]byte, error) {
				checks = append(checks, c)
				return status(ctx, c)
			},
			StartProcess: func(procutil.Command) (procutil.Process, error) {
				spawned = true
				return proc, nil
			},
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			StopTimeout: 10 * time.Millisecond,
		}, "")
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		err := a.Start(ctx, &recSink{t: t})
		_ = a.Close()
		return checks, spawned, err
	}

	t.Run("not logged in", func(t *testing.T) {
		t.Parallel()
		// The CLI exits with 1 and still prints the status.
		checks, spawned, err := start(t, nil, func(context.Context, procutil.Command) ([]byte, error) {
			return []byte(`{"loggedIn":false,"authMethod":"none"}`), errExit1
		})
		var login *native.LoginRequiredError
		require.ErrorAs(t, err, &login)
		assert.Equal(t, loginHint, login.Login)
		assert.Equal(t, "claude auth login", login.Login.Command)
		assert.False(t, spawned, "the session isn't started")
		require.Len(t, checks, 1)
		assert.Equal(t, "claude", checks[0].Program)
		assert.Equal(t, []string{"auth", "status", "--json"}, checks[0].Args)
		assert.Equal(t, "/work", checks[0].Dir)
		// The same environment as the session.
		assert.Equal(t, buildEnv([]string{"PATH=/bin", "CLAUDECODE=1", "ANTHROPIC_API_KEY=k"}), checks[0].Env)
		assert.False(t, slices.Contains(checks[0].Env, "CLAUDECODE=1"))
	})

	t.Run("logged in", func(t *testing.T) {
		t.Parallel()
		checks, spawned, err := start(t, nil, loggedIn)
		// The fake process never answers initialize.
		require.Error(t, err)
		var login *native.LoginRequiredError
		assert.False(t, errors.As(err, &login))
		assert.Len(t, checks, 1)
		assert.True(t, spawned)
	})

	for name, status := range map[string]authStatus{
		"command fails": func(context.Context, procutil.Command) ([]byte, error) {
			return nil, errors.New(`exec: "claude": executable file not found in $PATH`)
		},
		"unknown output": func(context.Context, procutil.Command) ([]byte, error) {
			return []byte("Usage: wrapper [options]\n"), errExit1
		},
		"times out": func(ctx context.Context, _ procutil.Command) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	} {
		t.Run(name+" starts anyway", func(t *testing.T) {
			t.Parallel()
			_, spawned, err := start(t, nil, status)
			var login *native.LoginRequiredError
			assert.False(t, errors.As(err, &login))
			assert.True(t, spawned)
		})
	}

	t.Run("--settings skips the check", func(t *testing.T) {
		t.Parallel()
		checks, spawned, err := start(t, []string{"--settings", "s.json"}, func(context.Context, procutil.Command) ([]byte, error) {
			return []byte(`{"loggedIn":false}`), errExit1
		})
		var login *native.LoginRequiredError
		assert.False(t, errors.As(err, &login))
		assert.Empty(t, checks)
		assert.True(t, spawned)
	})
}

func TestRunAuthStatus(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	dir := t.TempDir()
	script := func(name, body string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
		return path
	}

	t.Run("output of a failing command", func(t *testing.T) {
		t.Parallel()
		program := script("logged-out", `[ "$1 $2 $3" = "auth status --json" ] || exit 2
echo '{"loggedIn":false}'
echo 'stderr is not read' >&2
exit 1
`)
		out, err := runAuthStatus(context.Background(), procutil.Command{Program: program, Args: []string{"auth", "status", "--json"}})
		require.Error(t, err)
		loggedIn, ok := parseAuthStatus(out)
		assert.True(t, ok)
		assert.False(t, loggedIn)
	})

	t.Run("a hanging command is stopped", func(t *testing.T) {
		t.Parallel()
		program := script("hangs", "sleep 30 &\nwait\n")
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		began := time.Now()
		_, err := runAuthStatus(ctx, procutil.Command{Program: program})
		require.Error(t, err)
		assert.Less(t, time.Since(began), 5*time.Second)
	})
}

func TestLoginLostMidSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("not logged in", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, sink := started(t, Config{}, proc)
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
		// What Claude Code 2.1.299 sends for a turn without a login.
		proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
		proc.send(t, []byte(`{"type":"assistant","message":{"id":"52c0c71b","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","type":"message","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"Not logged in · Please run /login"}]},"parent_tool_use_id":null,"session_id":"s1","uuid":"u1","error":"authentication_failed","is_api_error_message":true}`))
		proc.send(t, []byte(`{"type":"result","subtype":"success","is_error":true,"api_error_status":null,"duration_api_ms":0,"num_turns":1,"result":"Not logged in · Please run /login","stop_reason":"stop_sequence","session_id":"s1","terminal_reason":"api_error","total_cost_usd":0}`))
		proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
		proc.barrier(t)
		sink.mu.Lock()
		defer sink.mu.Unlock()
		require.Equal(t, []native.LoginRequired{loginHint}, sink.logins)
		require.Len(t, sink.results, 1)
		assert.Equal(t, "error", sink.results[0].StopReason)
		// The result says "success" with is_error; the terminal reason
		// says what failed.
		assert.Equal(t, "Claude Code: the turn failed (api_error): Not logged in · Please run /login", sink.results[0].Error)
		// The login is reported before the turn ends.
		login := slices.Index(sink.log, "login_required")
		assert.Less(t, login, slices.Index(sink.log, "turn_completed:error"))
	})

	for name, result := range map[string]string{
		"invalid API key": `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"num_turns":1,"result":"Invalid API key · Please run /login"}`,
		"expired token":   `{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1,"errors":["OAuth token has expired. Please obtain a new token or refresh your existing token."]}`,
		"401 only":        `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"num_turns":1,"result":"API Error"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proc := newFakeProcess("")
			a, sink := started(t, Config{}, proc)
			require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
			proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
			proc.send(t, []byte(result))
			proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
			proc.barrier(t)
			sink.mu.Lock()
			defer sink.mu.Unlock()
			assert.Len(t, sink.logins, 1)
		})
	}

	t.Run("other errors and answers are not login problems", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, sink := started(t, Config{}, proc)
		for _, result := range []string{
			`{"type":"result","subtype":"error_during_execution","is_error":true,"api_error_status":529,"errors":["Overloaded"]}`,
			// A reply that talks about /login is not an error.
			`{"type":"result","subtype":"success","is_error":false,"result":"Please run /login in your terminal."}`,
		} {
			require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
			proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
			proc.send(t, []byte(result))
			proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
			proc.barrier(t)
		}
		sink.mu.Lock()
		defer sink.mu.Unlock()
		assert.Empty(t, sink.logins)
		assert.Len(t, sink.results, 2)
	})
}
