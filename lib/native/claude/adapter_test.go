package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// started returns an adapter on a fake process that completed the
// handshake.
func started(t *testing.T, cfg Config, proc *fakeProcess) (*Adapter, *recSink) {
	t.Helper()
	cfg.StartProcess = func(Command) (Process, error) { return proc, nil }
	if cfg.AuthStatus == nil {
		cfg.AuthStatus = loggedIn
	}
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Env = []string{}
	a, err := New(cfg, "")
	require.NoError(t, err)
	sink := &recSink{t: t}
	errCh := make(chan error, 1)
	go func() { errCh <- a.Start(context.Background(), sink) }()
	init := proc.waitWritten(t, 0)
	require.Equal(t, "control_request:initialize", signature(init))
	proc.send(t, []byte(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"claude_code_version":"2.1.299"}}}`, init["request_id"])))
	require.NoError(t, <-errCh)
	return a, sink
}

func TestStartFailure(t *testing.T) {
	t.Parallel()

	t.Run("exit during startup", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("No conversation found with session ID: 6943de8c\n")
		a, err := New(Config{
			StartProcess: func(Command) (Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, "6943de8c")
		require.NoError(t, err)
		go func() {
			proc.waitWritten(t, 0)
			proc.exit(errExit1)
		}()
		err = a.Start(context.Background(), &recSink{t: t})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No conversation found with session ID: 6943de8c")
		assert.ErrorIs(t, a.Wait(), errExit1)
	})

	t.Run("clean exit during startup", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("No conversation found with session ID: 6943de8c\n")
		a, err := New(Config{
			StartProcess: func(Command) (Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, "6943de8c")
		require.NoError(t, err)
		go func() {
			proc.waitWritten(t, 0)
			proc.send(t, []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":0}`))
			proc.exit(nil)
		}()
		err = a.Start(context.Background(), &recSink{t: t})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No conversation found with session ID: 6943de8c")
	})

	t.Run("no answer", func(t *testing.T) {
		t.Parallel()
		proc := newFakeProcess("")
		a, err := New(Config{
			StartProcess: func(Command) (Process, error) { return proc, nil },
			AuthStatus:   loggedIn,
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			StopTimeout:  10 * time.Millisecond,
		}, "")
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		require.Error(t, a.Start(ctx, &recSink{t: t}))
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
}

func TestTurnErrors(t *testing.T) {
	t.Parallel()
	a, err := New(Config{}, "")
	require.NoError(t, err)
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
	require.Len(t, sink.results, 1)
	assert.Equal(t, "error", sink.results[0].StopReason)
	assert.Equal(t, "Claude Code: the turn failed (error_during_execution), API status 529: Overloaded", sink.results[0].Error)

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

func TestSnapshotsShareNothing(t *testing.T) {
	t.Parallel()
	p := newStreamParser(quartz.NewMock(t), time.Millisecond)
	msgs, _ := p.user(&frame{Type: "user", UUID: "u1", Message: json.RawMessage(
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}`)})
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].Content[0].Images, 1)
	// The server replaces image data with URLs in the message it got.
	msgs[0].Content[0].Images[0].Data = nil
	msgs[0].Content[0].Images = nil
	again := p.setResultStatus("t1", "running")
	require.Len(t, again, 1)
	require.Len(t, again[0].Content[0].Images, 1)
	assert.NotEmpty(t, again[0].Content[0].Images[0].Data)
	assert.True(t, strings.HasPrefix(string(again[0].Content[0].Images[0].Data), "\x89PNG"))
}
