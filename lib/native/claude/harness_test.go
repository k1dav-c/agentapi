package claude

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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// fakeProcess plays Claude Code: the test writes its stdout and reads what
// the adapter wrote to its stdin.
type fakeProcess struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderr  *bytes.Reader

	// exitOnStdinClose makes the process exit when its input closes;
	// exitOnTerminate when it gets SIGTERM.
	exitOnStdinClose bool
	exitOnTerminate  bool

	mu         sync.Mutex
	cond       *sync.Cond
	written    []map[string]any
	terminated bool
	killed     bool

	once    sync.Once
	exited  chan struct{}
	exitErr error
}

func newFakeProcess(stderr string) *fakeProcess {
	p := &fakeProcess{exitOnStdinClose: true, exitOnTerminate: true, exited: make(chan struct{})}
	p.cond = sync.NewCond(&p.mu)
	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()
	if stderr != "" {
		p.stderr = bytes.NewReader([]byte(stderr))
	}
	go p.readStdin()
	return p
}

func (p *fakeProcess) readStdin() {
	reader := bufio.NewReader(p.stdinR)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var msg map[string]any
			if json.Unmarshal(line, &msg) == nil {
				p.mu.Lock()
				p.written = append(p.written, msg)
				p.cond.Broadcast()
				p.mu.Unlock()
			}
		}
		if err != nil {
			if p.exitOnStdinClose {
				p.exit(nil)
			}
			return
		}
	}
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProcess) Stdout() io.Reader     { return p.stdoutR }

func (p *fakeProcess) Stderr() io.Reader {
	if p.stderr == nil {
		return nil
	}
	return p.stderr
}

func (p *fakeProcess) Wait() error {
	<-p.exited
	return p.exitErr
}

func (p *fakeProcess) Terminate() error {
	p.mu.Lock()
	p.terminated = true
	p.mu.Unlock()
	if p.exitOnTerminate {
		p.exit(errors.New("signal: terminated"))
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.exit(errors.New("signal: killed"))
	return nil
}

// exit ends the process: its output closes and writes to its input fail.
func (p *fakeProcess) exit(err error) {
	p.once.Do(func() {
		p.exitErr = err
		_ = p.stdoutW.Close()
		_ = p.stdinR.CloseWithError(errors.New("process exited"))
		close(p.exited)
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	})
}

// send writes one stdout line and returns once the adapter read it.
func (p *fakeProcess) send(t *testing.T, line []byte) {
	t.Helper()
	_, err := p.stdoutW.Write(append(bytes.TrimSpace(line), '\n'))
	require.NoError(t, err)
}

// barrier returns once the adapter handled every line sent before: the
// pipe's next write completes only when the read loop reads again.
func (p *fakeProcess) barrier(t *testing.T) {
	t.Helper()
	p.send(t, []byte(`{"type":"keep_alive"}`))
}

// waitWritten waits for the n-th frame (0-based) the adapter wrote.
func (p *fakeProcess) waitWritten(t *testing.T, n int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	go func() {
		time.Sleep(5 * time.Second)
		p.mu.Lock()
		p.cond.Broadcast()
		p.mu.Unlock()
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.written) <= n {
		if time.Now().After(deadline) {
			t.Fatalf("adapter wrote %d frames, waiting for frame %d", len(p.written), n)
		}
		p.cond.Wait()
	}
	return p.written[n]
}

func (p *fakeProcess) writtenFrames() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.written...)
}

// recSink records the adapter's events and fails on concurrent calls.
type recSink struct {
	t      *testing.T
	active atomic.Int32

	mu       sync.Mutex
	log      []string
	rich     []jsonlwatcher.RichMessage
	events   []jsonlwatcher.SessionEvent
	requests []native.Request
	closed   []string
	refs     []string
	names    []string
	errs     []string
	results  []native.TurnResult
}

func (s *recSink) enter() func() {
	if s.active.Add(1) != 1 {
		s.t.Errorf("sink called concurrently")
	}
	return func() { s.active.Add(-1) }
}

func (s *recSink) record(entry string) {
	s.mu.Lock()
	s.log = append(s.log, entry)
	s.mu.Unlock()
}

func (s *recSink) TurnStarted() { defer s.enter()(); s.record("turn_started") }

