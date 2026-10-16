// Package pi drives the Pi coding agent through its RPC mode
// (`pi --mode rpc`): JSON commands on stdin, JSON events and responses on
// stdout, one object per line.
package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/native"
)

// Config configures the Pi adapter.
type Config struct {
	// Program is the Pi executable, usually "pi".
	Program string
	// Args are the user's arguments after the program. Session and mode
	// options are rejected (see ValidateArgs); the adapter adds its own.
	Args []string
	// CWD is the working directory of the agent.
	CWD string
	// Env is the agent's environment; nil means this process's. Variables
	// leaked from a parent agent session are removed either way.
	Env    []string
	Logger *slog.Logger
}

// NewFactory returns a factory for Pi adapters. A non-empty resumeRef is a
// Pi session ID reported earlier with Sink.SessionRef; the new process
// continues that session.
func NewFactory(cfg Config) native.Factory {
	return func(resumeRef string) (native.Adapter, error) {
		return newAdapter(cfg, resumeRef, spawnProcess)
	}
}

// Timeouts of the shutdown sequence.
const (
	// abortGrace is how long Close waits for a running turn to stop
	// before it closes stdin.
	abortGrace = 3 * time.Second
	// stdinGrace is how long Pi gets to exit after stdin closes.
	stdinGrace = 3 * time.Second
	// termGrace is how long Pi gets after SIGTERM before SIGKILL.
	termGrace = 2 * time.Second
	// snapshotInterval is the shortest time between two snapshots of a
	// message that is streaming.
	snapshotInterval = 100 * time.Millisecond
	// maxLineBytes bounds one stdout record (images make them large).
	maxLineBytes = 64 << 20
)

var (
	// errExited is returned for operations after Pi exited.
	errExited = errors.New("pi exited")
	// errClosed is returned by Start after Close.
	errClosed = errors.New("the adapter was closed")
)

// cmdKind is what a command waiting for its response was for.
type cmdKind int

const (
	cmdInit cmdKind = iota
	cmdTurn
	cmdSteer
	cmdAbort
)

// cmdResult is the outcome of a command.
type cmdResult struct {
	err         error
	disposition string
	data        json.RawMessage
}

// pendingCmd is a command waiting for its response.
type pendingCmd struct {
	kind cmdKind
	// done receives the result once; buffered.
	done chan cmdResult
	// detached: the caller no longer waits for the response, because the
	// loop reported the prompt accepted (a run or a dialog started) or the
	// caller gave up. The loop then handles the response alone.
	detached bool
}

// Adapter is a native.Adapter for Pi.
type Adapter struct {
	cfg       Config
	logger    *slog.Logger
	resumeRef string
	spawn     spawnFunc

	// Shutdown timeouts, shortened by tests.
	abortGrace, stdinGrace, termGrace time.Duration
	interval                          time.Duration
	maxLine                           int
	// lineHandled, if set, is called after each stdout record is handled.
	lineHandled func()

	writeMu sync.Mutex
	stdin   io.WriteCloser
	proc    process

	mu         sync.Mutex
	started    bool
	exited     bool
	closing    bool
	nextID     int
	pending    map[string]*pendingCmd
	dialogs    map[string]*dialog
	turnActive bool
	runActive  bool
	// interrupted is set by Interrupt and cleared when the turn ends.
	interrupted bool
	caps        native.Capabilities

	// timers posts timer events to the loop.
	timers chan func()
	// loopDone is closed when the event loop has stopped.
	loopDone chan struct{}
	// procDone is closed when the process has exited.
	procDone chan struct{}
	exitErr  error

	closeOnce sync.Once
	doneOnce  sync.Once

	// Loop state; only the event loop goroutine touches it.
	loop loopState
}

var _ native.Adapter = (*Adapter)(nil)

func newAdapter(cfg Config, resumeRef string, spawn spawnFunc) (*Adapter, error) {
	if err := ValidateArgs(cfg.Args); err != nil {
		return nil, err
	}
	if cfg.Program == "" {
		cfg.Program = "pi"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		cfg:        cfg,
		logger:     logger.With("agent", "pi"),
		resumeRef:  resumeRef,
		spawn:      spawn,
		abortGrace: abortGrace,
		stdinGrace: stdinGrace,
		termGrace:  termGrace,
		interval:   snapshotInterval,
		maxLine:    maxLineBytes,
		pending:    make(map[string]*pendingCmd),
		dialogs:    make(map[string]*dialog),
		caps:       native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true},
		timers:     make(chan func()),
		loopDone:   make(chan struct{}),
		procDone:   make(chan struct{}),
	}, nil
}

