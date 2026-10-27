package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/transcript"
)

// started returns an adapter on a fake process that completed the
// handshake.
func started(t *testing.T, cfg Config, proc *fakeProcess) (*Adapter, *nativetest.Sink) {
	t.Helper()
	return resumed(t, cfg, "", proc)
}

// resumed is started for an adapter that resumes resumeRef.
func resumed(t *testing.T, cfg Config, resumeRef string, proc *fakeProcess) (*Adapter, *nativetest.Sink) {
	t.Helper()
	cfg.StartProcess = func(procutil.Command) (procutil.Process, error) { return proc, nil }
	if cfg.AuthStatus == nil {
		cfg.AuthStatus = loggedIn
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg.Env = []string{}
	a := newTestAdapter(t, cfg, resumeRef)
	sink := nativetest.NewSink(t)
	errCh := make(chan error, 1)
	go func() { errCh <- a.Start(context.Background(), sink) }()
	init := proc.waitWritten(t, 0)
	require.Equal(t, "control_request:initialize", signature(init))
	proc.answer(t, init, `{"claude_code_version":"2.1.299"}`)
	require.NoError(t, <-errCh)
	return a, sink
}

func TestStartFailure(t *testing.T) {
	t.Parallel()

	t.Run("exit during startup", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("No conversation found with session ID: 6943de8c\n")
		a := newTestAdapter(t, Config{
			StartProcess: func(procutil.Command) (procutil.Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, "6943de8c")
		go func() {
			proc.waitWritten(t, 0)
			proc.exit(errExit1)
		}()
		err := a.Start(context.Background(), nativetest.NewSink(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No conversation found with session ID: 6943de8c")
		assert.ErrorIs(t, a.Wait(), errExit1)
	})

	t.Run("clean exit during startup", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("No conversation found with session ID: 6943de8c\n")
		a := newTestAdapter(t, Config{
			StartProcess: func(procutil.Command) (procutil.Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, "6943de8c")
		go func() {
			proc.waitWritten(t, 0)
			proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":0}`))
			proc.exit(nil)
		}()
		err := a.Start(context.Background(), nativetest.NewSink(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No conversation found with session ID: 6943de8c")
	})

	t.Run("no answer", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a := newTestAdapter(t, Config{
			StartProcess: func(procutil.Command) (procutil.Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			StopTimeout:  10 * time.Millisecond,
		}, "")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		require.Error(t, a.Start(ctx, nativetest.NewSink(t)))
		// The process is stopped.
		select {
		case <-proc.exited:
		case <-time.After(5 * time.Second):
			t.Fatal("the process was not stopped")
		}
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("stdin", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, _ := started(t, Config{StopTimeout: time.Hour}, proc)
		require.NoError(t, a.Close())
		require.NoError(t, a.Close())
		require.NoError(t, a.Wait())
		assert.False(t, proc.terminated)
	})

	t.Run("sigterm", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		proc.exitOnStdinClose = false
		a, _ := started(t, Config{StopTimeout: 10 * time.Millisecond}, proc)
		require.NoError(t, a.Close())
		assert.True(t, proc.terminated)
		assert.False(t, proc.killed)
		assert.Error(t, a.Wait())
	})

	t.Run("sigkill", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		proc.exitOnStdinClose = false
		proc.exitOnTerminate = false
		a, _ := started(t, Config{StopTimeout: 10 * time.Millisecond}, proc)
		require.NoError(t, a.Close())
		assert.True(t, proc.terminated)
		assert.True(t, proc.killed)
	})

	t.Run("a write is blocked", func(t *testing.T) {
		t.Parallel()
		// The CLI reads the initialize and get_settings requests, then
		// hangs.
		proc := newFakeProcess("")
		proc.stallAfter = 2
		a, _ := started(t, Config{StopTimeout: 10 * time.Millisecond}, proc)
		turnErr := make(chan error, 1)
		go func() { turnErr <- a.StartTurn(context.Background(), native.Input{Text: "hi"}) }()
		require.Eventually(t, a.writer.Writing, 5*time.Second, time.Millisecond, "the prompt is not being written")
		closed := make(chan error, 1)
		go func() { closed <- a.Close() }()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Close waits for the blocked write")
		}
		// Closing stdin failed the write.
		require.Error(t, <-turnErr)
		assert.True(t, proc.terminated)
	})
}

// A CLI that stops reading can't hold StartTurn past its context: the
// prompt may still reach the CLI if it reads again, so the error says it is
// unconfirmed, and the turn can start again.
func TestStartTurnGivesUpWithTheContext(t *testing.T) {
	t.Parallel()
	// The CLI reads the initialize and get_settings requests, then hangs.
	proc := newFakeProcess("")
	proc.stallAfter = 2
	a, _ := started(t, Config{StopTimeout: 10 * time.Millisecond}, proc)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := a.StartTurn(ctx, native.Input{Text: "hi"})
	require.ErrorIs(t, err, native.ErrUnconfirmed)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, a.Close())
}

func TestTurnErrors(t *testing.T) {
	t.Parallel()
	a := newTestAdapter(t, Config{}, "")
	require.ErrorIs(t, a.StartTurn(context.Background(), native.Input{Text: "hi"}), native.ErrNotStarted)
	require.ErrorIs(t, a.Wait(), native.ErrNotStarted)
	require.NoError(t, a.Close())

	proc := newFakeProcess("")
	a, sink := started(t, Config{}, proc)
	ctx := context.Background()
	require.ErrorIs(t, a.Steer(ctx, native.Input{Text: "more"}), native.ErrNoActiveTurn)
	require.ErrorIs(t, a.Interrupt(ctx), native.ErrNoActiveTurn)
	require.ErrorIs(t, a.Respond(ctx, "nope", native.Response{OptionID: "allow"}), native.ErrRequestNotFound)

	require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
	require.ErrorIs(t, a.StartTurn(ctx, native.Input{Text: "again"}), native.ErrTurnActive)
	require.NoError(t, a.Steer(ctx, native.Input{Text: "more"}))

	// A failed turn reports the error without the CLI's diagnostics.
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":null,"terminal_reason":"completed","api_error_status":529,"errors":["[ede_diagnostic] x","Overloaded"]}`))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	proc.barrier(t)
	require.Len(t, sink.State().Results, 1)
	assert.Equal(t, "error", sink.State().Results[0].StopReason)
	assert.Equal(t, "Claude Code: the turn failed (error_during_execution), API status 529: Overloaded", sink.State().Results[0].Error)

	// Requests the adapter doesn't offer are refused, not left waiting.
	proc.send(t, []byte(`{"type":"control_request","request_id":"hook-1","request":{"subtype":"hook_callback","callback_id":"x"}}`))
	var reply map[string]any
	for i := 0; ; i++ {
		f := proc.waitWritten(t, i)
		if f["type"] == "control_response" {
			reply = f["response"].(map[string]any)
			break
		}
	}
	assert.Equal(t, "error", reply["subtype"])
	assert.Equal(t, "hook-1", reply["request_id"])
	require.NoError(t, a.Close())
}

func TestSnapshotThrottle(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	p := newStreamParser(clock, 100*time.Millisecond)
	ev := func(event string) *frame {
		return &frame{Type: "stream_event", APIMessageID: "msg_1", Event: json.RawMessage(event)}
	}
	var sent int
	feed := func(f *frame) {
		sent += len(p.streamEvent(f))
	}
	feed(ev(`{"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"output_tokens":1}}}`))
	feed(ev(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
	assert.Equal(t, 1, sent, "the first block is sent at once")
	for i := 0; i < 10; i++ {
		feed(ev(fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"%d"}}`, i)))
	}
	assert.Equal(t, 1, sent, "deltas within the interval are held back")
	clock.Advance(100 * time.Millisecond)
	feed(ev(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`))
	assert.Equal(t, 2, sent)

	// The final block and the stop reason are always sent.
	msgs, _ := p.assistant(&frame{Type: "assistant", Message: json.RawMessage(`{"id":"msg_1","model":"m","content":[{"type":"text","text":"0123456789x!"}]}`)})
	require.Len(t, msgs, 1)
	assert.Equal(t, "0123456789x!", msgs[0].Content[0].Text)
	msgs = p.streamEvent(ev(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":3,"output_tokens":9}}`))
	require.Len(t, msgs, 1)
	assert.Equal(t, "end_turn", msgs[0].StopReason)
	assert.Equal(t, 9, msgs[0].Usage.OutputTokens)
	assert.Empty(t, p.endTurn(), "nothing is left unsent")
}

func TestTurnResult(t *testing.T) {
	t.Parallel()
	stop := func(s string) *string { return &s }
	for _, tc := range []struct {
		f           frame
		interrupted bool
		want        native.TurnResult
	}{
		{f: frame{Subtype: "success", StopReason: stop("end_turn")}, want: native.TurnResult{StopReason: native.StopReasonEndTurn}},
		{f: frame{Subtype: "success", StopReason: stop("max_tokens")}, want: native.TurnResult{StopReason: native.StopReasonMaxTokens}},
		// Other API stop reasons are not TurnResult stop reasons.
		{f: frame{Subtype: "success", StopReason: stop("stop_sequence")}, want: native.TurnResult{StopReason: native.StopReasonEndTurn}},
		{f: frame{Subtype: "error_during_execution", IsError: true, TerminalReason: "aborted_tools"}, want: native.TurnResult{StopReason: native.StopReasonInterrupted}},
		{f: frame{Subtype: "error_during_execution", IsError: true}, interrupted: true, want: native.TurnResult{StopReason: native.StopReasonInterrupted}},
		{
			f:    frame{Subtype: "success", IsError: true, TerminalReason: "api_error", Result: json.RawMessage(`"Not logged in"`)},
			want: native.TurnResult{StopReason: native.StopReasonError, Error: "Claude Code: the turn failed (api_error): Not logged in"},
		},
		{
			f:    frame{Subtype: "success", IsError: true, Result: json.RawMessage(`"API Error"`)},
			want: native.TurnResult{StopReason: native.StopReasonError, Error: "Claude Code: the turn failed: API Error"},
		},
		{
			f:    frame{Subtype: "error_max_turns", IsError: true, Errors: []string{"", "[ede_diagnostic] x", "Reached the maximum number of turns"}},
			want: native.TurnResult{StopReason: native.StopReasonError, Error: "Claude Code: the turn failed (error_max_turns): Reached the maximum number of turns"},
		},
	} {
		assert.Equal(t, tc.want, turnResult(&tc.f, tc.interrupted), "%+v", tc.f)
	}
}

func TestEarlierFailureIsShown(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	a, sink := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(context.Background(), native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"api_error_status":529,"errors":["Overloaded"]}`))
	// A background task finished and woke Claude up; that part worked.
	// Only the last result ends the turn.
	proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1"}`))
	proc.send(t, []byte(`{"type":"result","subtype":"success","is_error":false,"result":"done"}`))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	proc.barrier(t)
	assert.Equal(t, []string{"error: Claude Code: the turn failed (error_during_execution), API status 529: Overloaded"}, sink.State().Errors)
	assert.Equal(t, []native.TurnResult{{StopReason: native.StopReasonEndTurn}}, sink.State().Results)
	require.NoError(t, a.Close())
}

func TestNewSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	turn := func(t *testing.T, a *Adapter, proc *fakeProcess, prompt, sessionID string) {
		t.Helper()
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: prompt}))
		proc.send(t, []byte(fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q}`, sessionID)))
		proc.send(t, []byte(fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":%q}`, sessionID)))
		proc.barrier(t)
	}
	// titles returns what the title requests describe, once there are n.
	titles := func(t *testing.T, proc *fakeProcess, n int) []string {
		t.Helper()
		var out []string
		require.Eventually(t, func() bool {
			out = nil
			for _, f := range proc.writtenFrames() {
				if subtypeOf(f) == "generate_session_title" {
					out = append(out, f["request"].(map[string]any)["description"].(string))
				}
			}
			return len(out) >= n
		}, 5*time.Second, time.Millisecond)
		return out
	}

	t.Run("clear", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, sink := started(t, Config{}, proc)
		turn(t, a, proc, "first", "s1")
		turn(t, a, proc, "/clear", "s1")
		turn(t, a, proc, "second", "s2")
		// /clear started a new session: a restart resumes it, and it gets
		// a title too.
		assert.Equal(t, []string{"s1", "s2"}, sink.State().Refs)
		assert.ElementsMatch(t, []string{"first", "second"}, titles(t, proc, 2))
		require.NoError(t, a.Close())
	})

	t.Run("resumed", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, sink := resumed(t, Config{}, "s1", proc)
		// The resumed session has its id and title already.
		turn(t, a, proc, "again", "s1")
		assert.Empty(t, sink.State().Refs)
		turn(t, a, proc, "/clear", "s1")
		turn(t, a, proc, "next", "s2")
		assert.Equal(t, []string{"s2"}, sink.State().Refs)
		assert.Equal(t, []string{"next"}, titles(t, proc, 1))
		require.NoError(t, a.Close())
	})
}

