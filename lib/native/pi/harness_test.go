package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/transcript"
)

// The fixtures in testdata are captures of `pi --mode rpc` (Pi 1.0.2):
// one record per line, {"dir":"in"|"out"|"meta","t":ms,"msg":{…}}. In
// these captures "in" is what the client wrote to Pi's stdin and "out" is
// what Pi wrote to stdout.

type record struct {
	Dir   string          `json:"dir"`
	Msg   json.RawMessage `json:"msg"`
	Raw   string          `json:"raw"`
	Event string          `json:"event"`
}

// loadFixture returns the records of each Pi process in a fixture.
func loadFixture(t *testing.T, name string) [][]record {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	procs := [][]record{nil}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var r record
			require.NoError(t, json.Unmarshal(line, &r))
			if r.Dir == "meta" && r.Event == "spawn" {
				procs = append(procs, nil)
			}
			if r.Dir != "meta" {
				procs[len(procs)-1] = append(procs[len(procs)-1], r)
			}
		}
		if err != nil {
			break
		}
	}
	return procs
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	m, err := decodeMap(raw)
	require.NoError(t, err)
	return m
}

// decodeMap decodes a JSON object, keeping numbers exact.
func decodeMap(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	err := dec.Decode(&m)
	return m, err
}

// fakePi plays Pi's side of the transport.
type fakePi struct {
	t *testing.T

	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter

	cmds        chan map[string]any
	stdinClosed chan struct{}
	// exitOnStdinClose: like Pi, exit once stdin closes.
	exitOnStdinClose bool
	// ignoreTerminate: keep running after SIGTERM.
	ignoreTerminate bool
	stderr          string

	mu       sync.Mutex
	log      []string
	argv     []string
	env      []string
	exitCh   chan struct{}
	exitErr  error
	exitOnce sync.Once
}

func newFakePi(t *testing.T) *fakePi {
	f := &fakePi{t: t, cmds: make(chan map[string]any, 64), stdinClosed: make(chan struct{}), exitCh: make(chan struct{}), exitOnStdinClose: true}
	f.stdinR, f.stdinW = io.Pipe()
	f.stdoutR, f.stdoutW = io.Pipe()
	go f.readCommands()
	t.Cleanup(func() { f.exit(nil) })
	return f
}

func (f *fakePi) start(c procutil.Command) (procutil.Process, error) {
	f.mu.Lock()
	f.argv, f.env = c.Args, c.Env
	f.mu.Unlock()
	return f, nil
}

func (f *fakePi) Stdin() io.WriteCloser { return f.stdinW }
func (f *fakePi) Stdout() io.Reader     { return f.stdoutR }

func (f *fakePi) readCommands() {
	reader := bufio.NewReader(f.stdinR)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if m, err := decodeMap(line); err == nil {
				f.record("cmd:" + m["type"].(string))
				f.cmds <- m
			}
		}
		if err != nil {
			f.record("stdin closed")
			close(f.stdinClosed)
			if f.exitOnStdinClose {
				f.exit(nil)
			}
			return
		}
	}
}

func (f *fakePi) record(s string) {
	f.mu.Lock()
	f.log = append(f.log, s)
	f.mu.Unlock()
}

func (f *fakePi) events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// exit ends the fake process: stdout closes and Wait returns err.
func (f *fakePi) exit(err error) {
	f.exitOnce.Do(func() {
		f.exitErr = err
		_ = f.stdoutW.Close()
		_ = f.stdinR.Close()
		close(f.exitCh)
	})
}

func (f *fakePi) Wait() error {
	<-f.exitCh
	return f.exitErr
}

func (f *fakePi) Terminate() error {
	f.record("SIGTERM")
	if !f.ignoreTerminate {
		f.exit(errors.New("signal: terminated"))
	}
	return nil
}

func (f *fakePi) Kill() error {
	f.record("SIGKILL")
	f.exit(errors.New("signal: killed"))
	return nil
}

func (f *fakePi) StderrTail() string { return f.stderr }

func (f *fakePi) nextCmd() map[string]any {
	f.t.Helper()
	select {
	case m := <-f.cmds:
		return m
	case <-time.After(5 * time.Second):
		f.t.Fatal("timed out waiting for a command from the adapter")
		return nil
	}
}

// harness replays a fixture against the adapter.
type harness struct {
	t     *testing.T
	ctx   context.Context
	pi    *fakePi
	a     *Adapter
	sink  *nativetest.Sink
	clock *quartz.Mock

	written atomic.Int64
	handled atomic.Int64

	startErr chan error
	// drive overrides the default action for an "in" record, by its id.
	drive map[string]func(m map[string]any) bool
	// calls holds the results of adapter calls made while driving, by
	// the id of the fixture record.
	callsMu sync.Mutex
	calls   map[string]chan error
	// sent are the commands the adapter wrote, in order.
	sent []map[string]any
	// stateSeen: the handshake get_state was answered.
	stateSeen bool
}

