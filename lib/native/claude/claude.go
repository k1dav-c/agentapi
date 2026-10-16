// Package claude drives Claude Code through its stream-json protocol: the
// CLI runs headless (-p) with stream-json on stdin and stdout, and
// permission prompts arrive as control requests on the same stream.
package claude

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
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

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
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
	// StartProcess starts the process; nil runs it with os/exec.
	StartProcess StartProcess
	Clock        quartz.Clock
	// SnapshotInterval is the least time between two snapshots of a
	// streaming message; zero means 100ms.
	SnapshotInterval time.Duration
	// StopTimeout is how long Close waits for the process to exit after
	// closing its stdin before it sends SIGTERM, and again before SIGKILL.
	// Zero means 3s and 2s.
	StopTimeout time.Duration
	// NewID generates session and message ids; nil uses random UUIDs.
	NewID func() string
}

// NewFactory returns a factory for Claude Code adapters.
func NewFactory(cfg Config) native.Factory {
	return func(resumeRef string) (native.Adapter, error) {
		return New(cfg, resumeRef)
	}
}

// New creates an adapter that starts a new session, or resumes resumeRef.
func New(cfg Config, resumeRef string) (*Adapter, error) {
	args, err := parseArgs(cfg.Args)
	if err != nil {
		return nil, err
	}
	if cfg.Program == "" {
		cfg.Program = "claude"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Env == nil {
		cfg.Env = os.Environ()
	}
	if cfg.StartProcess == nil {
		cfg.StartProcess = startExec
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if cfg.SnapshotInterval == 0 {
		cfg.SnapshotInterval = 100 * time.Millisecond
	}
	if cfg.NewID == nil {
		cfg.NewID = newUUID
	}
	a := &Adapter{
		cfg:       cfg,
		args:      args,
		resumeRef: resumeRef,
		parser:    newStreamParser(cfg.Clock, cfg.SnapshotInterval),
		controls:  make(map[string]*control),
		requests:  make(map[string]*pendingRequest),
		exited:    make(chan struct{}),
		readDone:  make(chan struct{}),
	}
	if resumeRef == "" {
		a.sessionID = cfg.NewID()
	}
	return a, nil
}

// Adapter is a native.Adapter for Claude Code.
type Adapter struct {
	cfg       Config
	args      parsedArgs
	resumeRef string
	// sessionID is the id passed with --session-id for a new session.
	sessionID string

	proc   Process
	sink   native.Sink
	parser *streamParser

	writeMu sync.Mutex

	mu      sync.Mutex
	started bool
	// busy: a turn was sent or the agent is working on one, until the
	// agent is idle again.
	busy bool
	// turnReported: TurnStarted was sent for the current turn.
	turnReported bool
	// sessionStates: the CLI reports session_state_changed, so "idle" ends
	// a turn (otherwise every result does).
	sessionStates bool
	// result is the latest result of the current turn.
	result      *native.TurnResult
	interrupted bool
	refReported bool
	titleAsked  bool
	controlSeq  int
	controls    map[string]*control
	requests    map[string]*pendingRequest
	closing     bool

	stderrMu   sync.Mutex
	stderrTail []byte

	exited   chan struct{}
	exitErr  error
	readDone chan struct{}
	stopOnce sync.Once
}

var _ native.Adapter = (*Adapter)(nil)

// control is a control request the adapter sent and the CLI hasn't
// answered yet. Answers are delivered on ch, or handed to onResponse in the
// read loop.
type control struct {
	ch         chan controlResult
	onResponse func(json.RawMessage)
}

type controlResult struct {
	response json.RawMessage
	err      error
}

// stderrTailSize is how much of stderr is kept for exit errors.
const stderrTailSize = 4 << 10

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Capabilities implements native.Adapter.
func (a *Adapter) Capabilities() native.Capabilities {
	return native.Capabilities{Steer: true, Interrupt: true, Resume: !a.args.noPersistence, Images: true}
}

// Start implements native.Adapter: it starts the CLI and completes the
// initialize handshake.
func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	cmd := Command{
		Program: a.cfg.Program,
		Args:    buildArgs(a.args, a.sessionID, a.resumeRef),
		Env:     buildEnv(a.cfg.Env),
		Dir:     a.cfg.CWD,
	}
	a.cfg.Logger.Info("Starting Claude Code", "program", cmd.Program, "args", cmd.Args, "resume", a.resumeRef != "")
	proc, err := a.cfg.StartProcess(cmd)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.proc = proc
	a.sink = sink
	a.mu.Unlock()

	stderrDone := make(chan struct{})
	if stderr := proc.Stderr(); stderr != nil {
		go a.readStderr(stderr, stderrDone)
	} else {
		close(stderrDone)
	}
	go a.readLoop(proc.Stdout())
	go func() {
		err := proc.Wait()
		<-stderrDone
		a.mu.Lock()
		a.exitErr = a.withStderr(err)
		a.mu.Unlock()
		close(a.exited)
	}()

	response, err := a.request(ctx, "initialize", map[string]any{"hooks": nil})
	if err != nil {
		_ = a.Close()
		select {
		case <-a.exited:
			exitErr := a.waitErr()
			if exitErr == nil {
				// It can exit cleanly after a fatal error (an unknown
				// session to resume); stderr says why.
				exitErr = a.withStderr(err)
			}
			return fmt.Errorf("claude exited during startup: %w", exitErr)
		default:
		}
		return fmt.Errorf("initialize claude: %w", err)
	}
	var info struct {
		Version        string `json:"claude_code_version"`
		PermissionMode string `json:"current_permission_mode"`
	}
	_ = json.Unmarshal(response, &info)
	a.cfg.Logger.Info("Claude Code is ready", "version", info.Version, "permission_mode", info.PermissionMode)

	a.mu.Lock()
	a.started = true
	resumed := a.resumeRef != ""
	if resumed {
		a.refReported = true
		a.titleAsked = true
	}
	a.mu.Unlock()
	if resumed {
		// The read loop hasn't called the sink for this session yet: the
		// CLI says nothing before the first turn.
		sink.SessionRef(a.resumeRef)
	}
	return nil
}

// withStderr adds the tail of stderr to an exit error.
func (a *Adapter) withStderr(err error) error {
	if err == nil {
		return nil
	}
	a.stderrMu.Lock()
	tail := strings.TrimSpace(string(a.stderrTail))
	a.stderrMu.Unlock()
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, tail)
}

