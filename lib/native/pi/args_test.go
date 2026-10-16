package pi

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      []string
		want    []string
		dropped []string
		err     string
	}{
		{name: "none", in: nil, want: []string{"--mode", "rpc", "--session-id", "s"}},
		{
			name: "pass through",
			in:   []string{"--provider", "openai-codex", "--model", "gpt-5.6-luna", "--thinking", "low", "-e", "ext.ts", "--approve", "--plan"},
			want: []string{"--mode", "rpc", "--session-id", "s", "--provider", "openai-codex", "--model", "gpt-5.6-luna", "--thinking", "low", "-e", "ext.ts", "--approve", "--plan"},
		},
		{
			name: "equals form of built-in flags is split",
			in:   []string{"--model=openai-codex/gpt-5.6-luna", "--session-dir=/tmp/s", "--plan=x"},
			want: []string{"--mode", "rpc", "--session-id", "s", "--model", "openai-codex/gpt-5.6-luna", "--session-dir", "/tmp/s", "--plan=x"},
		},
		{
			name:    "TUI flags are dropped",
			in:      []string{"--tui-mode", "regular", "--use-theme=dark", "--model", "m"},
			want:    []string{"--mode", "rpc", "--session-id", "s", "--model", "m"},
			dropped: []string{"--tui-mode", "regular", "--use-theme=dark"},
		},
		{name: "value that looks like a file", in: []string{"--name", "@home"}, want: []string{"--mode", "rpc", "--session-id", "s", "--name", "@home"}},
		{name: "mode", in: []string{"--mode", "json"}, err: "--mode"},
		{name: "mode equals", in: []string{"--mode=rpc"}, err: "--mode"},
		{name: "print", in: []string{"-p"}, err: "-p"},
		{name: "session", in: []string{"--session", "x.jsonl"}, err: "--session"},
		{name: "session id", in: []string{"--session-id", "x"}, err: "--session-id"},
		{name: "continue", in: []string{"-c"}, err: "-c"},
		{name: "resume", in: []string{"--resume"}, err: "--resume"},
		{name: "no session", in: []string{"--no-session"}, err: "--no-session"},
		{name: "fork", in: []string{"--fork", "x"}, err: "--fork"},
		{name: "version", in: []string{"--version"}, err: "--version"},
		{name: "file args", in: []string{"@prompt.md"}, err: "@file"},
		{name: "messages", in: []string{"--", "hello"}, err: "--initial-prompt"},
		{name: "subcommand", in: []string{"install", "x"}, err: "management command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, dropped, err := buildArgs(tc.in, "s")
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)
				assert.Error(t, ValidateArgs(tc.in))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.dropped, dropped)
		})
	}
}

func TestNewSessionID(t *testing.T) {
	t.Parallel()
	id := newSessionID()
	assert.Regexp(t, `^agentapi-[0-9a-f]{16}$`, id)
	assert.NotEqual(t, id, newSessionID())
}

func TestAgentEnv(t *testing.T) {
	t.Parallel()
	env := agentEnv([]string{"PATH=/bin", "PI_CODING_AGENT=true", "AI_AGENT=pi", "CLAUDECODE=1", "CODEX_THREAD_ID=x", "PI_CODING_AGENT_DIR=/p", "HOME=/h"})
	assert.Equal(t, []string{"PATH=/bin", "PI_CODING_AGENT_DIR=/p", "HOME=/h"}, env)
}

func TestNewFactoryRejectsArgs(t *testing.T) {
	t.Parallel()
	_, err := NewFactory(Config{Program: "pi", Args: []string{"--mode", "json"}})("")
	require.Error(t, err)
	a, err := NewFactory(Config{Program: "pi"})("ref")
	require.NoError(t, err)
	assert.Equal(t, "ref", a.(*Adapter).resumeRef)
	require.NoError(t, a.Close(), "closing an adapter that never started")
	require.NoError(t, a.Wait())
}

func TestReadStdout(t *testing.T) {
	t.Parallel()
	a, err := newAdapter(Config{}, "", nil)
	require.NoError(t, err)
	a.maxLine = 64
	long := strings.Repeat("x", 200)
	in := "{\"a\":1}\r\n\n{\"u\":\" \"}\n" + long + "\n{\"b\":2}\n{\"c\":3}"
	lines := make(chan []byte, 10)
	a.readStdout(strings.NewReader(in), lines)
	var got []string
	for l := range lines {
		got = append(got, string(l))
	}
	assert.Equal(t, []string{`{"a":1}`, "{\"u\":\" \"}", `{"b":2}`, `{"c":3}`}, got)
}

func TestStartAfterClose(t *testing.T) {
	t.Parallel()
	spawned := false
	a, err := newAdapter(Config{}, "", func(string, []string, string, []string, *slog.Logger) (*transport, error) {
		spawned = true
		return nil, errors.New("unexpected")
	})
	require.NoError(t, err)
	require.NoError(t, a.Close())
	assert.ErrorIs(t, a.Start(t.Context(), &recSink{}), errClosed)
	assert.False(t, spawned)
}
