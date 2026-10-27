// Package pi drives the Pi coding agent through its RPC mode
// (`pi --mode rpc`): JSON commands on stdin, JSON events and responses on
// stdout, one object per line.
package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/procenv"
)

// Config configures the Pi adapter.
type Config struct {
	// Program is the Pi executable, usually "pi".
	Program string
	// Args are the user's arguments after the program. Session and mode
	// options are rejected (see NewFactory); the adapter adds its own.
	Args []string
	// CWD is the working directory of the agent.
	CWD string
	// Env is the agent's environment; nil means this process's. Variables
	// leaked from a parent agent session are removed either way.
	Env    []string
	Logger *slog.Logger
	// StartProcess starts Pi; nil runs Program with procutil.Start.
	StartProcess procutil.StartFunc
	// Clock is the real clock when nil.
	Clock quartz.Clock
}

// NewFactory returns a factory for Pi adapters, or an error for options
// the native transport can't pass on. A resume ref is a Pi session ID
// reported earlier with Sink.SessionRef; the new process continues that
// session, or with Fork, a copy of it under a new ID.
func NewFactory(cfg Config) (native.Factory, error) {
	args, dropped, err := filterArgs(cfg.Args)
	if err != nil {
		return nil, err
	}
	if len(dropped) > 0 {
		cfg.logger().Warn("Dropped Pi options that only affect its terminal UI", "args", dropped)
	}
	return func(resume native.Resume) (native.Adapter, error) {
		a := newAdapter(cfg, args, resume.Ref)
		a.fork = resume.Fork && resume.Ref != ""
		return a, nil
	}, nil
}

func (cfg Config) logger() *slog.Logger {
	if cfg.Logger == nil {
		return slog.Default()
	}
	return cfg.Logger
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
)

// Other timeouts and limits.
const (
	// exitInfoWait is how long a failed start waits for Pi's exit status.
	exitInfoWait = 3 * time.Second
	// interruptTimeout is how long an interrupted turn waits for Pi to end
	// it before the adapter ends it.
	interruptTimeout = 10 * time.Second
	// snapshotInterval is the shortest time between two snapshots of a
	// message that is streaming.
	snapshotInterval = 100 * time.Millisecond
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
	cmdTurn cmdKind = iota
	cmdSteer
	cmdAbort
	// cmdQuery only wants the response.
	cmdQuery
)

// cmdResult is the outcome of a command.
type cmdResult struct {
	err  error
	data json.RawMessage
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
	// turn is the turn the loop last found the prompt under way in (see
	// acceptPrompt), or 0.
	turn int
}