func (a *Adapter) readStderr(r io.Reader, done chan<- struct{}) {
	defer close(done)
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			a.cfg.Logger.Debug("claude stderr", "line", line)
			a.stderrMu.Lock()
			a.stderrTail = append(a.stderrTail, line...)
			a.stderrTail = append(a.stderrTail, '\n')
			if len(a.stderrTail) > stderrTailSize {
				a.stderrTail = a.stderrTail[len(a.stderrTail)-stderrTailSize:]
			}
			a.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// write sends one frame to the CLI.
func (a *Adapter) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	a.mu.Lock()
	proc := a.proc
	a.mu.Unlock()
	if proc == nil {
		return native.ErrNotStarted
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if _, err := proc.Stdin().Write(data); err != nil {
		return fmt.Errorf("write to claude: %w", err)
	}
	return nil
}

// sendControl writes a control request and registers c for its answer.
func (a *Adapter) sendControl(subtype string, fields map[string]any, c *control) (string, error) {
	a.mu.Lock()
	if a.closing {
		a.mu.Unlock()
		return "", errors.New("claude is shutting down")
	}
	a.controlSeq++
	id := fmt.Sprintf("agentapi_%d", a.controlSeq)
	a.controls[id] = c
	a.mu.Unlock()
	request := map[string]any{"subtype": subtype}
	for k, v := range fields {
		request[k] = v
	}
	if err := a.write(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		a.mu.Lock()
		delete(a.controls, id)
		a.mu.Unlock()
		return "", err
	}
	return id, nil
}

// request sends a control request and waits for its answer.
func (a *Adapter) request(ctx context.Context, subtype string, fields map[string]any) (json.RawMessage, error) {
	c := &control{ch: make(chan controlResult, 1)}
	id, err := a.sendControl(subtype, fields, c)
	if err != nil {
		return nil, err
	}
	select {
	case res := <-c.ch:
		return res.response, res.err
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.controls, id)
		a.mu.Unlock()
		return nil, ctx.Err()
	case <-a.readDone:
		return nil, errors.New("claude closed its output")
	}
}