func newHarness(t *testing.T, cfg Config, resumeRef string, opts ...func(*harness)) *harness {
	t.Helper()
	h := &harness{
		t: t, ctx: context.Background(), pi: newFakePi(t), sink: nativetest.NewSink(t), clock: quartz.NewMock(t),
		startErr: make(chan error, 1),
		drive:    map[string]func(map[string]any) bool{},
		calls:    map[string]chan error{},
	}
	if cfg.Logger == nil && !testing.Verbose() {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg.StartProcess, cfg.Clock = h.pi.start, h.clock
	a := newAdapter(cfg, cfg.Args, resumeRef)
	a.lineHandled = func() { h.handled.Add(1) }
	a.host.InterruptGrace, a.host.StdinGrace, a.host.TermGrace = time.Second, time.Second, time.Second
	h.a = a
	h.sink.CheckUnlocked(func() { a.Settings() })
	for _, opt := range opts {
		opt(h)
	}
	t.Cleanup(func() { _ = a.Close() })
	go func() { h.startErr <- a.Start(h.ctx, h.sink) }()
	return h
}

// started waits for Start to return.
func (h *harness) started() {
	h.t.Helper()
	select {
	case err := <-h.startErr:
		require.NoError(h.t, err)
	case <-time.After(5 * time.Second):
		h.t.Fatal("Start didn't return")
	}
}

// answerState answers the handshake for fixtures captured without one.
func (h *harness) answerState(state string) {
	h.t.Helper()
	cmd := h.pi.nextCmd()
	require.Equal(h.t, "get_state", cmd["type"])
	h.sent = append(h.sent, cmd)
	h.writeLine(map[string]any{"id": cmd["id"], "type": "response", "command": "get_state", "success": true, "data": json.RawMessage(state)})
	h.stateSeen = true
}

func (h *harness) writeLine(m map[string]any) {
	h.t.Helper()
	b, err := json.Marshal(m)
	require.NoError(h.t, err)
	h.write(string(b))
}

// prompt calls StartTurn with text, recorded as call id, and returns the
// prompt command Pi got.
func (h *harness) prompt(id, text string) map[string]any {
	h.t.Helper()
	h.call(id, func() error { return h.a.StartTurn(h.ctx, native.Input{Text: text}) })
	cmd := h.pi.nextCmd()
	require.Equal(h.t, "prompt", cmd["type"])
	return cmd
}

// answer writes Pi's response to cmd, with data as JSON unless it is
// empty.
func (h *harness) answer(cmd map[string]any, data string) {
	h.t.Helper()
	m := map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": true}
	if data != "" {
		m["data"] = json.RawMessage(data)
	}
	h.writeLine(m)
}

// refuse writes Pi's error response to cmd.
func (h *harness) refuse(cmd map[string]any, message string) {
	h.t.Helper()
	h.writeLine(map[string]any{"id": cmd["id"], "type": "response", "command": cmd["type"], "success": false, "error": message})
}

// write writes a record given as JSON.
func (h *harness) write(record string) {
	h.t.Helper()
	h.written.Add(1)
	_, err := h.pi.stdoutW.Write([]byte(record + "\n"))
	require.NoError(h.t, err)
}

// settle waits until the adapter handled every line written.
func (h *harness) settle() {
	h.t.Helper()
	require.Eventually(h.t, func() bool { return h.handled.Load() >= h.written.Load() }, 5*time.Second, time.Millisecond)
}

// advance moves the clock on by d and waits until the event loop ran what
// the timers that fired posted to it.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	h.clock.Advance(d).MustWait(ctx)
	require.True(h.t, h.a.runOnLoop(func() {}), "the event loop stopped")
}

// call runs an adapter call in the background and records its result.
func (h *harness) call(id string, fn func() error) {
	ch := make(chan error, 1)
	h.callsMu.Lock()
	h.calls[id] = ch
	h.callsMu.Unlock()
	go func() { ch <- fn() }()
}

// result waits for the result of the call made for record id.
func (h *harness) result(id string) error {
	h.t.Helper()
	h.callsMu.Lock()
	ch := h.calls[id]
	h.callsMu.Unlock()
	require.NotNil(h.t, ch, "no call for %s", id)
	select {
	case err := <-ch:
		ch <- err
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatalf("call %s didn't return", id)
		return nil
	}
}

// done reports whether the call made for record id returned.
func (h *harness) done(id string) bool {
	h.callsMu.Lock()
	ch := h.calls[id]
	h.callsMu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case err := <-ch:
		ch <- err
		return true
	default:
		return false
	}
}

