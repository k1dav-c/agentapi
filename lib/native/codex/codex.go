// Package codex drives Codex CLI through `codex app-server`, its JSON-RPC
// protocol over stdio, as a native.Adapter.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

// Config configures the adapters a Factory creates.
type Config struct {
	// Program is the codex executable; Args are the user's arguments after
	// it (see parseArgs).
	Program string
	Args    []string
	// CWD is the directory Codex works in, unless -C/--cd says otherwise.
	CWD string
	// Env is the environment of app-server; nil means os.Environ().
	// Variables a parent agent session sets are removed either way.
	Env    []string
	Logger *slog.Logger

	// Launch starts app-server; nil runs Program with procutil.Start.
	// Tests inject pipes.
	Launch procutil.StartFunc
	// StopTimeout is how long Close waits for app-server to exit after
	// closing its stdin before it sends SIGTERM, and again before SIGKILL.
	// Zero means 3s and 2s.
	StopTimeout time.Duration
	// SnapshotInterval is the least time between two snapshots of a
	// streaming message. Zero means 100 ms.
	SnapshotInterval time.Duration
	// InterruptTimeout is how long to wait for app-server to end an
	// interrupted turn before ending it here. Zero means 10 s.
	InterruptTimeout time.Duration
	// Clock times snapshots, interrupted turns and Close; nil means the
	// real clock.
	Clock quartz.Clock
}

const (
	// inboxSize is how many messages from app-server wait for the read
	// loop.
	inboxSize = 256
	// readBufferSize is how much of app-server's output is read at once;
	// longer lines take several reads.
	readBufferSize = 64 << 10
	// exitWait is how long a failed Start waits for app-server's exit
	// status, which says why it failed.
	exitWait = 3 * time.Second
)

// NewFactory checks the command line and returns a factory for adapters
// that run it. An empty resumeRef starts a new thread; otherwise the
// adapter resumes that thread.
func NewFactory(cfg Config) (native.Factory, error) {
	opts, err := parseArgs(cfg.Args, cfg.CWD)
	if err != nil {
		return nil, err
	}
	return func(resumeRef string) (native.Adapter, error) {
		return newAdapter(cfg, opts, resumeRef), nil
	}, nil
}

// optOutNotifications are notifications the adapter doesn't use; some are
// large (moderation metadata).
var optOutNotifications = []string{
	"turn/moderationMetadata",
	"remoteControl/status/changed",
	"turn/diff/updated",
	"thread/started",
	"app/list/updated",
	"fs/changed",
	"thread/settings/updated",
}

// Adapter is a native.Adapter for one `codex app-server` process and one
// thread.
type Adapter struct {
	cfg       Config
	opts      launchOptions
	resumeRef string
	log       *slog.Logger
	// prefix makes request IDs unique across processes: app-server numbers
	// its requests from 0 in every process.
	prefix string

	proc    procutil.Process
	sink    native.Sink
	writeMu sync.Mutex

	inbox chan rpcMessage
	tasks chan func()
	// loopDone is closed when the read loop has finished.
	loopDone chan struct{}
	// exited is closed when the process has exited and the loop finished.
	exited  chan struct{}
	exitErr error

	mu       sync.Mutex
	started  bool
	closed   bool
	nextID   int64
	calls    map[int64]*call
	threadID string
	// activeTurn is the turn app-server is working on, or "".
	activeTurn string
	// interrupting marks the active turn while Interrupt asks app-server
	// to stop it.
	interrupting map[string]bool
	requests     map[string]*pendingRequest
	// settings are the thread's settings, with the changes in pending,
	// which the next turn/start carries; models are app-server's models,
	// nil until listed. See settings.go.
	settings native.Settings
	pending  native.Settings
	models   []native.ModelOption
	// plan: the thread is in the plan collaboration mode, or will be from
	// the next turn; pendingCollab is the mode the next turn/start
	// switches to, if any; planWait is the plan request the current turn
	// waits on. See plan.go.
	plan          bool
	pendingCollab string
	planWait      string
	// collabSet: the thread has a collaboration mode, which overrides the
	// model and effort of its turns.
	collabSet bool

	closeOnce sync.Once
	// handled counts the messages the read loop has handled.
	handled atomic.Int64

	// loop is owned by the read loop.
	loop loopState
}