func TestFieldsOfUnexpectedType(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	a, sink := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(context.Background(), native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	// A field of another shape doesn't lose the request.
	proc.send(t, []byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"tool_use_id":"t1","title":{"text":"x"}}}`))
	// A request without a tool can't be asked; it is refused, so the CLI
	// doesn't wait for it.
	proc.send(t, []byte(`{"type":"control_request","request_id":"r2","request":{"subtype":"can_use_tool","tool_name":{"name":"Bash"},"input":{}}}`))
	proc.barrier(t)
	req, ok := sink.Request("r1")
	require.True(t, ok)
	assert.Equal(t, "Bash: ls", req.Title)
	assert.Equal(t, "t1", req.ToolUseID)
	_, ok = sink.Request("r2")
	assert.False(t, ok)
	var reply map[string]any
	for i := 0; reply == nil; i++ {
		f := proc.waitWritten(t, i)
		if response, _ := f["response"].(map[string]any); f["type"] == "control_response" && response["request_id"] == "r2" {
			reply = response
		}
	}
	assert.Equal(t, "error", reply["subtype"])

	// A result whose errors aren't all strings still ends the turn.
	proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":[{"code":529},"Overloaded"]}`))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	proc.barrier(t)
	assert.Equal(t, []string{"turn_started", "turn_completed:error"}, sink.TurnLog())
	assert.Equal(t, "Claude Code: the turn failed (error_during_execution): Overloaded", sink.State().Results[0].Error)
	require.NoError(t, a.Close())
}

func TestRespondWriteFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proc := newFakeProcess("")
	a, _ := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	proc.send(t, []byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"tool_use_id":"t1"}}`))
	proc.barrier(t)
	proc.mu.Lock()
	proc.failWrites = 1
	proc.mu.Unlock()
	require.Error(t, a.Respond(ctx, "r1", native.Response{OptionID: optionAllow}))
	// The CLI didn't get the answer: the request can still be answered.
	require.NoError(t, a.Respond(ctx, "r1", native.Response{OptionID: optionAllow}))
	answer := proc.waitWritten(t, 2)
	assert.Equal(t, "control_response:allow", signature(answer))
	require.ErrorIs(t, a.Respond(ctx, "r1", native.Response{OptionID: optionAllow}), native.ErrRequestNotFound)
	require.NoError(t, a.Close())
}

func TestInterruptTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	interrupt := func(t *testing.T, a *Adapter, proc *fakeProcess) {
		t.Helper()
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
		proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
		errCh := make(chan error, 1)
		go func() { errCh <- a.Interrupt(ctx) }()
		request, _ := proc.waitRequest(t, 1, "interrupt")
		proc.answer(t, request, `{}`)
		require.NoError(t, <-errCh)
		proc.barrier(t)
	}

	t.Run("not confirmed", func(t *testing.T) {
		t.Parallel()
		clock := quartz.NewMock(t)
		proc := newFakeProcess("")
		a, sink := started(t, Config{Clock: clock}, proc)
		interrupt(t, a, proc)
		// Claude Code never ends the turn: no result, no "idle". It ends
		// here after InterruptTimeout (10s by default).
		assert.Equal(t, []string{"turn_started"}, sink.TurnLog())
		clock.Advance(10 * time.Second).MustWait(ctx)
		require.Eventually(t, func() bool { return len(sink.TurnLog()) == 2 }, 5*time.Second, time.Millisecond)
		assert.Equal(t, []string{"turn_started", "turn_completed:interrupted"}, sink.TurnLog())
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "again"}))
		require.NoError(t, a.Close())
	})

	t.Run("confirmed", func(t *testing.T) {
		t.Parallel()
		clock := quartz.NewMock(t)
		proc := newFakeProcess("")
		a, sink := started(t, Config{Clock: clock, InterruptTimeout: time.Second}, proc)
		interrupt(t, a, proc)
		proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`))
		proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
		proc.barrier(t)
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "again"}))
		proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
		proc.barrier(t)
		// No timer is left to end the next turn.
		_, pending := clock.Peek()
		assert.False(t, pending)
		assert.Equal(t, []string{"turn_started", "turn_completed:interrupted", "turn_started"}, sink.TurnLog())
		require.NoError(t, a.Close())
	})
}

