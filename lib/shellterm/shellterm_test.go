//go:build unix

package shellterm

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readUntil reads output until it contains want.
func readUntil(t *testing.T, output <-chan []byte, got *bytes.Buffer, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(got.String(), want) {
		select {
		case data, ok := <-output:
			if !ok {
				t.Fatalf("output ended before %q; got %q", want, got.String())
			}
			got.Write(data)
		case <-deadline:
			t.Fatalf("no %q in %q", want, got.String())
		}
	}
}

func newTerminal(t *testing.T) *Terminal {
	t.Helper()
	term := New(Config{
		Shell:  "/bin/sh",
		Dir:    t.TempDir(),
		Env:    []string{"PATH=" + os.Getenv("PATH"), "PS1=$ "},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(func() { _ = term.Close() })
	return term
}

func TestTerminal(t *testing.T) {
	t.Parallel()
	term := newTerminal(t)
	require.ErrorIs(t, term.Write([]byte("x")), ErrNotRunning, "nothing runs before someone watches")

	history, output, stop, err := term.Watch()
	require.NoError(t, err)
	assert.Empty(t, history)
	require.NoError(t, term.Resize(120, 40))
	require.NoError(t, term.Write([]byte("echo \"size $(stty size)\"; pwd\r")))
	var got bytes.Buffer
	readUntil(t, output, &got, "size 40 120")

	// A second watcher gets what was shown so far, then the same output.
	history2, output2, stop2, err := term.Watch()
	require.NoError(t, err)
	assert.Contains(t, string(history2), "size 40 120")
	require.NoError(t, term.Write([]byte("echo second\r")))
	var got2 bytes.Buffer
	readUntil(t, output2, &got2, "second")
	stop2()
	for range output2 {
		// What was sent before the stop; the channel is closed after it.
	}

	// The shell exits: the watch ends; the next watch starts a new shell.
	require.NoError(t, term.Write([]byte("exit 3\r")))
	readUntil(t, output, &got, "exited with status 3")
	for range output {
	}
	stop()
	history, output, stop, err = term.Watch()
	require.NoError(t, err)
	defer stop()
	assert.Contains(t, string(history), "exited with status 3")
	require.NoError(t, term.Write([]byte("echo again\r")))
	got.Reset()
	readUntil(t, output, &got, "again")
}

func TestTerminalClose(t *testing.T) {
	t.Parallel()
	term := newTerminal(t)
	_, output, _, err := term.Watch()
	require.NoError(t, err)
	// A job that ignores the hangup is killed with the shell's group.
	require.NoError(t, term.Write([]byte("trap '' HUP; sleep 60 & echo started\r")))
	var got bytes.Buffer
	readUntil(t, output, &got, "started")
	done := make(chan struct{})
	go func() {
		_ = term.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close didn't return")
	}
	_, _, _, err = term.Watch()
	assert.ErrorIs(t, err, ErrClosed)
}

func TestScrollbackStartsAtALine(t *testing.T) {
	t.Parallel()
	term := New(Config{Scrollback: 10})
	term.appendLocked([]byte("first line\nsecond\n"))
	assert.Equal(t, "second\n", string(term.history))
}