var (
	_ native.Adapter      = (*Adapter)(nil)
	_ native.Configurable = (*Adapter)(nil)
)

type call struct {
	// onResult runs in the read loop before the caller sees the response,
	// so the loop's state is updated before later notifications.
	onResult func(result json.RawMessage, err *rpcError)
	ch       chan callResult
}

type callResult struct {
	result json.RawMessage
	err    *rpcError
}

func (r callResult) unpack() (json.RawMessage, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.result, nil
}

func newAdapter(cfg Config, opts launchOptions, resumeRef string) *Adapter {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.SnapshotInterval == 0 {
		cfg.SnapshotInterval = 100 * time.Millisecond
	}
	if cfg.InterruptTimeout == 0 {
		cfg.InterruptTimeout = 10 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	a := &Adapter{
		cfg:       cfg,
		opts:      opts,
		resumeRef: resumeRef,
		log:       logger.With("agent", "codex"),
		prefix:    hex.EncodeToString(nonce[:]),
		inbox:     make(chan rpcMessage, inboxSize),
		// Unbuffered: a task handed over is one the loop runs.
		tasks:        make(chan func()),
		loopDone:     make(chan struct{}),
		exited:       make(chan struct{}),
		calls:        make(map[int64]*call),
		interrupting: make(map[string]bool),
		requests:     make(map[string]*pendingRequest),
	}
	a.loop.a = a
	return a
}

// Capabilities implements native.Adapter.
func (a *Adapter) Capabilities() native.Capabilities {
	return native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true, Settings: true}
}

// command returns the app-server command line.
func (a *Adapter) command() []string {
	return append([]string{"app-server"}, a.opts.appServerArgs...)
}

// Start implements native.Adapter: it starts app-server, initializes the
// connection, checks the login and starts (or resumes) the thread. Without
// a login it returns a native.LoginRequiredError.
func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("adapter is closed")
	}
	if a.proc != nil {
		a.mu.Unlock()
		return errors.New("adapter already started")
	}
	launch := a.cfg.Launch
	if launch == nil {
		launch = procutil.Start
	}
	proc, err := launch(procutil.Command{
		Program: a.cfg.Program,
		Args:    a.command(),
		Dir:     a.cwd(),
		Env:     native.AgentEnv(a.cfg.Env),
		Logger:  a.log,
	})
	if err != nil {
		// The adapter is done: Close and another Start must not close the
		// channels again.
		a.closed = true
		close(a.loopDone)
		close(a.exited)
		a.mu.Unlock()
		return err
	}
	a.proc = proc
	a.sink = sink
	a.mu.Unlock()

	go a.read(proc.Stdout())
	go a.run()
	go func() {
		err := proc.Wait()
		<-a.loopDone
		a.exitErr = procutil.WithStderr(err, proc.StderrTail())
		close(a.exited)
	}()

	if err := a.handshake(ctx); err != nil {
		if a.loopEnded() {
			// app-server exited: its stderr says why (a bad -c value, …).
			select {
			case <-a.exited:
				if a.exitErr != nil {
					return fmt.Errorf("%w (%v)", err, a.exitErr)
				}
				if tail := proc.StderrTail(); tail != "" {
					return fmt.Errorf("%w: %s", err, tail)
				}
			case <-time.After(exitWait):
			}
		}
		return err
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}

// cwd is the directory of the thread.
func (a *Adapter) cwd() string {
	if a.opts.cwd != "" {
		return a.opts.cwd
	}
	return a.cfg.CWD
}

