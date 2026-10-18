package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/transcript"
)

const waitTimeout = 5 * time.Second

// --- fake process ---

// fakeProc is app-server as pipes: the test writes its stdout and reads
// its stdin. Like app-server it exits when its stdin is closed.
type fakeProc struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter

	once       sync.Once
	done       chan struct{}
	err        error
	terminated atomic.Int32
	killed     atomic.Int32
	// ignoreStdinClose keeps the process running after stdin closes.
	ignoreStdinClose bool
	ignoreTerm       bool
}

func newFakeProc() *fakeProc {
	p := &fakeProc{done: make(chan struct{})}
	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()
	return p
}

func (p *fakeProc) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProc) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProc) Wait() error           { <-p.done; return p.err }
func (p *fakeProc) StderrTail() string    { return "" }

func (p *fakeProc) Terminate() error {
	p.terminated.Add(1)
	if !p.ignoreTerm {
		p.exit(errors.New("signal: terminated"))
	}
	return nil
}

func (p *fakeProc) Kill() error {
	p.killed.Add(1)
	p.exit(errors.New("signal: killed"))
	return nil
}

func (p *fakeProc) exit(err error) {
	p.once.Do(func() {
		p.err = err
		_ = p.stdoutW.Close()
		_ = p.stdinR.Close()
		close(p.done)
	})
}

// --- recording sink ---

// recorder is the shared recording sink.
type recorder = nativetest.Sink

func newRecorder(t *testing.T) *recorder { return nativetest.NewSink(t) }

// recorderState is what a recorder recorded, under the names these tests
// use.
type recorderState struct {
	log         []string
	completions []native.TurnResult
	started     int
	opened      []native.Request
	closed      []string
	names       []string
	refs        []string
	errors      []string
	events      []transcript.SessionEvent
	logins      []native.LoginRequired
	settings    []native.Settings
}

func snapshot(r *recorder) recorderState {
	s := r.State()
	return recorderState{
		log: s.Log, completions: s.Results, started: s.Started, opened: s.Requests, closed: s.Closed,
		names: s.Names, refs: s.Refs, errors: s.Errors, events: s.Events, logins: s.Logins, settings: s.Settings,
	}
}

func (s recorderState) eventsOf(kind string) []transcript.SessionEvent {
	return nativetest.SinkState{Events: s.events}.EventsOf(kind)
}

// --- harness ---

type harness struct {
	t    *testing.T
	a    *Adapter
	proc *fakeProc
	rec  *recorder
	// sent receives what the adapter writes to app-server.
	sent chan rpcMessage
	// all keeps everything the adapter wrote.
	allMu sync.Mutex
	all   []rpcMessage

	// fed counts the messages written as app-server.
	fed int64

	actions sync.WaitGroup
	// errs collects the results of actions, by name.
	errMu sync.Mutex
	errs  map[string]error
}

type harnessOption func(*Config)

func newHarness(t *testing.T, resumeRef string, args []string, opts ...harnessOption) *harness {
	t.Helper()
	proc := newFakeProc()
	cfg := Config{
		Program: "codex",
		Args:    args,
		CWD:     "/work/scratch",
		Env:     []string{"PATH=/usr/bin", "CODEX_THREAD_ID=parent"},
		StartProcess:  func(procutil.Command) (procutil.Process, error) { return proc, nil },
		Logger:  slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(&cfg)
	}
	factory, err := NewFactory(cfg)
	require.NoError(t, err)
	adapter, err := factory(resumeRef)
	require.NoError(t, err)
	h := &harness{
		t: t, a: adapter.(*Adapter), proc: proc, rec: newRecorder(t),
		sent: make(chan rpcMessage, 1024), errs: map[string]error{},
	}
	h.rec.CheckUnlocked(func() { h.a.Settings() })
	go func() {
		br := bufio.NewReader(proc.stdinR)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				var msg rpcMessage
				if jerr := json.Unmarshal(line, &msg); jerr != nil {
					t.Errorf("adapter wrote invalid JSON %q: %v", line, jerr)
				}
				h.allMu.Lock()
				h.all = append(h.all, msg)
				h.allMu.Unlock()
				h.sent <- msg
			}
			if err != nil {
				if !proc.ignoreStdinClose {
					proc.exit(nil)
				}
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = h.a.Close()
		proc.exit(nil)
	})
	return h
}

// act runs f in the background, as the conversation would call the
// adapter, and records its error under name.
func (h *harness) act(name string, f func(ctx context.Context) error) {
	h.actions.Add(1)
	go func() {
		defer h.actions.Done()
		ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
		defer cancel()
		err := f(ctx)
		h.errMu.Lock()
		h.errs[name] = err
		h.errMu.Unlock()
	}()
}

