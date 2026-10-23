// Package claude drives Claude Code through its stream-json protocol: the
// CLI runs headless (-p) with stream-json on stdin and stdout, and
// permission prompts arrive as control requests on the same stream.
package claude

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/native/procutil"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/transcript"
)

// Config configures the adapter.
type Config struct {
	// Program is the Claude Code executable.
	Program string
	// Args are the user's arguments after the program.
	Args []string
	// CWD is the working directory; empty means the current one.
	CWD    string
	Logger *slog.Logger
	// Env is the environment to start from; nil means os.Environ().
	Env []string
	// StartProcess starts the process; nil runs it with procutil.Start.
	StartProcess procutil.StartFunc
	// AuthStatus runs `claude auth status --json` before the session
	// starts and returns its stdout; nil runs it with procutil.Output.
	AuthStatus func(ctx context.Context, cmd procutil.Command) ([]byte, error)
	Clock      quartz.Clock
	// SnapshotInterval is the least time between two snapshots of a
	// streaming message; a change held back is sent when it is over. Zero
	// means 100ms.
	SnapshotInterval time.Duration
	// InterruptTimeout is how long to wait for Claude Code to end an
	// interrupted turn before ending it here. Zero means 10s.
	InterruptTimeout time.Duration
	// StopTimeout is how long Close waits for the process to exit after
	// closing its stdin before it sends SIGTERM, and again before SIGKILL,
	// and before that for a turn it interrupted to end. Zero means 3s, 2s,
	// and 3s for the turn.
	StopTimeout time.Duration
	// NewID generates session and message ids; nil uses random UUIDs.
	NewID func() string
}

// NewFactory returns a factory for Claude Code adapters. It fails for
// arguments that conflict with the native transport.
func NewFactory(cfg Config) (native.Factory, error) {
	return newFactory(cfg, os.Getuid())
}

// newFactory is NewFactory for a process that runs as the user uid.
func newFactory(cfg Config, uid int) (native.Factory, error) {
	args, err := parseArgs(cfg.Args)
	if err != nil {
		return nil, err
	}
	if args.avoidRootBypass(buildEnv(cfg.Env), uid) {
		logger := cfg.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("Claude Code refuses to bypass permissions as root outside a sandbox, " +
			"so it starts in the default permission mode and asks before it runs tools. " +
			"Set IS_SANDBOX=1 if AgentAPI runs in a sandbox, or choose a mode with --permission-mode.")
	}
	return func(resume native.Resume) (native.Adapter, error) {
		if resume.Fork && resume.Ref != "" {
			return newForkAdapter(cfg, args, resume.Ref), nil
		}
		return newAdapter(cfg, args, resume.Ref), nil
	}, nil
}