func (a *Adapter) handshake(ctx context.Context) error {
	result, err := a.call(ctx, "initialize", initializeParams{
		ClientInfo: clientInfo{Name: "agentapi", Title: "AgentAPI", Version: version.Version},
		Capabilities: initializeCapabilities{
			ExperimentalAPI:           true,
			OptOutNotificationMethods: optOutNotifications,
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	var info initializeResponse
	if json.Unmarshal(result, &info) == nil && info.UserAgent != "" {
		a.log.Debug("Connected to codex app-server", "userAgent", info.UserAgent)
	}
	if err := a.notify("initialized", nil); err != nil {
		return err
	}
	if err := a.checkLogin(ctx); err != nil {
		return err
	}

	if a.resumeRef != "" {
		err := a.openThread(ctx, "thread/resume", threadResumeParams{
			ThreadID:       a.resumeRef,
			ExcludeTurns:   true,
			CWD:            a.cwd(),
			Model:          a.opts.model,
			ApprovalPolicy: a.opts.approvalPolicy,
			Sandbox:        a.opts.sandbox,
		}, true)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || a.loopEnded() {
			return fmt.Errorf("resume codex thread %s: %w", a.resumeRef, err)
		}
		msg := fmt.Sprintf("Could not resume Codex thread %s (%v); starting a new thread.", a.resumeRef, err)
		a.log.Warn("Could not resume the Codex thread; starting a new one", "thread", a.resumeRef, "error", err)
		_ = a.inLoop(ctx, func() { a.loop.warn(msg) })
	}
	err = a.openThread(ctx, "thread/start", threadStartParams{
		CWD:            a.cwd(),
		Model:          a.opts.model,
		ApprovalPolicy: a.opts.approvalPolicy,
		Sandbox:        a.opts.sandbox,
	}, false)
	if err != nil {
		return fmt.Errorf("start codex thread: %w", err)
	}
	return nil
}

var errNoThread = errors.New("codex app-server returned no thread")

// openThread starts or resumes the thread with method.
func (a *Adapter) openThread(ctx context.Context, method string, params any, resumed bool) error {
	if _, err := a.call(ctx, method, params, a.loop.threadOpened(resumed)); err != nil {
		return err
	}
	// threadOpened has run: it sets the thread before the response
	// returns.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.threadID == "" {
		return errNoThread
	}
	return nil
}

func (a *Adapter) loopEnded() bool {
	select {
	case <-a.loopDone:
		return true
	default:
		return false
	}
}

// session returns the thread and the turn app-server is working on.
func (a *Adapter) session() (threadID, turnID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started {
		return "", "", native.ErrNotStarted
	}
	return a.threadID, a.activeTurn, nil
}

// StartTurn implements native.Adapter.
func (a *Adapter) StartTurn(ctx context.Context, input native.Input) error {
	threadID, turnID, err := a.session()
	if err != nil {
		return err
	}
	if turnID != "" {
		return native.ErrTurnActive
	}
	params := turnStartParams{ThreadID: threadID, Input: inputItems(input)}
	if !a.opts.summaryFromUser {
		params.Summary = "detailed"
	}
	pending := a.takePending(&params)
	_, err = a.call(ctx, "turn/start", params, func(result json.RawMessage, rpcErr *rpcError) {
		if rpcErr != nil {
			return
		}
		var resp turnResponse
		if json.Unmarshal(result, &resp) == nil && resp.Turn.ID != "" {
			a.loop.beginTurn(resp.Turn.ID)
		}
	})
	if err != nil {
		a.restorePending(pending)
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && a.busy(rpcErr) {
			return native.ErrTurnActive
		}
		return fmt.Errorf("start turn: %w", err)
	}
	return nil
}

// busy reports whether turn/start failed because app-server is working on
// another turn: the error says so, or the turn started meanwhile (its
// turn/started comes before the error).
func (a *Adapter) busy(err *rpcError) bool {
	if isBusyError(err.Message) {
		return true
	}
	_, turnID, _ := a.session()
	return turnID != ""
}

// isBusyError reports whether a turn/start error says another turn is
// running, such as "thread already has an active or pending turn".
func isBusyError(message string) bool {
	m := strings.ToLower(message)
	if !strings.Contains(m, "turn") || strings.Contains(m, "no active turn") {
		return false
	}
	for _, busy := range []string{"already", "active", "running", "in progress", "pending"} {
		if strings.Contains(m, busy) {
			return true
		}
	}
	return false
}

// Steer implements native.Adapter with turn/steer. The message joins the
// running turn at its next model request; the current output continues.
func (a *Adapter) Steer(ctx context.Context, input native.Input) error {
	threadID, turnID, err := a.session()
	if err != nil {
		return err
	}
	if turnID == "" {
		return native.ErrNoActiveTurn
	}
	_, err = a.call(ctx, "turn/steer", turnSteerParams{
		ThreadID: threadID, ExpectedTurnID: turnID, Input: inputItems(input),
	}, nil)
	return turnCallError("steer turn", err)
}

// Interrupt implements native.Adapter with turn/interrupt. The turn ends
// with turn/completed (status interrupted) a few milliseconds later.
func (a *Adapter) Interrupt(ctx context.Context) error {
	threadID, turnID, err := a.session()
	if err != nil {
		return err
	}
	if turnID == "" {
		if waited, err := a.cancelPlanDecision(ctx); waited {
			return err
		}
	}
	if turnID == "" || !a.markInterrupting(turnID) {
		return native.ErrNoActiveTurn
	}
	_, err = a.call(ctx, "turn/interrupt", turnInterruptParams{ThreadID: threadID, TurnID: turnID},
		func(_ json.RawMessage, rpcErr *rpcError) {
			if rpcErr == nil {
				a.loop.interruptSent(turnID)
			}
		})
	if err != nil {
		// The turn may go on: if it completes, it was not interrupted.
		a.mu.Lock()
		delete(a.interrupting, turnID)
		a.mu.Unlock()
	}
	return turnCallError("interrupt turn", err)
}

// markInterrupting marks turnID as being interrupted, unless it ended. A
// turn that completes before app-server sees the interrupt still ends as
// interrupted (see completeTurn).
func (a *Adapter) markInterrupting(turnID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeTurn != turnID {
		return false
	}
	a.interrupting[turnID] = true
	return true
}

// turnCallError maps the error of a call about the active turn: app-server
// says there is no active turn when it ended meanwhile.
func turnCallError(what string, err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) && strings.Contains(strings.ToLower(rpcErr.Message), "no active turn") {
		return native.ErrNoActiveTurn
	}
	return fmt.Errorf("%s: %w", what, err)
}

// inputItems converts a user message to UserInput items: the text, then
// each image as a local file (native.InputFromText makes the paths
// absolute).
func inputItems(input native.Input) []userInput {
	items := []userInput{{Type: "text", Text: input.Text}}
	for _, img := range input.Images {
		items = append(items, userInput{Type: "localImage", Path: img.Path})
	}
	return items
}

// Wait implements native.Adapter.
func (a *Adapter) Wait() error {
	<-a.exited
	return a.exitErr
}

// Close implements native.Adapter: it closes app-server's stdin (it exits
// on EOF), then sends SIGTERM and SIGKILL if it doesn't exit (see
// Config.StopTimeout).
func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		proc := a.proc
		if proc == nil && !a.closed {
			// Never started, so nothing else closes them.
			close(a.loopDone)
			close(a.exited)
		}
		a.closed = true
		a.mu.Unlock()
		if proc == nil {
			return
		}
		procutil.Stop(proc, a.exited, procutil.StopOptions{
			StdinGrace: a.cfg.StopTimeout,
			TermGrace:  a.cfg.StopTimeout,
			Clock:      a.cfg.Clock,
			Logger:     a.log,
			Name:       "Codex app-server",
		})
	})
	return nil
}