// settle waits for the running actions to return.
func (h *harness) settle() {
	h.t.Helper()
	done := make(chan struct{})
	go func() { h.actions.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		h.t.Fatal("adapter call did not return")
	}
}

func (h *harness) err(name string) error {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.errs[name]
}

func (h *harness) start() {
	h.act("start", func(ctx context.Context) error { return h.a.Start(ctx, h.rec) })
}

// feed writes a message as app-server.
func (h *harness) feed(msg json.RawMessage) {
	h.t.Helper()
	_, err := h.proc.stdoutW.Write(append(append([]byte(nil), msg...), '\n'))
	require.NoError(h.t, err)
	h.fed++
}

// sync waits until the adapter has handled everything fed so far.
func (h *harness) sync() {
	h.t.Helper()
	h.eventually(func() bool { return h.a.handled.Load() >= h.fed }, "the adapter to handle its input")
}

// feedJSON feeds a message written by hand, on one line.
func (h *harness) feedJSON(s string) {
	h.t.Helper()
	var buf bytes.Buffer
	require.NoError(h.t, json.Compact(&buf, []byte(s)))
	h.feed(buf.Bytes())
}

// answerAccount answers account/read with a ChatGPT login.
func (h *harness) answerAccount() {
	h.t.Helper()
	read := h.expectSent("account/read")
	h.feedJSON(`{"id":` + string(read.ID) + `,"result":{"account":{"type":"chatgpt","email":null,"planType":"plus"},"requiresOpenaiAuth":true}}`)
}

// expectSent returns the next message the adapter wrote and checks its
// method ("" for a response).
func (h *harness) expectSent(method string) rpcMessage {
	h.t.Helper()
	select {
	case msg := <-h.sent:
		require.Equal(h.t, method, msg.Method, "adapter wrote %+v", msg)
		return msg
	case <-time.After(waitTimeout):
		h.t.Fatalf("adapter did not write %q", method)
		return rpcMessage{}
	}
}

func (h *harness) expectNothingSent() {
	h.t.Helper()
	select {
	case msg := <-h.sent:
		h.t.Fatalf("adapter wrote %s %s unexpectedly", msg.Method, msg.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

// eventually waits for cond.
func (h *harness) eventually(cond func() bool, what string) {
	h.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// requestID is the native ID of server request rpcID.
func (h *harness) requestID(rpcID json.RawMessage) string {
	return h.a.prefix + "-" + strings.Trim(string(rpcID), `"`)
}

// finish closes the adapter like the conversation does and waits for it.
func (h *harness) finish() {
	h.t.Helper()
	h.sync()
	h.settle()
	require.NoError(h.t, h.a.Close())
	done := make(chan error, 1)
	go func() { done <- h.a.Wait() }()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		h.t.Fatal("Wait did not return after Close")
	}
}

// --- fixture replay ---

type fixtureLine struct {
	Dir   string          `json:"dir"`
	Msg   json.RawMessage `json:"msg"`
	Event string          `json:"event"`
	Text  string          `json:"text"`
}

func loadFixture(t *testing.T, name string) []fixtureLine {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".ndjson"))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var lines []fixtureLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var l fixtureLine
		require.NoError(t, json.Unmarshal(sc.Bytes(), &l))
		lines = append(lines, l)
	}
	require.NoError(t, sc.Err())
	return lines
}

// inputFromParams turns turn/start or turn/steer params of a capture into
// the user message the adapter was given.
func inputFromParams(t *testing.T, params json.RawMessage) native.Input {
	var p struct {
		Input []userInput `json:"input"`
	}
	require.NoError(t, json.Unmarshal(params, &p))
	var in native.Input
	for _, item := range p.Input {
		switch item.Type {
		case "text":
			in.Text = item.Text
		case "localImage":
			in.Images = append(in.Images, native.Image{Path: item.Path, MediaType: "image/png"})
		}
	}
	return in
}

// responseFromResult turns a captured answer to a server request into the
// native response that produces it.
func responseFromResult(t *testing.T, result json.RawMessage) native.Response {
	var r struct {
		Decision json.RawMessage                       `json:"decision"`
		Action   string                                `json:"action"`
		Answers  map[string]struct{ Answers []string } `json:"answers"`
	}
	require.NoError(t, json.Unmarshal(result, &r))
	switch {
	case len(r.Decision) > 0:
		var name string
		if json.Unmarshal(r.Decision, &name) == nil {
			return native.Response{OptionID: name}
		}
		var obj map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(r.Decision, &obj))
		for k := range obj {
			return native.Response{OptionID: k}
		}
	case r.Action != "":
		return native.Response{OptionID: r.Action}
	case r.Answers != nil:
		answers := map[string][]string{}
		for k, v := range r.Answers {
			answers[k] = v.Answers
		}
		return native.Response{Answers: answers}
	}
	t.Fatalf("can't answer %s", result)
	return native.Response{}
}