// newAdapter creates an adapter that starts a new session, or resumes
// resumeRef.
func newAdapter(cfg Config, args parsedArgs, resumeRef string) *Adapter {
	if cfg.Program == "" {
		cfg.Program = "claude"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.StartProcess == nil {
		cfg.StartProcess = procutil.Start
	}
	if cfg.AuthStatus == nil {
		cfg.AuthStatus = runAuthStatus
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if cfg.SnapshotInterval == 0 {
		cfg.SnapshotInterval = 100 * time.Millisecond
	}
	if cfg.InterruptTimeout == 0 {
		cfg.InterruptTimeout = 10 * time.Second
	}
	if cfg.NewID == nil {
		cfg.NewID = newUUID
	}
	a := &Adapter{
		cfg:         cfg,
		log:         cfg.Logger.With("agent", "claude"),
		args:        args,
		resumeRef:   resumeRef,
		parser:      newStreamParser(cfg.Clock, cfg.SnapshotInterval),
		reportedRef: resumeRef,
		writer:      agentio.NewWriter("claude"),
		requests:    make(map[string]*pendingRequest),
		exited:      make(chan struct{}),
		readDone:    make(chan struct{}),
		closeGrace:  closeInterruptGrace,
	}
	if cfg.StopTimeout > 0 {
		a.closeGrace = cfg.StopTimeout
	}
	if resumeRef == "" {
		a.sessionID = cfg.NewID()
	}
	return a
}

// newForkAdapter creates an adapter that opens a copy of the session
// resumeRef under a new session ID.
func newForkAdapter(cfg Config, args parsedArgs, resumeRef string) *Adapter {
	a := newAdapter(cfg, args, resumeRef)
	a.fork = true
	a.sessionID = a.cfg.NewID()
	return a
}

// Adapter is a native.Adapter for Claude Code.
type Adapter struct {
	cfg       Config
	args      parsedArgs
	resumeRef string
	// fork opens a copy of resumeRef's session under sessionID.
	fork bool
	// sessionID is the id passed with --session-id for a new session.
	sessionID string

	proc   procutil.Process
	sink   native.Sink
	parser *streamParser
	log    *slog.Logger
	// closeGrace is how long Close waits for a turn it interrupted to end.
	closeGrace time.Duration
	// reportedRef is the session id last reported with SessionRef, at
	// first the session to resume. Read loop only.
	reportedRef string
	// interruptAt is when the CLI accepted the interrupt of the current
	// turn, or zero. Read loop only.
	interruptAt time.Time

	// writer writes frames to the CLI's stdin, once it started.
	writer *agentio.Writer
	// calls are the control requests waiting for the CLI's answer.
	calls agentio.Calls[string, controlResult]

	mu      sync.Mutex
	started bool
	// turn is the current turn, if any.
	turn turnState
	// sessionStates: the CLI reports session_state_changed, so "idle" ends
	// a turn (otherwise every result does).
	sessionStates bool
	// prompt is the text of the turn sent last, until the CLI starts a
	// turn: a new session is titled after it.
	prompt     string
	controlSeq int
	requests   map[string]*pendingRequest
	closing    bool
	// mode is Claude Code's permission mode, model the value of the model
	// option it runs and effort its reasoning effort; models are the
	// models it offers. See settings.go.
	mode   string
	model  string
	effort string
	models []claudeModel
	// usage is the usage the CLI last reported (see usage.go). Read loop
	// only.
	usage native.AgentUsage

	exited   chan struct{}
	exitErr  error
	readDone chan struct{}
	stopOnce sync.Once
}

var (
	_ native.Adapter      = (*Adapter)(nil)
	_ native.Configurable = (*Adapter)(nil)
)

var (
	// errClosed is returned by Start after Close.
	errClosed = errors.New("the adapter was closed")
	// errExited fails the control requests the CLI didn't answer before
	// its output ended.
	errExited = errors.New("claude exited")
)

// turnPhase is where the current turn is.
type turnPhase int

const (
	// turnIdle: there is no turn.
	turnIdle turnPhase = iota
	// turnSent: a turn was sent, which the CLI hasn't started on yet.
	turnSent
	// turnRunning: the CLI works on a turn, reported with TurnStarted.
	// It can start one by itself (after a background task finished).
	turnRunning
)

// turnState is what the adapter knows of the current turn. endTurn
// clears it.
type turnState struct {
	phase turnPhase
	// ended is closed when the turn ends.
	ended chan struct{}
	// interrupted: the user interrupted it.
	interrupted bool
	// authFailed: the CLI reported a login failure in it.
	authFailed bool
	// result is its latest result.
	result *native.TurnResult
}

type controlResult struct {
	response json.RawMessage
	err      error
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Capabilities implements native.Adapter.
func (a *Adapter) Capabilities() native.Capabilities {
	return native.Capabilities{Steer: true, Interrupt: true, Resume: !a.args.noPersistence, Images: true, Settings: true}
}

// Start implements native.Adapter: it checks the login, starts the CLI and
// completes the initialize handshake. Without a login it returns a
// native.LoginRequiredError.
func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	cmd := procutil.Command{
		Program: a.cfg.Program,
		Args:    buildArgs(a.args, a.sessionID, a.resumeRef, a.fork),
		Env:     buildEnv(a.cfg.Env),
		Dir:     a.cfg.CWD,
		Logger:  a.log,
	}
	if err := a.checkLogin(ctx, cmd.Env); err != nil {
		return err
	}
	// A restart may have closed the adapter while the login was checked
	// (up to 10 s): starting the CLI now would run it next to the new one.
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		return errClosed
	}
	// The arguments can hold secrets (inline --settings or --mcp-config).
	a.log.Info("Starting Claude Code", "program", cmd.Program, "resume", a.resumeRef != "")
	a.log.Debug("Claude Code command line", "args", cmd.Args)
	proc, err := a.cfg.StartProcess(cmd)
	if err != nil {
		return err
	}
	a.mu.Lock()
	closing = a.closing
	if !closing {
		a.proc = proc
		a.sink = sink
	}
	a.mu.Unlock()
	if closing {
		// Close ran while the CLI was starting and found no process to stop.
		_ = proc.Stdin().Close()
		_ = proc.Kill()
		return errClosed
	}
	a.writer.Attach(proc.Stdin())

	go a.readLoop(proc.Stdout())
	go func() {
		err := proc.Wait()
		a.mu.Lock()
		a.exitErr = procutil.WithStderr(err, proc.StderrTail())
		a.mu.Unlock()
		close(a.exited)
	}()

	response, err := a.request(ctx, "initialize", map[string]any{"hooks": nil}, nil)
	if err != nil {
		_ = a.Close()
		if !errors.Is(err, errExited) {
			// Timed out, or the CLI answered with an error.
			return fmt.Errorf("initialize claude: %w", err)
		}
		exitErr := a.waitErr()
		if exitErr == nil {
			// It can exit cleanly after a fatal error (an unknown session
			// to resume); stderr says why.
			exitErr = procutil.WithStderr(err, proc.StderrTail())
		}
		return fmt.Errorf("claude exited during startup: %w", exitErr)
	}
	var info initializeInfo
	_ = json.Unmarshal(response, &info)
	a.readSettings(ctx, info)
	a.log.Info("Claude Code is ready", "version", info.Version, "permission_mode", info.PermissionMode)

	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}

// write sends one frame to the CLI.
func (a *Adapter) write(v any) error {
	return a.writer.Write(v)
}

// sendControl writes a control request. onResponse, if set, gets a
// successful answer in the read loop; the answer comes on the returned
// call.
func (a *Adapter) sendControl(subtype string, fields map[string]any, onResponse func(json.RawMessage)) (string, *agentio.Call[controlResult], error) {
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		return "", nil, errors.New("claude is shutting down")
	}
	return a.issueControl(subtype, fields, onResponse)
}