// Start launches Pi and waits for its first get_state response, which
// says the session is open: RPC mode has no ready message.
func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	sessionID := a.resumeRef
	if sessionID == "" {
		sessionID = newSessionID()
	}
	args, dropped, err := buildArgs(a.cfg.Args, sessionID)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		a.logger.Warn("Dropped Pi options that only affect its terminal UI", "args", dropped)
	}
	a.logger.Info("Starting Pi in RPC mode", "program", a.cfg.Program, "args", args, "resume", a.resumeRef != "")
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		return errClosed
	}
	t, err := a.spawn(a.cfg.Program, args, a.cfg.CWD, agentEnv(a.cfg.Env), a.logger)
	if err != nil {
		a.markDone(err)
		return err
	}
	a.writeMu.Lock()
	a.stdin = t.stdin
	a.writeMu.Unlock()
	a.mu.Lock()
	closing = a.closing
	if !closing {
		a.proc = t.proc
	}
	a.mu.Unlock()
	if closing {
		// Close ran while Pi was starting and found no process to stop.
		_ = t.stdin.Close()
		_ = t.proc.Kill()
		return errClosed
	}
	a.loop = newLoopState(sink, a.cfg.CWD)

	lines := make(chan []byte, 16)
	go a.readStdout(t.stdout, lines)
	go a.eventLoop(lines)
	go func() {
		err := t.proc.Wait()
		// Let the loop handle what Pi wrote before it exited.
		select {
		case <-a.loopDone:
		case <-time.After(2 * time.Second):
		}
		if err != nil {
			if tail := t.proc.StderrTail(); tail != "" {
				err = fmt.Errorf("%w; stderr: %s", err, tail)
			}
		}
		a.markDone(err)
	}()

	res, err := a.request(ctx, cmdInit, simpleCommand{Type: "get_state"})
	if err != nil {
		return fmt.Errorf("pi didn't open its session: %w", err)
	}
	if res.err != nil {
		return fmt.Errorf("pi didn't open its session: %w", res.err)
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}

// markDone records the exit and makes Wait return.
func (a *Adapter) markDone(err error) {
	a.doneOnce.Do(func() {
		a.exitErr = err
		close(a.procDone)
	})
}

// readStdout splits stdout on "\n" only: Pi's JSON can contain U+2028.
func (a *Adapter) readStdout(r io.Reader, lines chan<- []byte) {
	defer close(lines)
	reader := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	dropping := false
	for {
		chunk, err := reader.ReadSlice('\n')
		switch {
		case dropping:
		case len(buf)+len(chunk) > a.maxLine:
			dropping = true
			buf = nil
			a.logger.Warn("Dropped an oversized Pi record")
		default:
			buf = append(buf, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if line := bytes.TrimRight(buf, "\r\n"); !dropping && len(bytes.TrimSpace(line)) > 0 {
			lines <- line
		}
		buf, dropping = nil, false
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				a.logger.Debug("Pi stdout closed", "error", err)
			}
			return
		}
	}
}

// send writes one command line.
func (a *Adapter) send(cmd any) error {
	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.stdin == nil {
		return native.ErrNotStarted
	}
	if _, err := a.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write to pi: %w", err)
	}
	return nil
}

// newID returns a command id.
func (a *Adapter) newIDLocked() string {
	a.nextID++
	return fmt.Sprintf("agentapi-%d", a.nextID)
}

// withID sets the id of a command.
func withID(cmd any, id string) any {
	switch c := cmd.(type) {
	case simpleCommand:
		c.ID = id
		return c
	case promptCommand:
		c.ID = id
		return c
	}
	return cmd
}

// request sends a command and waits for its response, or for the loop to
// report it accepted.
func (a *Adapter) request(ctx context.Context, kind cmdKind, cmd any) (cmdResult, error) {
	a.mu.Lock()
	if a.exited {
		a.mu.Unlock()
		return cmdResult{}, errExited
	}
	id := a.newIDLocked()
	p := &pendingCmd{kind: kind, done: make(chan cmdResult, 1)}
	a.pending[id] = p
	a.mu.Unlock()

	if err := a.send(withID(cmd, id)); err != nil {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return cmdResult{}, err
	}
	select {
	case res := <-p.done:
		return res, nil
	case <-ctx.Done():
		// A late response is handled by the loop without a caller.
		a.mu.Lock()
		p.detached = true
		a.mu.Unlock()
		return cmdResult{}, ctx.Err()
	}
}