// --- JSON-RPC plumbing ---

var errExited = errors.New("codex app-server exited")

// call sends a request and waits for its response, bounded by ctx. Once
// the loop has ended, waiting calls fail with errExited.
func (a *Adapter) call(ctx context.Context, method string, params any, onResult func(json.RawMessage, *rpcError)) (json.RawMessage, error) {
	a.mu.Lock()
	if a.loopEnded() {
		a.mu.Unlock()
		return nil, errExited
	}
	a.nextID++
	id := a.nextID
	c := &call{onResult: onResult, ch: make(chan callResult, 1)}
	a.calls[id] = c
	a.mu.Unlock()

	if err := a.write(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		a.dropCall(id)
		return nil, err
	}
	select {
	case res := <-c.ch:
		return res.unpack()
	case <-ctx.Done():
		a.dropCall(id)
		return nil, ctx.Err()
	case <-a.loopDone:
		// The loop may have delivered the response just before it ended.
		select {
		case res := <-c.ch:
			return res.unpack()
		default:
			return nil, errExited
		}
	}
}

func (a *Adapter) dropCall(id int64) {
	a.mu.Lock()
	delete(a.calls, id)
	a.mu.Unlock()
}

func (a *Adapter) notify(method string, params any) error {
	return a.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

// write sends one message on app-server's stdin.
func (a *Adapter) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if _, err := a.proc.Stdin().Write(data); err != nil {
		return fmt.Errorf("write to codex app-server: %w", err)
	}
	return nil
}