// replay plays the records: it writes Pi's output and, for each command
// the client sent, drives the adapter to send it and checks that it did.
func (h *harness) replay(records []record) {
	h.t.Helper()
	idMap := map[string]string{}
	skipped := map[string]bool{}
	for _, r := range records {
		switch r.Dir {
		case "in":
			if r.Raw != "" {
				continue
			}
			m := decode(h.t, r.Msg)
			id, _ := m["id"].(string)
			h.settle()
			drive := h.drive[id]
			if drive == nil {
				drive = h.defaultDrive
			}
			if !drive(m) {
				if id != "" {
					skipped[id] = true
				}
				continue
			}
			cmd := h.pi.nextCmd()
			h.sent = append(h.sent, cmd)
			require.Equal(h.t, normalize(m), normalize(cmd), "command for fixture record %s", id)
			if id != "" {
				idMap[id], _ = cmd["id"].(string)
			}
		case "out":
			m := decode(h.t, r.Msg)
			typ, _ := m["type"].(string)
			if id, ok := m["id"].(string); ok && (typ == "response" || typ == "bash_execution_update") {
				if skipped[id] {
					continue
				}
				if mapped, ok := idMap[id]; ok {
					m["id"] = mapped
				} else if id != "" {
					continue
				}
			}
			h.writeLine(m)
		}
	}
	h.settle()
}

// normalize drops what may differ between the fixture's command and the
// adapter's: ids, and steering through "steer" or a steering prompt.
func normalize(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k == "id" && m["type"] != "extension_ui_response" {
			continue
		}
		out[k] = v
	}
	if out["type"] == "steer" {
		out["type"] = "prompt"
		out["streamingBehavior"] = "steer"
	}
	return out
}

// defaultDrive makes the adapter send the command of an "in" record the
// way AgentAPI would. It returns false for commands AgentAPI doesn't send.
func (h *harness) defaultDrive(m map[string]any) bool {
	id, _ := m["id"].(string)
	text, _ := m["message"].(string)
	if m["type"] == "get_state" {
		if h.stateSeen {
			return false
		}
		h.stateSeen = true
		return true
	}
	// AgentAPI only calls the adapter once Start has returned.
	require.Eventually(h.t, func() bool {
		h.a.mu.Lock()
		defer h.a.mu.Unlock()
		return h.a.started
	}, 5*time.Second, time.Millisecond)
	switch m["type"] {
	case "prompt":
		input := native.Input{Text: text, Images: h.images(m)}
		switch m["streamingBehavior"] {
		case "steer":
			h.call(id, func() error { return h.a.Steer(h.ctx, input) })
		case nil:
			h.call(id, func() error { return h.a.StartTurn(h.ctx, input) })
		default:
			return false
		}
		return true
	case "steer":
		h.call(id, func() error { return h.a.Steer(h.ctx, native.Input{Text: text}) })
		return true
	case "abort":
		if !h.a.isTurnActive() {
			require.ErrorIs(h.t, h.a.Interrupt(h.ctx), native.ErrNoActiveTurn)
			return false
		}
		h.call(id, func() error { return h.a.Interrupt(h.ctx) })
		return true
	case "extension_ui_response":
		h.respond(m)
		return true
	}
	return false
}

// images writes the images of a fixture prompt to files.
func (h *harness) images(m map[string]any) []native.Image {
	list, _ := m["images"].([]any)
	var out []native.Image
	for i, item := range list {
		img := item.(map[string]any)
		data, err := base64.StdEncoding.DecodeString(img["data"].(string))
		require.NoError(h.t, err)
		path := filepath.Join(h.t.TempDir(), "image"+string(rune('0'+i)))
		require.NoError(h.t, os.WriteFile(path, data, 0o600))
		out = append(out, native.Image{Path: path, MediaType: img["mimeType"].(string)})
	}
	return out
}

// respond answers the dialog of an extension_ui_response record through
// Respond, mapping the fixture's answer onto the request.
func (h *harness) respond(m map[string]any) {
	id := m["id"].(string)
	var req native.Request
	require.Eventually(h.t, func() bool {
		var ok bool
		req, ok = h.sink.Request(id)
		return ok
	}, 5*time.Second, time.Millisecond)
	var resp native.Response
	switch {
	case m["cancelled"] == true:
		resp.OptionID = optionCancel
	case m["confirmed"] != nil:
		resp.OptionID = optionNo
		if m["confirmed"] == true {
			resp.OptionID = optionYes
		}
	case req.Kind == native.RequestKindApproval:
		resp.OptionID = m["value"].(string)
	default:
		resp.Answers = map[string][]string{req.Questions[0].ID: {m["value"].(string)}}
	}
	h.call(id, func() error { return h.a.Respond(h.ctx, id, resp) })
}

// sentTypes lists the types of the commands the adapter sent.
func (h *harness) sentTypes() []string {
	var out []string
	for _, c := range h.sent {
		out = append(out, c["type"].(string))
	}
	return out
}

// stripVolatile clears what legitimately differs between the adapter's
// messages and the session file's: IDs and timestamps.
func stripVolatile(msgs []transcript.RichMessage) []transcript.RichMessage {
	out := make([]transcript.RichMessage, len(msgs))
	for i, m := range msgs {
		m.MessageID = ""
		m.Timestamp = time.Time{}
		out[i] = m
	}
	return out
}

func equalJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	var va, vb any
	_ = json.Unmarshal(ja, &va)
	_ = json.Unmarshal(jb, &vb)
	return reflect.DeepEqual(va, vb)
}