// issueControl is sendControl also while the adapter closes.
func (a *Adapter) issueControl(subtype string, fields map[string]any, onResponse func(json.RawMessage)) (string, *agentio.Call[controlResult], error) {
	a.mu.Lock()
	a.controlSeq++
	id := fmt.Sprintf("agentapi_%d", a.controlSeq)
	a.mu.Unlock()
	var then func(controlResult, error)
	if onResponse != nil {
		then = func(r controlResult, _ error) {
			if r.err == nil {
				onResponse(r.response)
			}
		}
	}
	call, err := a.calls.Add(id, then)
	if err != nil {
		return "", nil, err
	}
	request := map[string]any{"subtype": subtype}
	for k, v := range fields {
		request[k] = v
	}
	if err := a.write(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		a.calls.Drop(id)
		return "", nil, err
	}
	return id, call, nil
}

// request sends a control request and waits for its answer. onResponse, if
// set, gets a successful answer in the read loop first. A request that was
// sent and got no answer fails with native.ErrUnconfirmed: the CLI may
// carry it out all the same.
func (a *Adapter) request(ctx context.Context, subtype string, fields map[string]any, onResponse func(json.RawMessage)) (json.RawMessage, error) {
	id, call, err := a.sendControl(subtype, fields, onResponse)
	if err != nil {
		return nil, err
	}
	res, err := call.Wait(ctx)
	if err != nil {
		a.calls.Drop(id)
		return nil, native.Unconfirmed(err)
	}
	return res.response, res.err
}