func (s *recSink) TurnCompleted(r native.TurnResult) {
	defer s.enter()()
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()
	s.record("turn_completed:" + r.StopReason)
}

func (s *recSink) RichMessage(m jsonlwatcher.RichMessage) {
	defer s.enter()()
	s.mu.Lock()
	s.rich = append(s.rich, m)
	s.mu.Unlock()
}

func (s *recSink) SessionEvents(e []jsonlwatcher.SessionEvent) {
	defer s.enter()()
	s.mu.Lock()
	s.events = append(s.events, e...)
	s.mu.Unlock()
}

func (s *recSink) RequestOpened(r native.Request) {
	defer s.enter()()
	s.mu.Lock()
	s.requests = append(s.requests, r)
	s.mu.Unlock()
	s.record("request_opened:" + r.ID)
}

func (s *recSink) RequestClosed(id string) {
	defer s.enter()()
	s.mu.Lock()
	s.closed = append(s.closed, id)
	s.mu.Unlock()
	s.record("request_closed:" + id)
}

func (s *recSink) SessionName(n string) {
	defer s.enter()()
	s.mu.Lock()
	s.names = append(s.names, n)
	s.mu.Unlock()
}

func (s *recSink) SessionRef(r string) {
	defer s.enter()()
	s.mu.Lock()
	s.refs = append(s.refs, r)
	s.mu.Unlock()
}

func (s *recSink) Error(m string, _ st.ErrorLevel) {
	defer s.enter()()
	s.mu.Lock()
	s.errs = append(s.errs, m)
	s.mu.Unlock()
}

func (s *recSink) turnLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.log {
		if strings.HasPrefix(e, "turn_") {
			out = append(out, e)
		}
	}
	return out
}

func (s *recSink) request(id string) (native.Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r.ID == id {
			return r, true
		}
	}
	return native.Request{}, false
}

// final returns the last state of every rich message, in first-seen order.
func (s *recSink) final() []jsonlwatcher.RichMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []jsonlwatcher.RichMessage
	index := map[string]int{}
	for _, m := range s.rich {
		key := m.Role + "/" + m.MessageID
		if i, ok := index[key]; ok {
			out[i] = m
			continue
		}
		index[key] = len(out)
		out = append(out, m)
	}
	return out
}

func (s *recSink) snapshots(id string) []jsonlwatcher.RichMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []jsonlwatcher.RichMessage
	for _, m := range s.rich {
		if m.MessageID == id {
			out = append(out, m)
		}
	}
	return out
}

// record is one line of a capture: "in" was written to the CLI's stdin,
// "out" read from its stdout.
type record struct {
	Dir  string          `json:"dir"`
	Msg  json.RawMessage `json:"msg"`
	Argv []string        `json:"argv"`
}

// loadRuns splits a capture into its runs (one process each).
func loadRuns(t *testing.T, name string) [][]record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".ndjson"))
	require.NoError(t, err)
	var runs [][]record
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var r record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &r))
		if r.Dir == "meta" {
			runs = append(runs, nil)
			continue
		}
		require.NotEmpty(t, runs)
		runs[len(runs)-1] = append(runs[len(runs)-1], r)
	}
	require.NoError(t, scanner.Err())
	return runs
}

// replayHooks drive the parts of a capture that depend on the user.
type replayHooks struct {
	// respond answers a request the way the capture did; nil derives the
	// answer from the captured response.
	respond func(t *testing.T, req native.Request, captured map[string]any) native.Response
	// steer reports whether a user message is sent with Steer.
	steer func(n int, busy bool) bool
}

type replay struct {
	t     *testing.T
	a     *Adapter
	proc  *fakeProcess
	sink  *recSink
	cmd   Command
	ids   map[string]string
	errCh []chan error
	// expected lists the frames the capture wrote, as signatures.
	expected []string
}

