// Package codex drives Codex CLI through `codex app-server`, its JSON-RPC
// protocol over stdio, as a native.Adapter.
package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/native"
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
	// Variables a parent Codex session sets are removed either way.
	Env    []string
	Logger *slog.Logger

	// Launch starts app-server; nil runs Program. Tests inject pipes.
	Launch Launcher
	// SnapshotInterval is the least time between two snapshots of a
	// streaming message. Zero means 100 ms.
	SnapshotInterval time.Duration
	// InterruptTimeout is how long to wait for app-server to end an
	// interrupted turn before ending it here. Zero means 10 s.
	InterruptTimeout time.Duration
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

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
// large (moderation metadata, rate limits).
var optOutNotifications = []string{
	"turn/moderationMetadata",
	"account/rateLimits/updated",
	"remoteControl/status/changed",
	"turn/diff/updated",
	"thread/started",
	"app/list/updated",
	"fs/changed",
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

	proc    Process
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
	activeTurn   string
	interrupting map[string]bool
	requests     map[string]*pendingRequest

	closeOnce sync.Once
	// handled counts the messages the read loop has handled.
	handled atomic.Int64

	// st is owned by the read loop.
	st loopState
}

var _ native.Adapter = (*Adapter)(nil)

type call struct {
	method string
	// onResult runs in the read loop before the caller sees the response,
	// so the loop's state is updated before later notifications.
	onResult func(result json.RawMessage, err *rpcError)
	ch       chan callResult
}

type callResult struct {
	result json.RawMessage
	err    *rpcError
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
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	a := &Adapter{
		cfg:          cfg,
		opts:         opts,
		resumeRef:    resumeRef,
		log:          logger.With("agent", "codex"),
		prefix:       hex.EncodeToString(nonce[:]),
		inbox:        make(chan rpcMessage, 256),
		tasks:        make(chan func(), 16),
		loopDone:     make(chan struct{}),
		exited:       make(chan struct{}),
		calls:        make(map[int64]*call),
		interrupting: make(map[string]bool),
		requests:     make(map[string]*pendingRequest),
	}
	a.st.a = a
	return a
}

// Capabilities implements native.Adapter.
func (a *Adapter) Capabilities() native.Capabilities {
	return native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true}
}

// command returns the app-server command line.
func (a *Adapter) command() []string {
	return append([]string{"app-server"}, a.opts.appServerArgs...)
}