// userMessage builds a user frame. Images go before the text, as Claude
// Code expands slash commands only from the last text block.
func (a *Adapter) userMessage(input native.Input) (map[string]any, error) {
	var content any = input.Text
	if len(input.Images) > 0 {
		blocks := make([]map[string]any, 0, len(input.Images)+1)
		for _, image := range input.Images {
			data, err := image.Base64()
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "base64", "media_type": image.MediaType, "data": data,
				},
			})
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": input.Text})
		content = blocks
	}
	return map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil,
		"session_id":         "",
		"uuid":               a.cfg.NewID(),
	}, nil
}

// StartTurn implements native.Adapter. The CLI takes the message right
// away; the turn starts when it reports running.
func (a *Adapter) StartTurn(ctx context.Context, input native.Input) error {
	msg, err := a.userMessage(input)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return native.ErrNotStarted
	}
	if a.turn.phase != turnIdle {
		a.mu.Unlock()
		return native.ErrTurnActive
	}
	a.beginTurnLocked(turnSent)
	a.turn.interrupted = false
	a.prompt = input.Text
	a.mu.Unlock()
	if err := a.write(msg); err != nil {
		a.mu.Lock()
		a.endTurnLocked()
		a.prompt = ""
		a.mu.Unlock()
		return err
	}
	return nil
}

// requestTitle asks the CLI to title a new session after its first prompt,
// as the terminal UI does. The answer is reported from the read loop.
func (a *Adapter) requestTitle(prompt string) {
	if runes := []rune(prompt); len(runes) > 2000 {
		prompt = string(runes[:2000])
	}
	onResponse := func(response json.RawMessage) {
		var r struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(response, &r) == nil && r.Title != "" {
			a.sink.SessionName(r.Title)
		}
	}
	if _, _, err := a.sendControl("generate_session_title", map[string]any{"description": prompt, "persist": true}, onResponse); err != nil {
		a.log.Debug("Failed to ask Claude Code for a session title", "error", err)
	}
}

// Steer implements native.Adapter. Without a priority the CLI adds the
// message to the running turn at its next model call.
func (a *Adapter) Steer(ctx context.Context, input native.Input) error {
	msg, err := a.userMessage(input)
	if err != nil {
		return err
	}
	a.mu.Lock()
	busy := a.turn.phase != turnIdle
	a.mu.Unlock()
	if !busy {
		return native.ErrNoActiveTurn
	}
	return a.write(msg)
}

// Interrupt implements native.Adapter. The turn ends with the result the
// CLI sends after it stopped, or here if that doesn't come within
// Config.InterruptTimeout.
func (a *Adapter) Interrupt(ctx context.Context) error {
	a.mu.Lock()
	busy := a.turn.phase != turnIdle
	if busy {
		a.turn.interrupted = true
	}
	a.mu.Unlock()
	if !busy {
		return native.ErrNoActiveTurn
	}
	_, err := a.request(ctx, "interrupt", nil, a.interruptAccepted)
	if err != nil {
		// Not interrupted: the turn ends as it ends, its error included.
		a.mu.Lock()
		a.turn.interrupted = false
		a.mu.Unlock()
	}
	return err
}

// interruptAccepted starts the wait for the end of the interrupted turn
// (see wake). It runs in the read loop.
func (a *Adapter) interruptAccepted(json.RawMessage) {
	a.mu.Lock()
	// The turn may have ended before the answer came.
	interrupted := a.turn.interrupted
	a.mu.Unlock()
	if interrupted && a.interruptAt.IsZero() {
		a.interruptAt = a.cfg.Clock.Now()
	}
}