// newReplay creates an adapter on a fake process.
func newReplay(t *testing.T, cfg Config, resumeRef string, stderr string) *replay {
	t.Helper()
	r := &replay{t: t, proc: newFakeProcess(stderr), sink: &recSink{t: t}, ids: map[string]string{}}
	cfg.StartProcess = func(c Command) (Process, error) {
		r.cmd = c
		return r.proc, nil
	}
	if cfg.Program == "" {
		cfg.Program = "claude"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Env == nil {
		cfg.Env = []string{}
	}
	if cfg.SnapshotInterval == 0 {
		cfg.SnapshotInterval = time.Hour
	}
	seq := 0
	if cfg.NewID == nil {
		cfg.NewID = func() string {
			seq++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", seq)
		}
	}
	a, err := New(cfg, resumeRef)
	require.NoError(t, err)
	r.a = a
	return r
}

// run replays the capture's records.
func (r *replay) run(recs []record, hooks replayHooks) {
	t := r.t
	t.Helper()
	ctx := context.Background()
	startErr := make(chan error, 1)
	started := false
	written := 0
	users := 0
	// next returns the adapter's next frame, stepping over the title
	// request it sends after the first prompt unless one is expected.
	next := func(wantTitle bool) map[string]any {
		for {
			f := r.proc.waitWritten(t, written)
			written++
			if wantTitle || subtypeOf(f) != "generate_session_title" {
				return f
			}
		}
	}
	for _, rec := range recs {
		var msg map[string]any
		_ = json.Unmarshal(rec.Msg, &msg)
		switch rec.Dir {
		case "in":
			switch msg["type"] {
			case "control_request":
				subtype := subtypeOf(msg)
				switch subtype {
				case "initialize":
					go func() { startErr <- r.a.Start(ctx, r.sink) }()
					started = true
				case "interrupt":
					r.proc.barrier(t)
					ch := make(chan error, 1)
					r.errCh = append(r.errCh, ch)
					go func() { ch <- r.a.Interrupt(ctx) }()
				case "generate_session_title":
					r.proc.barrier(t)
					request, _ := msg["request"].(map[string]any)
					desc, _ := request["description"].(string)
					r.a.requestTitle(desc)
				default:
					// Runtime requests the adapter doesn't make.
					continue
				}
				r.expected = append(r.expected, "control_request:"+subtype)
				got := next(subtype == "generate_session_title")
				require.Equal(t, "control_request:"+subtype, signature(got))
				r.ids[msg["request_id"].(string)] = got["request_id"].(string)
				if subtype == "initialize" {
					// Start returns once the handshake completes, below.
					continue
				}
			case "user":
				r.proc.barrier(t)
				input := inputFromCapture(t, msg)
				r.a.mu.Lock()
				busy := r.a.busy
				r.a.mu.Unlock()
				if hooks.steer != nil && hooks.steer(users, busy) {
					require.NoError(t, r.a.Steer(ctx, input))
				} else {
					require.NoError(t, r.a.StartTurn(ctx, input))
				}
				users++
				r.expected = append(r.expected, signature(msg))
				got := next(false)
				require.Equal(t, signature(msg), signature(got))
			case "control_response":
				r.proc.barrier(t)
				response, _ := msg["response"].(map[string]any)
				id, _ := response["request_id"].(string)
				captured, _ := response["response"].(map[string]any)
				req, ok := r.sink.request(id)
				require.True(t, ok, "request %s was not opened", id)
				var answer native.Response
				if hooks.respond != nil {
					answer = hooks.respond(t, req, captured)
				} else {
					answer = responseFromCapture(req, captured)
				}
				require.NoError(t, r.a.Respond(ctx, req.ID, answer))
				r.expected = append(r.expected, signature(msg))
				got := next(false)
				require.Equal(t, signature(msg), signature(got))
				gotResponse := got["response"].(map[string]any)["response"].(map[string]any)
				require.Equal(t, captured["behavior"], gotResponse["behavior"])
				if captured["behavior"] == "allow" {
					requireJSONEqual(t, captured["updatedInput"], gotResponse["updatedInput"])
				} else {
					require.Equal(t, captured["message"], gotResponse["message"])
				}
			}
		case "out":
			if msg["type"] == "control_response" {
				response, _ := msg["response"].(map[string]any)
				if id, ok := r.ids[fmt.Sprint(response["request_id"])]; ok {
					response["request_id"] = id
				}
				line, err := json.Marshal(msg)
				require.NoError(t, err)
				r.proc.send(t, line)
				if subtypeOfResponse(msg) && started {
					// The initialize answer: Start returns now.
					select {
					case err := <-startErr:
						require.NoError(t, err)
						started = false
					case <-time.After(5 * time.Second):
					}
				}
				continue
			}
			r.proc.send(t, rec.Msg)
		}
	}
	r.proc.barrier(t)
	for _, ch := range r.errCh {
		require.NoError(t, <-ch)
	}
}

// finish ends the process as the capture did and waits for the adapter.
func (r *replay) finish(exitErr error) error {
	r.proc.barrier(r.t)
	r.proc.exit(exitErr)
	done := make(chan error, 1)
	go func() { done <- r.a.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		r.t.Fatal("Wait did not return after the process exited")
		return nil
	}
}

// subtypeOfResponse reports whether a control_response answers initialize
// (its response carries the CLI version).
func subtypeOfResponse(msg map[string]any) bool {
	response, _ := msg["response"].(map[string]any)
	inner, _ := response["response"].(map[string]any)
	_, ok := inner["claude_code_version"]
	return ok
}

func subtypeOf(msg map[string]any) string {
	request, _ := msg["request"].(map[string]any)
	s, _ := request["subtype"].(string)
	return s
}

// signature identifies a frame written to the CLI, without ids.
func signature(msg map[string]any) string {
	switch msg["type"] {
	case "control_request":
		return "control_request:" + subtypeOf(msg)
	case "control_response":
		response, _ := msg["response"].(map[string]any)
		inner, _ := response["response"].(map[string]any)
		return fmt.Sprintf("control_response:%v", inner["behavior"])
	case "user":
		m, _ := msg["message"].(map[string]any)
		content, _ := json.Marshal(m["content"])
		return "user:" + string(content)
	}
	return fmt.Sprint(msg["type"])
}

// inputFromCapture rebuilds the native input of a captured user message;
// images are written to temporary files.
func inputFromCapture(t *testing.T, msg map[string]any) native.Input {
	t.Helper()
	m, _ := msg["message"].(map[string]any)
	switch content := m["content"].(type) {
	case string:
		return native.Input{Text: content}
	case []any:
		var input native.Input
		for _, b := range content {
			block, _ := b.(map[string]any)
			switch block["type"] {
			case "text":
				input.Text, _ = block["text"].(string)
			case "image":
				source, _ := block["source"].(map[string]any)
				data, err := base64.StdEncoding.DecodeString(fmt.Sprint(source["data"]))
				require.NoError(t, err)
				path := filepath.Join(t.TempDir(), fmt.Sprintf("image%d.png", len(input.Images)))
				require.NoError(t, os.WriteFile(path, data, 0o600))
				input.Images = append(input.Images, native.Image{Path: path, MediaType: fmt.Sprint(source["media_type"])})
			}
		}
		return input
	}
	t.Fatalf("unexpected user content %T", m["content"])
	return native.Input{}
}

// responseFromCapture derives the native response the capture gave.
func responseFromCapture(req native.Request, captured map[string]any) native.Response {
	message, _ := captured["message"].(string)
	switch req.Kind {
	case native.RequestKindQuestion:
		input, _ := captured["updatedInput"].(map[string]any)
		answers, _ := input["answers"].(map[string]any)
		out := native.Response{Answers: map[string][]string{}}
		for _, q := range req.Questions {
			if a, ok := answers[q.Question].(string); ok {
				out.Answers[q.ID] = strings.Split(a, ", ")
			}
		}
		return out
	case native.RequestKindPlan:
		if captured["behavior"] == "deny" {
			return native.Response{OptionID: optionDeny, Text: message}
		}
		return native.Response{OptionID: optionAcceptEdits}
	case native.RequestKindApproval:
	}
	if captured["behavior"] == "deny" {
		return native.Response{OptionID: optionDeny, Text: message}
	}
	if _, ok := captured["updatedPermissions"]; ok {
		return native.Response{OptionID: optionAllowAlways}
	}
	return native.Response{OptionID: optionAllow}
}

func requireJSONEqual(t *testing.T, want, got any) {
	t.Helper()
	w, err := json.Marshal(want)
	require.NoError(t, err)
	g, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(w), string(g))
}

// findBlock returns the final block of a tool call.
func findBlock(msgs []jsonlwatcher.RichMessage, blockType, toolUseID string) (jsonlwatcher.RichMessage, jsonlwatcher.RichContentBlock, bool) {
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == blockType && b.ToolUseID == toolUseID {
				return m, b, true
			}
		}
	}
	return jsonlwatcher.RichMessage{}, jsonlwatcher.RichContentBlock{}, false
}
