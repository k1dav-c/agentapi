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

// Timeouts.
const (
	// interruptTimeout is how long an interrupted turn waits for Pi to end
	// it before the adapter ends it.
	interruptTimeout = agentio.InterruptTimeout
	// snapshotInterval is the shortest time between two snapshots of a
	// message that is streaming.
	snapshotInterval = agentio.SnapshotInterval
)

// errExited is returned for operations after Pi exited.
var errExited = errors.New("pi exited")

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
	fork bool

	// The timeouts and limits of the constants above, and the clock of
	// the snapshot, dialog and interrupt timers; tests change them.
	interruptTimeout time.Duration
	interval         time.Duration
	clock            quartz.Clock
	// lineHandled, if set, is called after each stdout record is handled.
	lineHandled func()

	// writer writes Pi's commands, once Start gave it the stdin.
	writer *agentio.Writer
	// host runs Pi and the event loop.
	host *agentio.Host

	mu         sync.Mutex
	started    bool
	exited     bool
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

	// loopDone is closed when the event loop has stopped.
	loopDone <-chan struct{}

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
	a := &Adapter{
		cfg:              cfg,
		args:             args,
		log:              log,
		drift:            agentio.NewDrift("Pi", log),
		resumeRef:        resumeRef,
		interruptTimeout: interruptTimeout,
		interval:         snapshotInterval,
		writer:           agentio.NewWriter("pi"),
		clock:            cfg.Clock,
		pending:          make(map[string]*pendingCmd),
		dialogs:          make(map[string]*dialog),
		caps:             native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true, Settings: true, Compact: true},
	}
	a.host = agentio.NewHost(agentio.HostConfig{
		Name:   "Pi",
		Logger: log,
		Clock:  cfg.Clock,
		Start:  cfg.StartProcess,
		Writer: a.writer,
	})
	a.loopDone = a.host.LoopDone()
	return a
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
	// Before the loop runs, which owns it from then on.
	a.loop = a.newLoopState(sink)
	// The event loop handles Pi's records and the functions posted to it
	// (timers); it is the only goroutine that calls the sink.
	_, err := a.host.Launch(procutil.Command{
		Program: a.cfg.Program,
		Args:    args,
		Dir:     a.cfg.CWD,
		Env:     procenv.ForAgent(a.cfg.Env),
		Logger:  a.log,
	}, agentio.Loop{Line: a.handleRecord, NextWake: a.nextWake, Wake: a.wake, Ended: a.handleExit})
	if err != nil {
		return err
	}

	res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_state"})
	if err == nil {
		err = res.err
	}
	if err != nil {
		return a.startError(err)
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
		return a.startError(errExited)
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}

// startError stops Pi, which didn't open its session, and says why. When
// it exited, its exit error and stderr say why (a broken extension, a bad
// option).
func (a *Adapter) startError(err error) error {
	return a.host.StartError(fmt.Errorf("pi didn't open its session: %w", err))
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
	a.runOnLoop(func() {
		if a.isCurrentTurn(turn) {
			a.loop.interrupt.Start()
		}
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

// Wait returns when Pi has exited and the loop handled its output (see
// agentio.Host).
func (a *Adapter) Wait() error {
	return a.host.Wait()
}

// Close stops Pi: it aborts a running turn (closing stdin mid-turn leaves
// the session with a tool call without result), closes stdin, then sends
// SIGTERM and SIGKILL to the process group if Pi doesn't exit.
func (a *Adapter) Close() error {
	a.host.Close(a.abortForClose)
	return nil
}

// abortForClose aborts a running turn, if any, and returns a channel
// closed once Pi answered the abort.
func (a *Adapter) abortForClose(ctx context.Context) <-chan struct{} {
	a.mu.Lock()
	busy := (a.turnActive || a.runActive) && !a.exited
	var abort *pendingCmd
	var abortID string
	if busy {
		a.interrupted = true
		abortID, abort = a.addPendingLocked(cmdAbort)
	}
	a.mu.Unlock()
	if abort == nil {
		return nil
	}
	// A write that is stuck (Pi stopped reading) must not keep Close from
	// going on: the wait is bounded, and Stop's closing stdin makes the
	// write fail.
	go func() {
		if err := a.send(ctx, simpleCommand{ID: abortID, Type: "abort"}); err != nil {
			a.mu.Lock()
			delete(a.pending, abortID)
			a.mu.Unlock()
			deliverResult(abort, cmdResult{err: err})
		}
	}()
	answered := make(chan struct{})
	go func() {
		// The answer, a failed write, or errExited once Pi's output ended.
		<-abort.done
		close(answered)
	}()
	return answered
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