// Respond implements native.Adapter.
func (a *Adapter) Respond(ctx context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	p, ok := a.requests[requestID]
	if !ok || p.answering {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	answer, err := p.response(response)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	p.answering = true
	a.mu.Unlock()
	err = a.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": answer,
		},
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	// If the CLI didn't get the answer, the request is still open.
	p.answering = false
	if err == nil && a.requests[requestID] == p {
		delete(a.requests, requestID)
	}
	return err
}

// Wait implements native.Adapter.
func (a *Adapter) Wait() error {
	a.mu.Lock()
	proc := a.proc
	a.mu.Unlock()
	if proc == nil {
		return native.ErrNotStarted
	}
	<-a.exited
	<-a.readDone
	return a.waitErr()
}

func (a *Adapter) waitErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitErr
}

// Close implements native.Adapter: a turn that runs is interrupted first
// (closing stdin mid-turn leaves the session with a tool call that has no
// result, which a resumed session goes on from); closing stdin lets the CLI
// finish and exit on its own; SIGTERM and SIGKILL follow if it doesn't.
func (a *Adapter) Close() error {
	a.mu.Lock()
	proc := a.proc
	ended := a.turn.ended
	a.closing = true
	a.mu.Unlock()
	if proc == nil {
		return nil
	}
	a.stopOnce.Do(func() {
		if ended != nil {
			a.interruptForClose(ended)
		}
		procutil.Stop(proc, a.exited, procutil.StopOptions{
			StdinGrace: a.cfg.StopTimeout,
			TermGrace:  a.cfg.StopTimeout,
			Clock:      a.cfg.Clock,
			Logger:     a.log,
			Name:       "Claude Code",
		})
	})
	<-a.exited
	return nil
}

// closeInterruptGrace is how long Close waits for a turn it interrupted to
// end, unless Config.StopTimeout says otherwise (Adapter.closeGrace).
const closeInterruptGrace = 3 * time.Second

// interruptForClose interrupts the running turn, whose end closes ended,
// and waits for it to end, for the CLI to exit, or for the grace to pass.
func (a *Adapter) interruptForClose(ended <-chan struct{}) {
	a.mu.Lock()
	a.turn.interrupted = true
	a.mu.Unlock()
	// Not waited for: a write the CLI doesn't read must not hold Close,
	// whose closing stdin makes it fail. Sent although Close set closing,
	// which refuses other requests.
	go func() {
		if _, _, err := a.issueControl("interrupt", nil, a.interruptAccepted); err != nil {
			a.log.Debug("Failed to interrupt the turn before closing", "error", err)
		}
	}()
	// Real time, as the Clock could be one nobody advances once the
	// adapter is retired.
	timer := time.NewTimer(a.closeGrace)
	defer timer.Stop()
	select {
	case <-ended:
	case <-a.exited:
	case <-timer.C:
		a.log.Warn("Claude Code didn't end its turn in time; closing it anyway")
	}
}

// readLoop handles the CLI's output until it closes, and wakes up when a
// snapshot the throttle held back or the end of an interrupted turn is due.
// It is the only caller of the sink after Start.
func (a *Adapter) readLoop(stdout io.Reader) {
	defer close(a.readDone)
	lines := make(chan []byte)
	go a.readLines(stdout, lines)
	timer := a.cfg.Clock.NewTimer(time.Hour, "claude", "wake")
	timer.Stop()
	defer timer.Stop()
	var wakeAt time.Time
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				a.outputClosed()
				return
			}
			a.handleLine(line)
		case <-timer.C:
			wakeAt = time.Time{}
			a.wake()
		}
		if next := a.nextWake(); !next.Equal(wakeAt) {
			timer.Stop()
			wakeAt = next
			if !next.IsZero() {
				timer.Reset(max(next.Sub(a.cfg.Clock.Now()), 0), "claude", "wake")
			}
		}
	}
}

// readLines hands the CLI's output to the read loop line by line until it
// closes.
func (a *Adapter) readLines(stdout io.Reader, lines chan<- []byte) {
	defer close(lines)
	agentio.ReadLines(stdout, "claude", 0, a.log, func(line []byte) { lines <- line })
}

