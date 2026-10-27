package agentio_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/native/procutil"
)

// fakeProc is an agent process that exits once its stdin closes, unless
// it ignores that.
type fakeProc struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderr  string
	// stay keeps the process running after its stdin closes.
	stay bool

	once     sync.Once
	done     chan struct{}
	err      error
	mu       sync.Mutex
	signals  []string
	received chan string
}

func newFakeProc() *fakeProc {
	p := &fakeProc{done: make(chan struct{}), received: make(chan string, 16)}
	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()
	go func() {
		buf := make([]byte, 1<<10)
		for {
			n, err := p.stdinR.Read(buf)
			if n > 0 {
				p.received <- string(buf[:n])
			}
			if err != nil {
				if !p.stay {
					p.exit(nil)
				}
				return
			}
		}
	}()
	return p
}

func (p *fakeProc) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProc) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProc) Wait() error           { <-p.done; return p.err }
func (p *fakeProc) StderrTail() string    { return p.stderr }

func (p *fakeProc) Terminate() error {
	p.signal("TERM")
	p.exit(errors.New("signal: terminated"))
	return nil
}

func (p *fakeProc) Kill() error {
	p.signal("KILL")
	p.exit(errors.New("signal: killed"))
	return nil
}

func (p *fakeProc) signal(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signals = append(p.signals, s)
}

func (p *fakeProc) exit(err error) {
	p.once.Do(func() {
		p.err = err
		_ = p.stdoutW.Close()
		_ = p.stdinR.Close()
		close(p.done)
	})
}

func newHost(start procutil.StartFunc) *agentio.Host {
	return agentio.NewHost(agentio.HostConfig{
		Name:       "Test agent",
		Logger:     slog.New(slog.DiscardHandler),
		Start:      start,
		Writer:     agentio.NewWriter("test"),
		StdinGrace: 10 * time.Millisecond,
		TermGrace:  10 * time.Millisecond,
	})
}

func TestHostLaunchAndClose(t *testing.T) {
	t.Parallel()
	proc := newFakeProc()
	h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
	got, err := h.Launch(procutil.Command{Program: "agent"})
	require.NoError(t, err)
	assert.Equal(t, procutil.Process(proc), got)
	require.NoError(t, h.Writer.Write(context.Background(), "hello"))
	assert.Equal(t, "\"hello\"\n", <-proc.received, "the writer writes to stdin")
	_, err = h.Launch(procutil.Command{Program: "agent"})
	assert.Error(t, err, "a host launches once")

	h.Close(nil)
	<-h.Exited()
	assert.NoError(t, h.ExitErr())
	assert.Empty(t, proc.signals, "it exited when its stdin closed")
	h.Close(nil)
}

func TestHostCloseEscalates(t *testing.T) {
	t.Parallel()
	proc := newFakeProc()
	proc.stay = true
	h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
	_, err := h.Launch(procutil.Command{})
	require.NoError(t, err)
	h.Close(nil)
	assert.Equal(t, []string{"TERM"}, proc.signals)
	assert.EqualError(t, h.ExitErr(), "signal: terminated")
}

// Close interrupts a running turn and waits for it to end before it closes
// stdin; without a turn, or once the grace passed, it goes on.
func TestHostCloseInterrupts(t *testing.T) {
	t.Parallel()
	t.Run("the turn ends", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProc()
		h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
		_, err := h.Launch(procutil.Command{})
		require.NoError(t, err)
		ended, ctxEnded := make(chan struct{}), make(chan struct{})
		closed := make(chan struct{})
		go func() {
			h.Close(func(ctx context.Context) <-chan struct{} {
				go func() {
					<-ctx.Done()
					close(ctxEnded)
				}()
				return ended
			})
			close(closed)
		}()
		select {
		case <-closed:
			t.Fatal("Close didn't wait for the turn")
		case <-time.After(30 * time.Millisecond):
		}
		close(ended)
		<-closed
		select {
		case <-ctxEnded:
		case <-time.After(5 * time.Second):
			t.Fatal("the interrupt's context didn't end")
		}
	})
	t.Run("the grace passes", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProc()
		h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
		h.InterruptGrace = 20 * time.Millisecond
		_, err := h.Launch(procutil.Command{})
		require.NoError(t, err)
		start := time.Now()
		h.Close(func(context.Context) <-chan struct{} { return make(chan struct{}) })
		assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
	})
}

// A Close while the process starts finds none to stop: Launch stops it.
// A Close before keeps it from starting.
func TestHostCloseDuringLaunch(t *testing.T) {
	t.Parallel()
	proc := newFakeProc()
	starting, release := make(chan struct{}), make(chan struct{})
	h := newHost(func(procutil.Command) (procutil.Process, error) {
		close(starting)
		<-release
		return proc, nil
	})
	launched := make(chan error, 1)
	go func() {
		_, err := h.Launch(procutil.Command{})
		launched <- err
	}()
	<-starting
	h.Close(nil)
	close(release)
	require.ErrorIs(t, <-launched, agentio.ErrClosed)
	<-proc.done
	assert.Equal(t, []string{"KILL"}, proc.signals)

	h = newHost(func(procutil.Command) (procutil.Process, error) {
		t.Error("started after Close")
		return nil, errors.New("started")
	})
	h.Close(nil)
	<-h.Exited()
	_, err := h.Launch(procutil.Command{})
	require.ErrorIs(t, err, agentio.ErrClosed)
}

func TestHostStartError(t *testing.T) {
	t.Parallel()
	failed := errors.New("no answer")
	t.Run("the agent exited", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProc()
		proc.stderr = "No conversation found"
		h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
		_, err := h.Launch(procutil.Command{})
		require.NoError(t, err)
		proc.exit(errors.New("exit status 1"))
		assert.EqualError(t, h.StartError(failed, true), "Test agent exited during startup: exit status 1: No conversation found")
	})
	t.Run("a clean exit", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProc()
		proc.stderr = "No conversation found"
		h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
		_, err := h.Launch(procutil.Command{})
		require.NoError(t, err)
		proc.exit(nil)
		assert.EqualError(t, h.StartError(failed, true), "Test agent exited during startup: no answer: No conversation found")
	})
	t.Run("the agent runs", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProc()
		h := newHost(func(procutil.Command) (procutil.Process, error) { return proc, nil })
		_, err := h.Launch(procutil.Command{})
		require.NoError(t, err)
		assert.Equal(t, failed, h.StartError(failed, false))
		<-proc.done
		assert.True(t, h.Closing(), "it is stopped")
	})
	t.Run("the start fails", func(t *testing.T) {
		t.Parallel()
		h := newHost(func(procutil.Command) (procutil.Process, error) { return nil, failed })
		_, err := h.Launch(procutil.Command{})
		require.ErrorIs(t, err, failed)
		<-h.Exited()
		_, err = h.Launch(procutil.Command{})
		require.ErrorIs(t, err, agentio.ErrClosed, "a failed host doesn't start again")
	})
}