// A frame whose fields changed type is read all the same: a lost result
// would never end the turn. Each change, and each frame type the adapter
// doesn't know, is logged once, with the CLI's version.
func TestDriftedFramesAreRead(t *testing.T) {
	t.Parallel()
	logger, log := nativetest.NewLog()
	proc := newFakeProcess("")
	a, sink := started(t, Config{Logger: logger}, proc)
	ctx := context.Background()
	for range 2 {
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
		proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1"}`))
		proc.send(t, []byte(`{"type":"assistant","message":{"id":"msg_1","model":"m","content":[{"type":"text","text":"Hello"}],"usage":{"output_tokens":"many"}}}`))
		proc.send(t, []byte(`{"type":"brand_new","detail":1}`))
		proc.send(t, []byte(`{"type":"result","subtype":"success","is_error":"no","usage":{"cache_read_input_tokens":"many"}}`))
		proc.barrier(t)
	}
	assert.Equal(t, []string{"turn_started", "turn_completed:end_turn", "turn_started", "turn_completed:end_turn"}, sink.TurnLog())
	msg, ok := sink.Message("msg_1", "assistant")
	require.True(t, ok, "the message is shown without its usage")
	assert.Equal(t, "Hello", msg.Content[0].Text)

	assert.Len(t, log.Matching("type=assistant", "field=usage.output_tokens", "version=2.1.299"), 1, "%v", log.Warnings())
	assert.Len(t, log.Matching("type=result", "field=is_error"), 1)
	assert.Len(t, log.Matching("type=brand_new", "version=2.1.299"), 1)
	assert.Empty(t, log.Matching("type=keep_alive"), "the barrier's frames are known")
	require.NoError(t, a.Close())
}

func TestHeldBackSnapshotIsSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := quartz.NewMock(t)
	proc := newFakeProcess("")
	a, sink := started(t, Config{Clock: clock, SnapshotInterval: 100 * time.Millisecond}, proc)
	event := func(event string) {
		proc.send(t, []byte(`{"type":"stream_event","api_message_id":"msg_1","event":`+event+`}`))
	}
	event(`{"type":"message_start","message":{"id":"msg_1","model":"m"}}`)
	event(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	event(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}`)
	proc.barrier(t)
	require.Len(t, sink.Snapshots("msg_1"), 1, "the delta came within the interval")
	// The model pauses (summarized thinking comes in bursts): the delta
	// is sent when the interval is over, not with the next event.
	clock.Advance(100 * time.Millisecond).MustWait(ctx)
	require.Eventually(t, func() bool { return len(sink.Snapshots("msg_1")) == 2 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "Let me think", sink.Snapshots("msg_1")[1].Content[0].Thinking)
	require.NoError(t, a.Close())
}

func TestStreamEventsWithoutMessageID(t *testing.T) {
	t.Parallel()
	p := newStreamParser(quartz.NewMock(t), 0)
	var last []transcript.RichMessage
	for _, event := range []string{
		`{"type":"message_start","message":{"id":"msg_1","model":"m"}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
	} {
		if msgs := p.streamEvent(&frame{Type: "stream_event", Event: json.RawMessage(event)}); len(msgs) > 0 {
			last = msgs
		}
	}
	require.Len(t, last, 1)
	assert.Equal(t, "msg_1", last[0].MessageID)
	assert.Equal(t, "hello", last[0].Content[0].Text)
}