// outputClosed sends the snapshots the throttle held back, fails the
// control requests still waiting for an answer and forgets the permission
// requests: the CLI is gone.
func (a *Adapter) outputClosed() {
	a.sendRich(a.parser.flushAll())
	a.mu.Lock()
	a.requests = make(map[string]*pendingRequest)
	a.mu.Unlock()
	a.calls.Close(errExited)
}

// nextWake returns when the read loop has to act without output from the
// CLI, or the zero time.
func (a *Adapter) nextWake() time.Time {
	next := a.parser.nextFlush()
	if !a.interruptAt.IsZero() {
		if at := a.interruptAt.Add(a.cfg.InterruptTimeout); next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}

// wake sends the snapshots the throttle held back, and ends an interrupted
// turn the CLI didn't end in time: the CLI may never confirm an interrupt
// (no result, or no "idle").
func (a *Adapter) wake() {
	a.sendRich(a.parser.flush())
	if a.interruptAt.IsZero() || a.cfg.Clock.Since(a.interruptAt) < a.cfg.InterruptTimeout {
		return
	}
	a.log.Warn("Claude Code did not end the interrupted turn; ending it here")
	a.sendRich(a.parser.endTurn())
	a.endTurn()
}

func (a *Adapter) handleLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		// Some builds print diagnostics on stdout.
		a.log.Debug("Ignoring non-JSON output from Claude Code", "line", string(line[:min(len(line), 200)]))
		return
	}
	var f frame
	if err := decode(line, &f); err != nil {
		// The line isn't logged: it can hold the user's prompt.
		a.log.Warn("Failed to parse Claude Code output", "error", err)
		return
	}
	switch f.Type {
	case "control_response":
		a.handleControlResponse(&f)
	case "control_request":
		a.handleControlRequest(&f)
	case "control_cancel_request":
		a.mu.Lock()
		_, ok := a.requests[f.RequestID]
		delete(a.requests, f.RequestID)
		a.mu.Unlock()
		if ok {
			a.sink.RequestClosed(f.RequestID)
		}
	case "system":
		a.handleSystem(&f)
	case "stream_event":
		a.sendRich(a.parser.streamEvent(&f))
	case "assistant":
		a.turnActivity()
		if stringField(f.Error) == "authentication_failed" {
			a.mu.Lock()
			a.turn.authFailed = true
			a.mu.Unlock()
		}
		msgs, events := a.parser.assistant(&f)
		a.sendRich(msgs)
		a.sendEvents(events)
	case "user":
		msgs, events := a.parser.user(&f)
		a.sendRich(msgs)
		a.sendEvents(events)
	case "result":
		a.handleResult(&f)
	case "rate_limit_event":
		a.handleRateLimit(f.RateLimitInfo)
	}
}

func (a *Adapter) sendRich(msgs []transcript.RichMessage) {
	for _, m := range msgs {
		a.sink.RichMessage(m)
	}
}

func (a *Adapter) sendEvents(events []transcript.SessionEvent) {
	if len(events) > 0 {
		a.sink.SessionEvents(events)
	}
}

func (a *Adapter) handleControlResponse(f *frame) {
	var r struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	}
	if decode(f.Response, &r) != nil {
		return
	}
	var err error
	if r.Subtype == "error" {
		err = fmt.Errorf("claude: %s", r.Error)
	}
	a.calls.Resolve(r.RequestID, controlResult{response: r.Response, err: err}, nil)
}