// userMessage builds a user frame. Images go before the text, as Claude
// Code expands slash commands only from the last text block.
func (a *Adapter) userMessage(input native.Input) (map[string]any, error) {
	var content any = input.Text
	if len(input.Images) > 0 {
		blocks := make([]map[string]any, 0, len(input.Images)+1)
		for _, image := range input.Images {
			data, err := os.ReadFile(image.Path)
			if err != nil {
				return nil, fmt.Errorf("read image: %w", err)
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "base64", "media_type": image.MediaType,
					"data": base64.StdEncoding.EncodeToString(data),
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
	if a.busy {
		a.mu.Unlock()
		return native.ErrTurnActive
	}
	a.busy = true
	a.interrupted = false
	askTitle := !a.titleAsked
	a.titleAsked = true
	a.mu.Unlock()
	if err := a.write(msg); err != nil {
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
		return err
	}
	if askTitle {
		a.requestTitle(input.Text)
	}
	return nil
}

// requestTitle asks the CLI to title a new session after its first prompt,
// as the terminal UI does. The answer is reported from the read loop.
func (a *Adapter) requestTitle(prompt string) {
	if runes := []rune(prompt); len(runes) > 2000 {
		prompt = string(runes[:2000])
	}
	c := &control{onResponse: func(response json.RawMessage) {
		var r struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(response, &r) == nil && r.Title != "" {
			a.sink.SessionName(r.Title)
		}
	}}
	if _, err := a.sendControl("generate_session_title", map[string]any{"description": prompt, "persist": true}, c); err != nil {
		a.cfg.Logger.Debug("Failed to ask Claude Code for a session title", "error", err)
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
	busy := a.busy
	a.mu.Unlock()
	if !busy {
		return native.ErrNoActiveTurn
	}
	return a.write(msg)
}

// Interrupt implements native.Adapter. The turn ends with the result the
// CLI sends after it stopped.
func (a *Adapter) Interrupt(ctx context.Context) error {
	a.mu.Lock()
	busy := a.busy
	if busy {
		a.interrupted = true
	}
	a.mu.Unlock()
	if !busy {
		return native.ErrNoActiveTurn
	}
	_, err := a.request(ctx, "interrupt", nil)
	return err
}

// Respond implements native.Adapter.
func (a *Adapter) Respond(ctx context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	p, ok := a.requests[requestID]
	a.mu.Unlock()
	if !ok {
		return native.ErrRequestNotFound
	}
	answer, err := p.response(response)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if _, ok := a.requests[requestID]; !ok {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	delete(a.requests, requestID)
	a.mu.Unlock()
	return a.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype": "success", "request_id": requestID, "response": answer,
		},
	})
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

// Close implements native.Adapter: closing stdin lets the CLI finish and
// exit on its own; SIGTERM and SIGKILL follow if it doesn't.
func (a *Adapter) Close() error {
	a.mu.Lock()
	proc := a.proc
	a.closing = true
	a.mu.Unlock()
	if proc == nil {
		return nil
	}
	a.stopOnce.Do(func() {
		grace := a.cfg.StopTimeout
		kill := a.cfg.StopTimeout
		if grace == 0 {
			grace, kill = 3*time.Second, 2*time.Second
		}
		a.writeMu.Lock()
		_ = proc.Stdin().Close()
		a.writeMu.Unlock()
		if a.waitExit(grace) {
			return
		}
		a.cfg.Logger.Info("Claude Code did not exit after its input closed; terminating it")
		_ = proc.Terminate()
		if a.waitExit(kill) {
			return
		}
		a.cfg.Logger.Warn("Claude Code did not exit after SIGTERM; killing it")
		_ = proc.Kill()
	})
	<-a.exited
	return nil
}

func (a *Adapter) waitExit(d time.Duration) bool {
	timer := a.cfg.Clock.NewTimer(d, "claude", "stop")
	defer timer.Stop()
	select {
	case <-a.exited:
		return true
	case <-timer.C:
		return false
	}
}

// readLoop handles the CLI's output until it closes. It is the only caller
// of the sink after Start.
func (a *Adapter) readLoop(stdout io.Reader) {
	defer close(a.readDone)
	reader := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			a.handleLine(line)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				a.cfg.Logger.Debug("Reading Claude Code output failed", "error", err)
			}
			break
		}
	}
	a.mu.Lock()
	controls := a.controls
	a.controls = make(map[string]*control)
	a.requests = make(map[string]*pendingRequest)
	a.mu.Unlock()
	for _, c := range controls {
		if c.ch != nil {
			c.ch <- controlResult{err: errors.New("claude exited")}
		}
	}
}

func (a *Adapter) handleLine(line []byte) {
	if line[0] != '{' {
		// Some builds print diagnostics on stdout.
		a.cfg.Logger.Debug("Ignoring non-JSON output from Claude Code", "line", string(line[:min(len(line), 200)]))
		return
	}
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		a.cfg.Logger.Warn("Failed to parse Claude Code output", "error", err, "line", string(line[:min(len(line), 200)]))
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
		msgs, events := a.parser.assistant(&f)
		a.sendRich(msgs)
		a.sendEvents(events)
	case "user":
		msgs, events := a.parser.user(&f)
		a.sendRich(msgs)
		a.sendEvents(events)
	case "result":
		a.handleResult(&f)
	}
}