// writeAsync sends a message without blocking the read loop.
func (a *Adapter) writeAsync(v any) {
	go func() {
		if err := a.write(v); err != nil {
			a.log.Debug("Failed to write to codex app-server", "error", err)
		}
	}()
}

// inLoop runs f in the read loop, so it may call the sink. Unless it
// returns an error, the loop runs f.
func (a *Adapter) inLoop(ctx context.Context, f func()) error {
	select {
	case a.tasks <- f:
		return nil
	case <-a.loopDone:
		return errExited
	case <-ctx.Done():
		return ctx.Err()
	}
}

// read decodes app-server's stdout into the inbox until EOF.
func (a *Adapter) read(r io.Reader) {
	defer close(a.inbox)
	br := bufio.NewReaderSize(r, readBufferSize)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr != nil {
				a.log.Warn("Ignoring a line from codex app-server that is not JSON", "error", jerr, "line", native.OneLine(string(line), 200))
			} else {
				a.inbox <- msg
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				a.log.Debug("Reading codex app-server output failed", "error", err)
			}
			return
		}
	}
}

// run is the read loop: it handles app-server's messages in order, and
// is the only goroutine that calls the sink.
func (a *Adapter) run() {
	defer close(a.loopDone)
	timer := a.cfg.Clock.NewTimer(time.Hour, "codex", "wake")
	timer.Stop()
	var wakeAt time.Time
	for {
		select {
		case msg, ok := <-a.inbox:
			if !ok {
				a.loop.exited()
				return
			}
			a.dispatch(msg)
			a.handled.Add(1)
		case f := <-a.tasks:
			f()
		case <-timer.C:
			wakeAt = time.Time{}
			a.loop.wake()
		}
		if next := a.loop.nextWake(); !next.Equal(wakeAt) {
			timer.Stop()
			wakeAt = next
			if !next.IsZero() {
				timer.Reset(max(a.cfg.Clock.Until(next), time.Millisecond), "codex", "wake")
			}
		}
	}
}

// dispatch routes one message from app-server.
func (a *Adapter) dispatch(msg rpcMessage) {
	switch {
	case msg.Method == "" && len(msg.ID) > 0:
		a.handleResponse(msg)
	case msg.Method != "" && len(msg.ID) > 0:
		a.loop.serverRequest(msg)
	case msg.Method != "":
		a.loop.notification(msg)
	}
}

func (a *Adapter) handleResponse(msg rpcMessage) {
	var id int64
	if json.Unmarshal(msg.ID, &id) != nil {
		return
	}
	a.mu.Lock()
	c := a.calls[id]
	delete(a.calls, id)
	a.mu.Unlock()
	if c == nil {
		return
	}
	if c.onResult != nil {
		c.onResult(msg.Result, msg.Error)
	}
	c.ch <- callResult{result: msg.Result, err: msg.Error}
}