// Start implements native.Adapter: it starts app-server, initializes the
// connection and starts (or resumes) the thread.
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
	env := a.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	launch := a.cfg.Launch
	if launch == nil {
		launch = launchProcess
	}
	proc, err := launch(LaunchSpec{
		Program: a.cfg.Program,
		Args:    a.command(),
		Dir:     a.cwd(),
		Env:     cleanEnv(env),
		Logger:  a.log,
	})
	if err != nil {
		a.mu.Unlock()
		close(a.loopDone)
		close(a.exited)
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
		a.exitErr = err
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
				if p, ok := a.proc.(interface{ StderrTail() string }); ok && p.StderrTail() != "" {
					return fmt.Errorf("%w: %s", err, p.StderrTail())
				}
			case <-time.After(3 * time.Second):
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
			OptOutNotificationMethods: optOutNotifications,
		},
	}, nil)
	if err != nil && !strings.Contains(err.Error(), "Already initialized") {
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	var info initializeResponse
	if json.Unmarshal(result, &info) == nil && info.UserAgent != "" {
		a.log.Debug("Connected to codex app-server", "userAgent", info.UserAgent)
	}
	if err := a.notify("initialized", nil); err != nil {
		return err
	}

	if a.resumeRef != "" {
		_, err := a.call(ctx, "thread/resume", threadResumeParams{
			ThreadID:       a.resumeRef,
			ExcludeTurns:   true,
			CWD:            a.cwd(),
			Model:          a.opts.model,
			ApprovalPolicy: a.opts.approvalPolicy,
			Sandbox:        a.opts.sandbox,
		}, a.st.threadOpened(true))
		if err == nil {
			return nil
		}
		if ctx.Err() != nil || a.loopEnded() {
			return fmt.Errorf("resume codex thread %s: %w", a.resumeRef, err)
		}
		msg := fmt.Sprintf("Could not resume Codex thread %s (%v); starting a new thread.", a.resumeRef, err)
		a.log.Warn("Could not resume the Codex thread; starting a new one", "thread", a.resumeRef, "error", err)
		_ = a.inLoop(ctx, func() { a.st.warn(msg) })
	}
	_, err = a.call(ctx, "thread/start", threadStartParams{
		CWD:            a.cwd(),
		Model:          a.opts.model,
		ApprovalPolicy: a.opts.approvalPolicy,
		Sandbox:        a.opts.sandbox,
	}, a.st.threadOpened(false))
	if err != nil {
		return fmt.Errorf("start codex thread: %w", err)
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

// StartTurn implements native.Adapter.
func (a *Adapter) StartTurn(ctx context.Context, input native.Input) error {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return native.ErrNotStarted
	}
	if a.activeTurn != "" {
		a.mu.Unlock()
		return native.ErrTurnActive
	}
	threadID := a.threadID
	a.mu.Unlock()

	params := turnStartParams{ThreadID: threadID, Input: a.inputItems(input)}
	if !a.opts.summaryFromUser {
		params.Summary = "detailed"
	}
	_, err := a.call(ctx, "turn/start", params, func(result json.RawMessage, rpcErr *rpcError) {
		if rpcErr != nil {
			return
		}
		var resp turnResponse
		if json.Unmarshal(result, &resp) == nil && resp.Turn.ID != "" {
			a.st.beginTurn(resp.Turn.ID)
		}
	})
	if err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && isBusyError(rpcErr.Message) {
			return native.ErrTurnActive
		}
		return fmt.Errorf("start turn: %w", err)
	}
	return nil
}

// isBusyError reports whether a turn/start error says another turn is
// running.
func isBusyError(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "already") || strings.Contains(m, "busy") ||
		(strings.Contains(m, "active turn") && !strings.Contains(m, "no active turn"))
}

// Steer implements native.Adapter with turn/steer. The message joins the
// running turn at its next model request; the current output continues.
func (a *Adapter) Steer(ctx context.Context, input native.Input) error {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return native.ErrNotStarted
	}
	turnID, threadID := a.activeTurn, a.threadID
	a.mu.Unlock()
	if turnID == "" {
		return native.ErrNoActiveTurn
	}
	_, err := a.call(ctx, "turn/steer", turnSteerParams{
		ThreadID: threadID, ExpectedTurnID: turnID, Input: a.inputItems(input),
	}, nil)
	if err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && strings.Contains(strings.ToLower(rpcErr.Message), "no active turn") {
			return native.ErrNoActiveTurn
		}
		return fmt.Errorf("steer turn: %w", err)
	}
	return nil
}

// Interrupt implements native.Adapter with turn/interrupt. The turn ends
// with turn/completed (status interrupted) a few milliseconds later.
func (a *Adapter) Interrupt(ctx context.Context) error {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return native.ErrNotStarted
	}
	turnID, threadID := a.activeTurn, a.threadID
	if turnID != "" {
		a.interrupting[turnID] = true
	}
	a.mu.Unlock()
	if turnID == "" {
		return native.ErrNoActiveTurn
	}
	_, err := a.call(ctx, "turn/interrupt", turnInterruptParams{ThreadID: threadID, TurnID: turnID},
		func(_ json.RawMessage, rpcErr *rpcError) {
			if rpcErr == nil {
				a.st.interruptSent(turnID)
			}
		})
	if err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && strings.Contains(strings.ToLower(rpcErr.Message), "no active turn") {
			return native.ErrNoActiveTurn
		}
		return fmt.Errorf("interrupt turn: %w", err)
	}
	return nil
}

// inputItems converts a user message to UserInput items: the text, then
// each image as a local file.
func (a *Adapter) inputItems(input native.Input) []userInput {
	items := []userInput{{Type: "text", Text: input.Text}}
	for _, img := range input.Images {
		items = append(items, userInput{Type: "localImage", Path: absPath(img.Path, a.cwd())})
	}
	return items
}