func (a *Adapter) sendRich(msgs []jsonlwatcher.RichMessage) {
	for _, m := range msgs {
		a.sink.RichMessage(m)
	}
}

func (a *Adapter) sendEvents(events []jsonlwatcher.SessionEvent) {
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
	if json.Unmarshal(f.Response, &r) != nil {
		return
	}
	a.mu.Lock()
	c, ok := a.controls[r.RequestID]
	delete(a.controls, r.RequestID)
	a.mu.Unlock()
	if !ok {
		return
	}
	var err error
	if r.Subtype == "error" {
		err = fmt.Errorf("claude: %s", r.Error)
	}
	if c.ch != nil {
		c.ch <- controlResult{response: r.Response, err: err}
	}
	if c.onResponse != nil && err == nil {
		c.onResponse(r.Response)
	}
}

func (a *Adapter) handleControlRequest(f *frame) {
	var head struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(f.Request, &head)
	if head.Subtype != "can_use_tool" {
		// Hooks, SDK MCP servers and elicitations are not offered by this
		// adapter; answer so the CLI doesn't wait.
		id := f.RequestID
		go func() {
			_ = a.write(map[string]any{"type": "control_response", "response": map[string]any{
				"subtype": "error", "request_id": id, "error": "AgentAPI does not support " + head.Subtype,
			}})
		}()
		return
	}
	var r canUseTool
	if json.Unmarshal(f.Request, &r) != nil {
		return
	}
	p := newPendingRequest(f.RequestID, r)
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

func (a *Adapter) handleSystem(f *frame) {
	switch f.Subtype {
	case "init":
		a.turnActivity()
		if f.SessionID != "" {
			a.parser.sessionID = f.SessionID
		}
		a.mu.Lock()
		report := !a.refReported && f.SessionID != ""
		a.refReported = a.refReported || report
		a.mu.Unlock()
		if report {
			// The session is resumable once it has a turn.
			a.sink.SessionRef(f.SessionID)
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
	start := !a.turnReported
	a.turnReported = true
	a.busy = true
	a.mu.Unlock()
	if start {
		a.sink.TurnStarted()
	}
}

func (a *Adapter) handleResult(f *frame) {
	a.sendRich(a.parser.endTurn())
	result := a.turnResult(f)
	a.mu.Lock()
	inTurn := a.busy || a.turnReported
	started := a.started
	a.result = &result
	byResult := !a.sessionStates
	a.mu.Unlock()
	if !inTurn {
		// A result outside a turn ends nothing; an error in it is still
		// worth showing.
		if result.Error != "" && started {
			a.sink.Error(result.Error, st.ErrorLevelError)
		}
		a.mu.Lock()
		a.result = nil
		a.mu.Unlock()
		return
	}
	if byResult {
		a.endTurn()
	}
}

// turnResult classifies a result frame.
func (a *Adapter) turnResult(f *frame) native.TurnResult {
	a.mu.Lock()
	interrupted := a.interrupted
	a.mu.Unlock()
	switch {
	case f.TerminalReason == "aborted_streaming" || f.TerminalReason == "aborted_tools":
		return native.TurnResult{StopReason: "interrupted"}
	case f.Subtype == "success" && !f.IsError:
		reason := "end_turn"
		if f.StopReason != nil && *f.StopReason != "" {
			reason = *f.StopReason
		}
		return native.TurnResult{StopReason: reason}
	case interrupted && f.Subtype == "error_during_execution":
		return native.TurnResult{StopReason: "interrupted"}
	}
	var details []string
	for _, e := range f.Errors {
		if !strings.HasPrefix(e, "[ede_diagnostic]") {
			details = append(details, e)
		}
	}
	if text := stringField(f.Result); text != "" && len(details) == 0 {
		details = append(details, text)
	}
	msg := "Claude Code: the turn failed (" + f.Subtype + ")"
	if status := strings.TrimSpace(string(f.APIErrorStatus)); status != "" && status != "null" {
		msg += ", API status " + status
	}
	if len(details) > 0 {
		msg += ": " + strings.Join(details, "; ")
	}
	return native.TurnResult{StopReason: "error", Error: msg}
}

// endTurn reports the end of the current turn, if one was sent or started.
func (a *Adapter) endTurn() {
	a.mu.Lock()
	active := a.busy || a.turnReported
	result := native.TurnResult{StopReason: "end_turn"}
	if a.result != nil {
		result = *a.result
	}
	a.busy = false
	a.turnReported = false
	a.result = nil
	a.interrupted = false
	// The CLI withdraws its requests when a turn ends.
	a.requests = make(map[string]*pendingRequest)
	a.mu.Unlock()
	if active {
		a.sink.TurnCompleted(result)
	}
}
