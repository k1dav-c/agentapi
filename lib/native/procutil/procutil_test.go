//go:build unix

package procutil

import (
	"bufio"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startSh(t *testing.T, script string) *process {
	t.Helper()
	p, err := Start(Command{Program: "sh", Args: []string{"-c", script}, Env: os.Environ()})
	require.NoError(t, err)
	proc := p.(*process)
	t.Cleanup(func() {
		// Whatever the test left running in the group.
		_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
		_ = proc.Wait()
	})
	return proc
}

// readAll reads stdout to EOF and fails if that takes longer than limit.
func readAll(t *testing.T, r io.Reader, limit time.Duration) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		done <- string(out)
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(limit):
		t.Fatalf("stdout didn't reach EOF within %s", limit)
		return ""
	}
}

func TestStart(t *testing.T) {
	t.Parallel()
	p := startSh(t, `read line; echo "got $line"; printf '\033[31moops\033[0m\n' >&2; exit 3`)
	_, err := p.Stdin().Write([]byte("hi\n"))
	require.NoError(t, err)

	assert.Equal(t, "got hi\n", readAll(t, p.Stdout(), 5*time.Second))
	err = p.Wait()
	var exitErr interface{ ExitCode() int }
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.ExitCode())
	assert.Equal(t, "oops", p.StderrTail(), "colors are removed")
	assert.EqualError(t, WithStderr(err, p.StderrTail()), "exit status 3: oops")
	assert.NoError(t, WithStderr(nil, "oops"))
	assert.NoError(t, p.Kill(), "signals after the exit are no-ops")
}

func TestStartFailure(t *testing.T) {
	t.Parallel()
	_, err := Start(Command{Program: "/nonexistent/agent"})
	assert.ErrorContains(t, err, "start /nonexistent/agent")
}

// A process the agent started can keep its output open after it exited:
// the output still ends, once everything written was read.
func TestOutputHeldByChild(t *testing.T) {
	t.Parallel()
	p := startSh(t, `echo first; sleep 30 & echo last; exit 0`)
	start := time.Now()
	assert.Equal(t, "first\nlast\n", readAll(t, p.Stdout(), OutputGrace+5*time.Second))
	assert.Less(t, time.Since(start), OutputGrace+3*time.Second)
	assert.NoError(t, p.Wait(), "a clean exit stays clean while stderr is held open")
}

// Output written before the reader comes back for it isn't lost to the
// grace period.
func TestOutputReadLate(t *testing.T) {
	t.Parallel()
	p := startSh(t, `echo early; sleep 30 & exit 0`)
	require.NoError(t, p.Wait())
	time.Sleep(OutputGrace + 500*time.Millisecond)
	assert.Equal(t, "early\n", readAll(t, p.Stdout(), OutputGrace+5*time.Second))
}

func TestTerminateStopsGroup(t *testing.T) {
	t.Parallel()
	p := startSh(t, `sleep 30 & echo $!; wait`)
	reader := bufio.NewReader(p.Stdout())
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	_, err = strconv.Atoi(strings.TrimSpace(line))
	require.NoError(t, err)

	require.NoError(t, p.Terminate())
	assert.Error(t, p.Wait())
	// The child holding stdout got SIGTERM too, so stdout ends right away
	// instead of after the grace period.
	start := time.Now()
	readAll(t, reader, OutputGrace+5*time.Second)
	assert.Less(t, time.Since(start), OutputGrace)
}

func TestStop(t *testing.T) {
	t.Parallel()

	t.Run("stdin", func(t *testing.T) {
		t.Parallel()
		p := startSh(t, `cat >/dev/null; exit 0`)
		Stop(p, p.exited, StopOptions{StdinGrace: time.Minute})
		assert.NoError(t, p.Wait())
	})

	t.Run("sigterm", func(t *testing.T) {
		t.Parallel()
		p := startSh(t, `while :; do sleep 0.1; done`)
		Stop(p, p.exited, StopOptions{StdinGrace: 50 * time.Millisecond, TermGrace: time.Minute})
		assert.ErrorContains(t, p.Wait(), "terminated")
	})

	t.Run("sigkill", func(t *testing.T) {
		t.Parallel()
		p := startSh(t, `trap '' TERM; while :; do sleep 0.1; done`)
		Stop(p, p.exited, StopOptions{StdinGrace: 50 * time.Millisecond, TermGrace: 50 * time.Millisecond})
		assert.ErrorContains(t, p.Wait(), "killed")
	})
}

func TestOutput(t *testing.T) {
	t.Parallel()

	t.Run("limit", func(t *testing.T) {
		t.Parallel()
		out, err := Output(context.Background(), Command{Program: "sh", Args: []string{"-c", "echo hello; echo world"}}, 7)
		require.NoError(t, err)
		assert.Equal(t, "hello\nw", string(out))
	})

	t.Run("cancel stops the group", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := Output(ctx, Command{Program: "sh", Args: []string{"-c", "sleep 30 & sleep 30"}}, 10)
		assert.Error(t, err)
		assert.Less(t, time.Since(start), 5*time.Second)
	})
}
