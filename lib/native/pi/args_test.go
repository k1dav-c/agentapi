package pi

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/native/procutil"

	"github.com/coder/agentapi/lib/native"
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
		{
			name:    "TUI flag before an option",
			in:      []string{"--tui-mode", "--model", "m"},
			want:    []string{"--mode", "rpc", "--session-id", "s", "--model", "m"},
			dropped: []string{"--tui-mode"},
		},
		{name: "value that looks like a file", in: []string{"--name", "@home"}, want: []string{"--mode", "rpc", "--session-id", "s", "--name", "@home"}},
		{name: "value that looks like an option", in: []string{"--system-prompt", "-be brief"}, want: []string{"--mode", "rpc", "--session-id", "s", "--system-prompt", "-be brief"}},
		{
			name: "extension flag with a value",
			in:   []string{"--plan", "strict", "--model", "m"},
			want: []string{"--mode", "rpc", "--session-id", "s", "--plan", "strict", "--model", "m"},
		},
		{
			name: "extension flag before an option",
			in:   []string{"--plan", "--model", "m"},
			want: []string{"--mode", "rpc", "--session-id", "s", "--plan", "--model", "m"},
		},
		{name: "short option", in: []string{"-x"}, want: []string{"--mode", "rpc", "--session-id", "s", "-x"}},
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
		{name: "file after an extension flag", in: []string{"--plan", "@prompt.md"}, err: "@file"},
		{name: "messages", in: []string{"--", "hello"}, err: "--initial-prompt"},
		{name: "message", in: []string{"fix the build"}, err: "--initial-prompt"},
		{name: "message after a switch", in: []string{"--approve", "fix the build"}, err: "--initial-prompt"},
		{name: "message after a value", in: []string{"--model", "m", "fix the build"}, err: "--initial-prompt"},
		{name: "subcommand", in: []string{"install", "x"}, err: "management command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kept, dropped, err := filterArgs(tc.in)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)
				_, err = NewFactory(Config{Args: tc.in})
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, rpcArgs(kept, "s"))
			assert.Equal(t, tc.dropped, dropped)
		})
	}
}

func TestNewSessionID(t *testing.T) {
	t.Parallel()
	id := NewSessionID()
	assert.Regexp(t, `^agentapi-[0-9a-f]{16}$`, id)
	assert.NotEqual(t, id, NewSessionID())
}

func TestNewFactoryRejectsArgs(t *testing.T) {
	t.Parallel()
	_, err := NewFactory(Config{Program: "pi", Args: []string{"--mode", "json"}})
	require.Error(t, err)
	factory, err := NewFactory(Config{Program: "pi"})
	require.NoError(t, err)
	a, err := factory(native.Resume{Ref: "ref"})
	require.NoError(t, err)
	assert.Equal(t, "ref", a.(*Adapter).resumeRef)
	require.NoError(t, a.Close(), "closing an adapter that never started")
	require.NoError(t, a.Wait())
}

func TestStartAfterClose(t *testing.T) {
	t.Parallel()
	spawned := false
	a := newAdapter(Config{StartProcess: func(procutil.Command) (procutil.Process, error) {
		spawned = true
		return nil, errors.New("unexpected")
	}}, nil, "")
	require.NoError(t, a.Close())
	assert.ErrorIs(t, a.Start(t.Context(), nativetest.NewSink(t)), agentio.ErrClosed)
	assert.False(t, spawned)
}