// replay plays a capture against the adapter: it feeds what app-server
// sent, makes the adapter calls the capture's client made, and checks that
// the adapter writes the same requests. Requests the adapter doesn't make
// (thread/name/set, model/list…) are skipped with their responses.
func (h *harness) replay(lines []fixtureLine) {
	h.t.Helper()
	idMap := map[string]json.RawMessage{} // capture id -> adapter id
	for _, l := range lines {
		switch l.Dir {
		case "in":
			var msg rpcMessage
			require.NoError(h.t, json.Unmarshal(l.Msg, &msg))
			if msg.Method == "" && len(msg.ID) > 0 {
				id, ok := idMap[string(msg.ID)]
				if !ok {
					continue
				}
				var raw map[string]json.RawMessage
				require.NoError(h.t, json.Unmarshal(l.Msg, &raw))
				raw["id"] = id
				h.feed(mustJSON(raw))
				continue
			}
			h.feed(l.Msg)
		case "out":
			h.replayOut(l.Msg, idMap)
		}
	}
}

func (h *harness) replayOut(raw json.RawMessage, idMap map[string]json.RawMessage) {
	h.t.Helper()
	var msg rpcMessage
	require.NoError(h.t, json.Unmarshal(raw, &msg))
	mapID := func(sent rpcMessage) { idMap[string(msg.ID)] = sent.ID }
	switch msg.Method {
	case "initialize":
		h.start()
		mapID(h.expectSent("initialize"))
	case "initialized":
		h.expectSent("initialized")
		// The captures predate the login check.
		h.answerAccount()
	case "thread/start", "thread/resume":
		mapID(h.expectSent(msg.Method))
	case "turn/start":
		h.sync()
		h.settle()
		input := inputFromParams(h.t, msg.Params)
		h.act("turn/start", func(ctx context.Context) error { return h.a.StartTurn(ctx, input) })
		mapID(h.expectSent("turn/start"))
	case "turn/steer", "turn/interrupt":
		h.sync()
		h.settle()
		h.a.mu.Lock()
		active := h.a.activeTurn != ""
		h.a.mu.Unlock()
		name := msg.Method
		if !active {
			// The capture's client sent it anyway; the adapter refuses
			// without asking app-server.
			name += "-idle"
		}
		h.act(name, func(ctx context.Context) error {
			if msg.Method == "turn/steer" {
				return h.a.Steer(ctx, inputFromParams(h.t, msg.Params))
			}
			return h.a.Interrupt(ctx)
		})
		if !active {
			h.settle()
			require.ErrorIs(h.t, h.err(name), native.ErrNoActiveTurn)
			h.expectNothingSent()
			return
		}
		mapID(h.expectSent(msg.Method))
	case "":
		// An answer to a server request.
		h.sync()
		id := h.requestID(msg.ID)
		h.eventually(func() bool { _, ok := h.rec.Request(id); return ok }, "request "+id)
		h.settle()
		h.a.mu.Lock()
		_, live := h.a.requests[id]
		h.a.mu.Unlock()
		resp := responseFromResult(h.t, msg.Result)
		if !live {
			// A late reply: the turn ended, app-server dropped the request
			// and the adapter declined it (see closeTurnRequests).
			declined := h.expectSent("")
			require.JSONEq(h.t, string(msg.ID), string(declined.ID))
			require.JSONEq(h.t, `{"decision":"decline"}`, string(declined.Result))
			require.ErrorIs(h.t, h.a.Respond(context.Background(), id, resp), native.ErrRequestNotFound)
			h.expectNothingSent()
			return
		}
		h.act("respond-"+id, func(ctx context.Context) error { return h.a.Respond(ctx, id, resp) })
		sent := h.expectSent("")
		require.JSONEq(h.t, string(msg.ID), string(sent.ID))
		require.JSONEq(h.t, string(msg.Result), string(sent.Result))
	default:
		// Not something the adapter sends.
	}
}

// sentParams returns the params of the first message the adapter wrote
// with method.
func (h *harness) sentParams(method string) json.RawMessage {
	h.allMu.Lock()
	defer h.allMu.Unlock()
	for _, m := range h.all {
		if m.Method == method {
			return m.Params
		}
	}
	h.t.Fatalf("adapter never wrote %s", method)
	return nil
}

// runFixture replays a capture and closes the adapter.
func runFixture(t *testing.T, name, resumeRef string, args []string, opts ...harnessOption) *harness {
	t.Helper()
	h := newHarness(t, resumeRef, args, opts...)
	h.replay(loadFixture(t, name))
	h.finish()
	require.NoError(t, h.err("start"))
	return h
}