func TestUnstreamedMessage(t *testing.T) {
	t.Parallel()
	p := newStreamParser(quartz.NewMock(t), 0)
	// The message Claude Code makes up for a turn without a login is not
	// streamed; its frame has the stop reason and usage.
	msgs, _ := p.assistant(&frame{Type: "assistant", Message: json.RawMessage(`{"id":"52c0c71b","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"Not logged in · Please run /login"}]}`)})
	require.Len(t, msgs, 1)
	assert.Equal(t, "stop_sequence", msgs[0].StopReason)
	require.NotNil(t, msgs[0].Usage)
	assert.Equal(t, "<synthetic>", msgs[0].Model)
}

func TestUserMessageIDs(t *testing.T) {
	t.Parallel()
	// A user frame without a uuid gets an id of its own. A restarted
	// adapter must not reuse one: the conversation keeps the messages of
	// the earlier adapter.
	marker := &frame{Type: "user", Message: json.RawMessage(`{"role":"user","content":"[Request interrupted by user]"}`)}
	id := func(p *streamParser) string {
		t.Helper()
		msgs, _ := p.user(marker)
		require.Len(t, msgs, 1)
		return msgs[0].MessageID
	}
	p := newStreamParser(quartz.NewMock(t), 0)
	first, second := id(p), id(p)
	assert.NotEqual(t, first, second)
	assert.NotEqual(t, first, id(newStreamParser(quartz.NewMock(t), 0)))
}

func TestCloseDuringTheLoginCheck(t *testing.T) {
	t.Parallel()
	checking, release := make(chan struct{}), make(chan struct{})
	spawned := make(chan struct{}, 1)
	a := newTestAdapter(t, Config{
		StartProcess: func(procutil.Command) (procutil.Process, error) {
			spawned <- struct{}{}
			return newFakeProcess(""), nil
		},
		AuthStatus: func(ctx context.Context, cmd procutil.Command) ([]byte, error) {
			close(checking)
			<-release
			return loggedIn(ctx, cmd)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, "")
	started := make(chan error, 1)
	go func() { started <- a.Start(context.Background(), nativetest.NewSink(t)) }()
	<-checking
	// A restart closes the adapter while its login is checked.
	require.NoError(t, a.Close())
	close(release)
	require.ErrorIs(t, <-started, errClosed)
	select {
	case <-spawned:
		t.Fatal("the CLI was started after Close")
	default:
	}
}

func TestFailedInterruptLeavesTheTurnAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proc := newFakeProcess("")
	a, sink := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	errCh := make(chan error, 1)
	go func() { errCh <- a.Interrupt(ctx) }()
	request, _ := proc.waitRequest(t, 1, "interrupt")
	proc.send(t, []byte(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"busy"}}`, request["request_id"])))
	require.Error(t, <-errCh)
	// The turn then fails: it ends with its error, not as interrupted.
	proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["boom"]}`))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	proc.barrier(t)
	require.Eventually(t, func() bool { return len(sink.TurnLog()) == 2 }, 5*time.Second, time.Millisecond)
	assert.NotEqual(t, "turn_completed:interrupted", sink.TurnLog()[1])
	require.NoError(t, a.Close())
}

// A snapshot the throttle held back is sent when the CLI's output ends,
// so the last text it streamed before it exited isn't lost.
func TestHeldBackSnapshotIsSentAtExit(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	_, sink := started(t, Config{Clock: quartz.NewMock(t), SnapshotInterval: time.Hour}, proc)
	event := func(event string) {
		proc.send(t, []byte(`{"type":"stream_event","api_message_id":"msg_1","event":`+event+`}`))
	}
	event(`{"type":"message_start","message":{"id":"msg_1","model":"m"}}`)
	event(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	event(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Half"}}`)
	event(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" a sentence"}}`)
	proc.barrier(t)
	require.Len(t, sink.Snapshots("msg_1"), 1, "the second delta is held back")
	proc.exit(errors.New("signal: killed"))
	require.Eventually(t, func() bool { return len(sink.Snapshots("msg_1")) == 2 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "Half a sentence", sink.Snapshots("msg_1")[1].Content[0].Text)
}

// The answer to a control request that comes right before the CLI's
// output ends is the request's answer, not "claude exited".
func TestAnswerJustBeforeExit(t *testing.T) {
	t.Parallel()
	for range 20 {
		proc := newFakeProcess("")
		a, _ := started(t, Config{}, proc)
		got := make(chan error, 1)
		go func() {
			_, err := a.request(context.Background(), "mcp_status", nil, nil)
			got <- err
		}()
		request, _ := proc.waitRequest(t, 0, "mcp_status")
		_, err := proc.stdoutW.Write([]byte(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`+"\n", request["request_id"])))
		require.NoError(t, err)
		proc.exit(nil)
		require.NoError(t, <-got)
		<-a.readDone
		_, err = a.request(context.Background(), "mcp_status", nil, nil)
		assert.ErrorIs(t, err, errExited, "the CLI is gone")
	}
}