// Adapter is a native.Adapter for Pi.
type Adapter struct {
	cfg Config
	// args are the user's arguments passed on to Pi.
	args []string
	log  *slog.Logger
	// drift counts what Pi sends that the adapter doesn't read.
	drift     *agentio.Drift
	resumeRef string
	// fork opens a copy of resumeRef's session under a new ID.
	fork  bool
	start procutil.StartFunc

	// The timeouts and limits of the constants above, and the clock of
	// the snapshot, dialog and interrupt timers; tests change them.
	abortGrace, stdinGrace, termGrace time.Duration
	interruptTimeout                  time.Duration
	interval                          time.Duration
	maxLine                           int
	clock                             quartz.Clock
	// lineHandled, if set, is called after each stdout record is handled.
	lineHandled func()

	// writer writes Pi's commands, once Start gave it the stdin.
	writer *agentio.Writer
	proc   procutil.Process

	mu         sync.Mutex
	started    bool
	exited     bool
	closing    bool
	nextID     int
	pending    map[string]*pendingCmd
	dialogs    map[string]*dialog
	turnActive bool
	runActive  bool
	// turnSeq numbers the turns begun; it tells a late response or timer
	// whether the turn it is about still runs.
	turnSeq int
	// interrupted is set by Interrupt and cleared when the turn ends.
	interrupted bool
	caps        native.Capabilities
	// settings are Pi's model and thinking level; models are its models,
	// nil until listed. See settings.go.
	settings native.Settings
	models   []native.ModelOption
	// applying counts the ApplySettings calls under way; the conversation
	// reads the settings when they return, so the loop doesn't report the
	// half-applied ones meanwhile.
	applying int
	// contextWindow is the context window of the session's model.
	contextWindow int64

	// posted carries functions to run on the event loop (see post).
	posted chan func()
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

var (
	_ native.Adapter      = (*Adapter)(nil)
	_ native.Configurable = (*Adapter)(nil)
	_ native.Compacter    = (*Adapter)(nil)
)

// newAdapter creates an adapter that passes args (filtered by filterArgs)
// on to Pi.
func newAdapter(cfg Config, args []string, resumeRef string) *Adapter {
	if cfg.Program == "" {
		cfg.Program = "pi"
	}
	if cfg.StartProcess == nil {
		cfg.StartProcess = procutil.Start
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	log := cfg.logger().With("agent", "pi")
	return &Adapter{
		cfg:              cfg,
		args:             args,
		log:              log,
		drift:            agentio.NewDrift("Pi", log),
		resumeRef:        resumeRef,
		start:            cfg.StartProcess,
		abortGrace:       abortGrace,
		stdinGrace:       stdinGrace,
		termGrace:        termGrace,
		interruptTimeout: interruptTimeout,
		interval:         snapshotInterval,
		maxLine:          agentio.MaxLine,
		writer:           agentio.NewWriter("pi"),
		clock:            cfg.Clock,
		pending:          make(map[string]*pendingCmd),
		dialogs:          make(map[string]*dialog),
		caps:             native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true, Settings: true, Compact: true},
		posted:           make(chan func()),
		loopDone:         make(chan struct{}),
		procDone:         make(chan struct{}),
	}
}

// Start launches Pi and waits for its first get_state response, which
// says the session is open: RPC mode has no ready message. Without a model
// provider it returns a native.LoginRequiredError.
func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	sessionID := a.resumeRef
	if sessionID == "" || a.fork {
		sessionID = NewSessionID()
	}
	args := rpcArgs(a.args, sessionID)
	if a.fork {
		args = slices.Insert(args, 4, "--fork", a.resumeRef)
	}
	// The arguments can hold secrets (--api-key).
	a.log.Info("Starting Pi in RPC mode", "program", a.cfg.Program, "resume", a.resumeRef != "")
	a.log.Debug("Pi command line", "args", args)
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		return errClosed
	}
	proc, err := a.start(procutil.Command{
		Program: a.cfg.Program,
		Args:    args,
		Dir:     a.cfg.CWD,
		Env:     procenv.ForAgent(a.cfg.Env),
		Logger:  a.log,
	})
	if err != nil {
		a.markDone(err)
		return err
	}
	a.writer.Attach(proc.Stdin())
	a.mu.Lock()
	closing = a.closing
	if !closing {
		a.proc = proc
	}
	a.mu.Unlock()
	if closing {
		// Close ran while Pi was starting and found no process to stop.
		_ = proc.Stdin().Close()
		_ = proc.Kill()
		return errClosed
	}
	a.loop = newLoopState(sink, a.cfg.CWD)

	lines := make(chan []byte, 16)
	go func() {
		defer close(lines)
		agentio.ReadLines(proc.Stdout(), "pi", a.maxLine, a.log, func(line []byte) { lines <- line })
	}()
	go a.eventLoop(lines)
	go func() {
		err := proc.Wait()
		// Let the loop handle what Pi wrote before it exited.
		select {
		case <-a.loopDone:
		case <-time.After(procutil.OutputGrace):
		}
		a.markDone(procutil.WithStderr(err, proc.StderrTail()))
	}()

	res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_state"})
	if err == nil {
		err = res.err
	}
	if err != nil {
		return a.startError(err, proc)
	}
	var state sessionState
	if err := a.drift.Decode("get_state response", res.data, &state); err != nil {
		// Not ours to judge: the session opens without what it says.
		a.log.Warn("Malformed Pi session state", "error", err)
	} else if !state.hasModel() {
		// The session is reported once Pi may start, so a start that
		// waits for a login doesn't report one each time it is tried
		// again.
		if err := a.checkLogin(ctx); err != nil {
			return err
		}
	}
	// Before Start returns, so the conversation finds the session ref and
	// capabilities.
	if !a.runOnLoop(func() { a.handleState(state) }) {
		return a.startError(errExited, proc)
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}

// startError explains why Pi didn't open its session. When it exited, its
// exit error and stderr say why (a broken extension, a bad option).
func (a *Adapter) startError(err error, proc procutil.Process) error {
	if errors.Is(err, errExited) {
		select {
		case <-a.procDone:
			if a.exitErr != nil {
				return fmt.Errorf("pi exited during startup: %w", a.exitErr)
			}
			if tail := proc.StderrTail(); tail != "" {
				return fmt.Errorf("pi exited during startup: %w: %s", err, tail)
			}
		case <-time.After(exitInfoWait):
		}
	}
	return fmt.Errorf("pi didn't open its session: %w", err)
}

// markDone records the exit and makes Wait return.
func (a *Adapter) markDone(err error) {
	a.doneOnce.Do(func() {
		a.exitErr = err
		close(a.procDone)
	})
}

// send writes one command line, unless ctx ends first (see
// agentio.Writer.Write).
func (a *Adapter) send(ctx context.Context, cmd any) error {
	return a.writer.Write(ctx, cmd)
}