// Wait implements native.Adapter.
func (a *Adapter) Wait() error {
	<-a.exited
	return a.exitErr
}

// Close implements native.Adapter: it closes app-server's stdin (it exits
// on EOF), then sends SIGTERM after 3 s and SIGKILL after 5 s.
func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		proc := a.proc
		a.mu.Unlock()
		if proc == nil {
			select {
			case <-a.exited:
			default:
				close(a.loopDone)
				close(a.exited)
			}
			return
		}
		a.writeMu.Lock()
		_ = proc.Stdin().Close()
		a.writeMu.Unlock()
		select {
		case <-a.exited:
			return
		case <-time.After(closeGrace):
		}
		a.log.Debug("Codex app-server did not exit after stdin closed; sending SIGTERM")
		_ = proc.Terminate()
		select {
		case <-a.exited:
			return
		case <-time.After(termGrace):
		}
		a.log.Warn("Codex app-server did not exit after SIGTERM; killing it")
		_ = proc.Kill()
	})
	return nil
}

// --- JSON-RPC plumbing ---

var errExited = errors.New("codex app-server exited")

// How long Close waits for app-server to exit after closing its stdin, and
// after SIGTERM. Variables for tests.
var (
	closeGrace = 3 * time.Second
	termGrace  = 2 * time.Second
)

// call sends a request and waits for its response, bounded by ctx.
func (a *Adapter) call(ctx context.Context, method string, params any, onResult func(json.RawMessage, *rpcError)) (json.RawMessage, error) {
	a.mu.Lock()
	if a.loopEnded() {
		a.mu.Unlock()
		return nil, errExited
	}
	a.nextID++
	id := a.nextID
	c := &call{method: method, onResult: onResult, ch: make(chan callResult, 1)}
	a.calls[id] = c
	a.mu.Unlock()

	if err := a.write(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		a.dropCall(id)
		return nil, err
	}
	select {
	case res := <-c.ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	case <-ctx.Done():
		a.dropCall(id)
		return nil, ctx.Err()
	case <-a.loopDone:
		// The loop may have delivered the response just before it ended.
		select {
		case res := <-c.ch:
			if res.err != nil {
				return nil, res.err
			}
			return res.result, nil
		default:
		}
		return nil, errExited
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

// inLoop runs f in the read loop, so it may call the sink.
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
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr != nil {
				a.log.Warn("Ignoring a line from codex app-server that is not JSON", "error", jerr, "line", truncate(string(line), 200))
			} else {
				a.inbox <- msg
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				a.log.Debug("Reading codex app-server output failed", "error", err)
			}
			return
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// run is the read loop: it handles app-server's messages in order, and
// is the only goroutine that calls the sink.
func (a *Adapter) run() {
	defer close(a.loopDone)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	var wakeAt time.Time
	for {
		select {
		case msg, ok := <-a.inbox:
			if !ok {
				a.st.exited()
				a.failCalls()
				return
			}
			a.dispatch(msg)
			a.handled.Add(1)
		case f := <-a.tasks:
			f()
		case <-timer.C:
			wakeAt = time.Time{}
			a.st.wake()
		}
		if next := a.st.nextWake(); !next.Equal(wakeAt) {
			timer.Stop()
			wakeAt = next
			if !next.IsZero() {
				timer.Reset(max(time.Until(next), time.Millisecond))
			}
		}
	}
}

// failCalls ends the calls still waiting for a response.
func (a *Adapter) failCalls() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.calls {
		delete(a.calls, id)
	}
}

// dispatch routes one message from app-server.
func (a *Adapter) dispatch(msg rpcMessage) {
	switch {
	case msg.Method == "" && len(msg.ID) > 0:
		a.handleResponse(msg)
	case msg.Method != "" && len(msg.ID) > 0:
		a.st.serverRequest(msg)
	case msg.Method != "":
		a.st.notification(msg)
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