// A CLI that answers initialize with an error is still running: the start
// fails with its answer, not "exited during startup".
func TestInitializeRefused(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	a := newTestAdapter(t, Config{
		StartProcess: func(procutil.Command) (procutil.Process, error) { return proc, nil },
		AuthStatus:   loggedIn,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		StopTimeout:  10 * time.Millisecond,
	}, "")
	errCh := make(chan error, 1)
	go func() { errCh <- a.Start(context.Background(), nativetest.NewSink(t)) }()
	init := proc.waitWritten(t, 0)
	proc.send(t, []byte(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"unsupported option"}}`, init["request_id"])))
	err := <-errCh
	require.Error(t, err)
	assert.Equal(t, "initialize claude: claude: unsupported option", err.Error())
}

// Close interrupts the running turn and waits for it to end before it
// closes stdin: closing it mid-turn leaves the session with a tool call
// that has no result.
func TestCloseInterruptsTheTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proc := newFakeProcess("")
	a, sink := started(t, Config{}, proc)
	a.closeGrace = 5 * time.Second
	require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	proc.barrier(t)

	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	request, _ := proc.waitRequest(t, 1, "interrupt")
	select {
	case <-proc.exited:
		t.Fatal("stdin closed before the turn ended")
	case <-closed:
		t.Fatal("Close returned before the turn ended")
	case <-time.After(50 * time.Millisecond):
	}
	proc.answer(t, request, `{}`)
	proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}`))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`))
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close didn't go on once the turn ended")
	}
	assert.Equal(t, []string{"turn_started", "turn_completed:interrupted"}, sink.TurnLog())
}

// A CLI that doesn't end its turn is closed after the grace; without a
// turn, Close interrupts nothing.
func TestCloseWithoutTheTurnEnding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("the turn doesn't end", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, _ := started(t, Config{}, proc)
		a.closeGrace = 30 * time.Millisecond
		require.NoError(t, a.StartTurn(ctx, native.Input{Text: "hi"}))
		proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1"}`))
		proc.barrier(t)
		start := time.Now()
		require.NoError(t, a.Close())
		assert.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond)
		proc.waitRequest(t, 1, "interrupt")
	})
	t.Run("no turn", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, _ := started(t, Config{}, proc)
		a.closeGrace = 5 * time.Second
		start := time.Now()
		require.NoError(t, a.Close())
		assert.Less(t, time.Since(start), time.Second)
		for _, f := range proc.writtenFrames() {
			assert.NotEqual(t, "interrupt", subtypeOf(f))
		}
	})
}

// A control request the CLI got but didn't answer may take effect all the
// same: the error says so.
func TestUnansweredRequestIsUnconfirmed(t *testing.T) {
	t.Parallel()
	proc := newFakeProcess("")
	a, _ := started(t, Config{}, proc)
	require.NoError(t, a.StartTurn(context.Background(), native.Input{Text: "hi"}))
	proc.send(t, []byte(`{"type":"system","subtype":"session_state_changed","state":"running"}`))
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- a.Interrupt(ctx) }()
	proc.waitRequest(t, 1, "interrupt")
	cancel()
	err := <-errCh
	require.ErrorIs(t, err, native.ErrUnconfirmed)
	assert.ErrorIs(t, err, context.Canceled)
	require.NoError(t, a.Close())
}