func (a *Adapter) handleControlRequest(f *frame) {
	var head struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(f.Request, &head)
	if head.Subtype != "can_use_tool" {
		// Hooks, SDK MCP servers and elicitations are not offered by this
		// adapter; answer so the CLI doesn't wait.
		a.refuse(f.RequestID, "AgentAPI does not support "+head.Subtype)
		return
	}
	var r canUseTool
	if err := decode(f.Request, &r); err != nil || r.ToolName == "" {
		a.log.Warn("Refusing a permission request from Claude Code that AgentAPI can't read", "request_id", f.RequestID)
		a.refuse(f.RequestID, "AgentAPI could not read the permission request")
		return
	}
	p := newPendingRequest(f.RequestID, r, a.args.allowBypass)
	p.req.CreatedAt = a.cfg.Clock.Now()
	a.mu.Lock()
	a.requests[f.RequestID] = p
	a.mu.Unlock()
	if p.req.Kind == native.RequestKindPlan {
		// The ExitPlanMode call can come without its input; the plan is
		// added to the permission request.
		a.sendRich(a.parser.patchToolInput(r.ToolUseID, r.Input))
	}
	a.sink.RequestOpened(p.req)
}

// refuse answers a control request with an error, so the CLI doesn't wait,
// without blocking the read loop.
func (a *Adapter) refuse(requestID, message string) {
	go func() {
		_ = a.write(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "error", "request_id": requestID, "error": message,
		}})
	}()
}

func (a *Adapter) handleSystem(f *frame) {
	switch f.Subtype {
	case "init":
		// The CLI starts a turn.
		a.turnActivity()
		if f.SessionID != "" {
			a.parser.sessionID = f.SessionID
		}
		if modeChanged, modelChanged := a.modeChanged(f.PermissionMode), a.modelRan(f.Model); modeChanged || modelChanged {
			a.reportSettings()
		}
		a.mu.Lock()
		prompt := a.prompt
		a.prompt = ""
		a.mu.Unlock()
		if f.SessionID == "" || f.SessionID == a.reportedRef {
			break
		}
		// A new session: the one AgentAPI started, or one /clear started.
		// It is resumable now that it has a turn, and is titled after the
		// prompt that started it. A resumed session keeps its id, which
		// the conversation already has, and its title.
		a.reportedRef = f.SessionID
		a.sink.SessionRef(f.SessionID)
		if prompt != "" {
			// Not in the read loop: a write can wait for the CLI.
			go a.requestTitle(prompt)
		}
	case "session_state_changed":
		a.mu.Lock()
		a.sessionStates = true
		a.mu.Unlock()
		switch f.State {
		case "running":
			a.turnActivity()
		case "idle":
			a.endTurn()
		}
	case "status":
		// Also sent when the permission mode changed: by a switch, or by
		// an approved plan.
		if a.modeChanged(f.PermissionMode) {
			a.reportSettings()
		}
	case "session_title_changed":
		if f.Title != "" {
			a.sink.SessionName(f.Title)
		}
	case "task_started":
		a.sendRich(a.parser.taskStarted(f))
	case "task_notification":
		a.sendRich(a.parser.taskNotification(f))
	case "api_retry":
		a.sink.Error("Claude Code is retrying a failed API request", st.ErrorLevelWarning)
	}
}

// turnActivity starts a turn if none is reported yet: the CLI works on a
// prompt it was sent or on one it started itself (after a background task
// finished).
func (a *Adapter) turnActivity() {
	a.mu.Lock()
	start := a.turn.phase != turnRunning
	a.beginTurnLocked(turnRunning)
	a.mu.Unlock()
	if start {
		a.sink.TurnStarted()
	}
}

// beginTurnLocked moves the turn to phase; a turn that begins gets the
// channel endTurnLocked closes. Caller holds a.mu.
func (a *Adapter) beginTurnLocked(phase turnPhase) {
	if a.turn.phase == turnIdle {
		a.turn.ended = make(chan struct{})
	}
	a.turn.phase = phase
}

// endTurnLocked clears the turn and returns what it was. Caller holds
// a.mu.
func (a *Adapter) endTurnLocked() turnState {
	turn := a.turn
	if turn.ended != nil {
		close(turn.ended)
	}
	a.turn = turnState{}
	return turn
}