// addPendingLocked registers a command that waits for its response under
// a new id.
func (a *Adapter) addPendingLocked(kind cmdKind) (string, *pendingCmd) {
	a.nextID++
	id := fmt.Sprintf("agentapi-%d", a.nextID)
	p := &pendingCmd{kind: kind, done: make(chan cmdResult, 1)}
	a.pending[id] = p
	return id, p
}

// request sends a command and waits for its response, or for the loop to
// report it accepted. A command that was sent and got neither fails with
// native.ErrUnconfirmed (as the error or the result's): Pi may carry it out
// all the same.
func (a *Adapter) request(ctx context.Context, kind cmdKind, cmd command) (cmdResult, error) {
	a.mu.Lock()
	if a.exited {
		a.mu.Unlock()
		return cmdResult{}, errExited
	}
	id, p := a.addPendingLocked(kind)
	a.mu.Unlock()

	if err := a.send(ctx, cmd.withID(id)); err != nil {
		a.mu.Lock()
		if errors.Is(err, native.ErrUnconfirmed) {
			// Pi may yet get it: the loop handles a late response
			// without a caller.
			p.detached = true
		} else {
			delete(a.pending, id)
		}
		a.mu.Unlock()
		return cmdResult{}, err
	}
	select {
	case res := <-p.done:
		if errors.Is(res.err, errExited) {
			// Pi exited before it answered.
			res.err = native.Unconfirmed(res.err)
		}
		return res, nil
	case <-ctx.Done():
		// A late response is handled by the loop without a caller.
		a.mu.Lock()
		p.detached = true
		a.mu.Unlock()
		return cmdResult{}, native.Unconfirmed(ctx.Err())
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
// reports it with StopReason "interrupted". If Pi doesn't end the turn
// within interruptTimeout (an extension command that doesn't stop), the
// adapter ends it.
func (a *Adapter) Interrupt(ctx context.Context) error {
	if err := a.ready(); err != nil {
		return err
	}
	a.mu.Lock()
	if !a.turnActive {
		a.mu.Unlock()
		return native.ErrNoActiveTurn
	}
	first := !a.interrupted
	a.interrupted = true
	turn := a.turnSeq
	id, _ := a.addPendingLocked(cmdAbort)
	a.mu.Unlock()
	if err := a.send(ctx, simpleCommand{ID: id, Type: "abort"}); err != nil {
		a.mu.Lock()
		delete(a.pending, id)
		if first && a.turnSeq == turn {
			// The turn wasn't interrupted after all.
			a.interrupted = false
		}
		a.mu.Unlock()
		return err
	}
	a.clock.AfterFunc(a.interruptTimeout, func() {
		a.post(func() { a.endInterruptedTurn(turn) })
	})
	return nil
}

// Respond answers an extension dialog.
func (a *Adapter) Respond(ctx context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	d := a.dialogs[requestID]
	if a.exited || d == nil || d.answering {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	reply, err := d.reply(response)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	d.answering = true
	a.mu.Unlock()
	err = a.send(ctx, reply)
	a.mu.Lock()
	switch {
	case err == nil:
		d.answering = false
		if a.dialogs[requestID] == d {
			d.stopTimer()
			delete(a.dialogs, requestID)
		}
	case errors.Is(err, native.ErrUnconfirmed):
		// Pi may yet get the answer: the dialog stays answering until Pi
		// closes it or the turn ends.
		d.unconfirmed = true
	default:
		d.answering = false
	}
	a.mu.Unlock()
	return err
}

// Capabilities reports steer, interrupt, resume and images. Images are
// left out when Pi says its model doesn't take them.
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
		abortID, abort = a.addPendingLocked(cmdAbort)
	}
	a.mu.Unlock()
	if proc == nil {
		a.markDone(nil)
		return
	}
	if abort != nil {
		// A write that is stuck (Pi stopped reading) must not keep Close
		// from going on: the wait is bounded, and Stop's closing stdin
		// makes the write fail.
		go func() {
			if err := a.send(context.Background(), simpleCommand{ID: abortID, Type: "abort"}); err != nil {
				a.mu.Lock()
				delete(a.pending, abortID)
				a.mu.Unlock()
				deliverResult(abort, cmdResult{err: err})
			}
		}()
		select {
		case <-abort.done:
		case <-a.procDone:
		case <-time.After(a.abortGrace):
			a.log.Warn("Pi didn't stop its turn in time; closing it anyway")
		}
	}
	procutil.Stop(proc, a.procDone, procutil.StopOptions{
		StdinGrace: a.stdinGrace,
		TermGrace:  a.termGrace,
		Logger:     a.log,
		Name:       "Pi",
	})
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
		data, err := img.Base64()
		if err != nil {
			return nil, err
		}
		out = append(out, imageContent{Type: "image", Data: data, MimeType: img.MediaType})
	}
	return out, nil
}