// StartTurn sends the message as a prompt. It returns once Pi started the
// run, or an extension command started and opened a dialog, or Pi
// answered (an extension command that finished at once).
func (a *Adapter) StartTurn(ctx context.Context, input native.Input) error {
	if err := a.ready(); err != nil {
		return err
	}
	images, err := readImages(input.Images)
	if err != nil {
		return err
	}
	res, err := a.request(ctx, cmdTurn, promptCommand{Type: "prompt", Message: input.Text, Images: images})
	if err != nil {
		return err
	}
	if res.err != nil {
		if strings.Contains(res.err.Error(), "already processing") {
			return native.ErrTurnActive
		}
		return res.err
	}
	return nil
}

// Steer sends the message as a prompt with streamingBehavior "steer". Pi
// adds it to the running run or, if the run just ended, starts a new one,
// so the message is never left queued.
func (a *Adapter) Steer(ctx context.Context, input native.Input) error {
	if err := a.ready(); err != nil {
		return err
	}
	images, err := readImages(input.Images)
	if err != nil {
		return err
	}
	res, err := a.request(ctx, cmdSteer, promptCommand{Type: "prompt", Message: input.Text, Images: images, StreamingBehavior: "steer"})
	if err != nil {
		return err
	}
	return res.err
}

// Interrupt sends abort. The turn ends once Pi has settled; the loop then
// reports it with StopReason "interrupted".
func (a *Adapter) Interrupt(context.Context) error {
	if err := a.ready(); err != nil {
		return err
	}
	a.mu.Lock()
	if !a.turnActive {
		a.mu.Unlock()
		return native.ErrNoActiveTurn
	}
	a.interrupted = true
	id := a.newIDLocked()
	a.pending[id] = &pendingCmd{kind: cmdAbort, done: make(chan cmdResult, 1)}
	a.mu.Unlock()
	return a.send(simpleCommand{ID: id, Type: "abort"})
}

// Respond answers an extension dialog.
func (a *Adapter) Respond(_ context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	if a.exited {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	d := a.dialogs[requestID]
	if d == nil {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	reply, err := d.reply(response)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	delete(a.dialogs, requestID)
	a.mu.Unlock()
	return a.send(reply)
}

// Capabilities reports steer, interrupt, resume and images (when the model
// accepts images).
func (a *Adapter) Capabilities() native.Capabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.caps
}

// Wait returns when Pi has exited.
func (a *Adapter) Wait() error {
	<-a.procDone
	return a.exitErr
}

// Close stops Pi: it aborts a running turn (closing stdin mid-turn leaves
// the session with a tool call without result), closes stdin, then sends
// SIGTERM and SIGKILL to the process group if Pi doesn't exit.
func (a *Adapter) Close() error {
	a.closeOnce.Do(a.shutdown)
	<-a.procDone
	return nil
}

func (a *Adapter) shutdown() {
	a.mu.Lock()
	a.closing = true
	proc := a.proc
	busy := (a.turnActive || a.runActive) && !a.exited
	var abort *pendingCmd
	var abortID string
	if busy {
		a.interrupted = true
		abortID = a.newIDLocked()
		abort = &pendingCmd{kind: cmdAbort, done: make(chan cmdResult, 1)}
		a.pending[abortID] = abort
	}
	a.mu.Unlock()
	if proc == nil {
		a.markDone(nil)
		return
	}
	if abort != nil {
		if err := a.send(simpleCommand{ID: abortID, Type: "abort"}); err == nil {
			select {
			case <-abort.done:
			case <-a.procDone:
			case <-time.After(a.abortGrace):
				a.logger.Warn("Pi didn't stop its turn in time; closing it anyway")
			}
		}
	}
	a.writeMu.Lock()
	if a.stdin != nil {
		_ = a.stdin.Close()
	}
	a.writeMu.Unlock()
	select {
	case <-a.procDone:
		return
	case <-time.After(a.stdinGrace):
	}
	a.logger.Warn("Pi didn't exit after stdin closed; sending SIGTERM")
	_ = proc.Terminate()
	select {
	case <-a.procDone:
		return
	case <-time.After(a.termGrace):
	}
	a.logger.Warn("Pi didn't exit after SIGTERM; sending SIGKILL")
	_ = proc.Kill()
}

// ready returns an error until Start has completed, and after Pi exited.
func (a *Adapter) ready() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exited {
		return errExited
	}
	if !a.started {
		return native.ErrNotStarted
	}
	return nil
}

// readImages reads the attached image files for a prompt.
func readImages(images []native.Image) ([]imageContent, error) {
	var out []imageContent
	for _, img := range images {
		data, err := os.ReadFile(img.Path)
		if err != nil {
			return nil, fmt.Errorf("read image: %w", err)
		}
		out = append(out, imageContent{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: img.MediaType})
	}
	return out, nil
}