func (a *Adapter) handleResult(f *frame) {
	a.sendRich(a.parser.endTurn())
	a.cacheUsed(f.Usage)
	limited := a.limitReached()
	a.mu.Lock()
	result := turnResult(f, a.turn.interrupted)
	if result.StopReason == native.StopReasonError && (limited || native.IsLimitError(result.Error)) {
		result.StopReason = native.StopReasonLimited
		if text := stringField(f.Result); text != "" {
			// Claude Code's own words, e.g. You've hit your limit ·
			// resets 3pm.
			result.Error = text
		}
	}
	inTurn := a.turn.phase != turnIdle
	started := a.started
	byResult := !a.sessionStates
	authFailed := a.turn.authFailed
	var replaced *native.TurnResult
	if inTurn {
		replaced, a.turn.result = a.turn.result, &result
	}
	a.mu.Unlock()
	if result.Error != "" && (authFailed || authFailure(f)) {
		// Reported before the turn ends, so the failed turn doesn't look
		// like the end of the problem.
		a.sink.LoginRequired(loginHint)
	}
	switch {
	case !inTurn:
		// A result outside a turn ends nothing; an error in it is still
		// worth showing.
		if result.Error != "" && started {
			a.sink.Error(result.Error, st.ErrorLevelError)
		}
		return
	case replaced != nil && replaced.Error != "":
		// With session state events a turn can have several results, and
		// only the last one ends it: an earlier failure is shown now.
		a.sink.Error(replaced.Error, st.ErrorLevelError)
	}
	if byResult {
		a.endTurn()
	}
}

// turnResult classifies a result frame. interrupted says the user
// interrupted the turn.
func turnResult(f *frame, interrupted bool) native.TurnResult {
	switch {
	case f.TerminalReason == "aborted_streaming" || f.TerminalReason == "aborted_tools":
		return native.TurnResult{StopReason: native.StopReasonInterrupted}
	case f.Subtype == "success" && !f.IsError:
		if f.StopReason != nil && *f.StopReason == native.StopReasonMaxTokens {
			return native.TurnResult{StopReason: native.StopReasonMaxTokens}
		}
		return native.TurnResult{StopReason: native.StopReasonEndTurn}
	case interrupted && f.Subtype == "error_during_execution":
		return native.TurnResult{StopReason: native.StopReasonInterrupted}
	}
	var details []string
	for _, e := range f.Errors {
		if e != "" && !strings.HasPrefix(e, "[ede_diagnostic]") {
			details = append(details, e)
		}
	}
	if text := stringField(f.Result); text != "" && len(details) == 0 {
		details = append(details, text)
	}
	reason := f.Subtype
	if reason == "success" {
		// A "success" with is_error: the terminal reason (e.g. api_error)
		// says what failed.
		reason = f.TerminalReason
	}
	msg := "Claude Code: the turn failed"
	if reason != "" {
		msg += " (" + reason + ")"
	}
	if status := strings.TrimSpace(string(f.APIErrorStatus)); status != "" && status != "null" {
		msg += ", API status " + status
	}
	if len(details) > 0 {
		msg += ": " + strings.Join(details, "; ")
	}
	return native.TurnResult{StopReason: native.StopReasonError, Error: msg}
}

// endTurn reports the end of the current turn, if one was sent or started.
// It runs in the read loop.
func (a *Adapter) endTurn() {
	a.interruptAt = time.Time{}
	a.mu.Lock()
	turn := a.endTurnLocked()
	active := turn.phase != turnIdle
	result := native.TurnResult{StopReason: native.StopReasonEndTurn}
	if turn.interrupted {
		result.StopReason = native.StopReasonInterrupted
	}
	if turn.result != nil {
		result = *turn.result
	}
	// The CLI withdraws its requests when a turn ends.
	a.requests = make(map[string]*pendingRequest)
	a.mu.Unlock()
	if active {
		a.sink.TurnCompleted(result)
		// Not in the read loop: a write can wait for the CLI.
		go a.readContextUsage()
	}
}
