package screentracker_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	st "github.com/coder/agentapi/lib/screentracker"
)

const testTimeout = 10 * time.Second

// testAgent is a goroutine-safe mock implementation of AgentIO.
type testAgent struct {
	mu     sync.Mutex
	screen string
	// onWrite is called during Write to simulate the agent reacting to
	// terminal input (e.g., changing the screen), which unblocks
	// writeStabilize's polling loops.
	onWrite func(data []byte)
}

func (a *testAgent) ReadScreen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.screen
}

func (a *testAgent) Write(data []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onWrite != nil {
		a.onWrite(data)
	}
	return len(data), nil
}

func (a *testAgent) setScreen(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.screen = s
}

type testEmitter struct{}

func (testEmitter) EmitMessages([]st.ConversationMessage) {}
func (testEmitter) EmitStatus(st.ConversationStatus)      {}
func (testEmitter) EmitScreen(string)                     {}
func (testEmitter) EmitError(_ string, _ st.ErrorLevel)   {}

// advanceFor is a shorthand for advanceUntil with a time-based condition.
func advanceFor(ctx context.Context, t *testing.T, mClock *quartz.Mock, total time.Duration) {
	t.Helper()
	target := mClock.Now().Add(total)
	advanceUntil(ctx, t, mClock, func() bool { return !mClock.Now().Before(target) })
}

// advanceUntil advances the mock clock one event at a time until done returns
// true. Because the snapshot TickerFunc is always pending and WaitFor reuses a
// single timer via Reset, there is always at least one event to advance.
func advanceUntil(ctx context.Context, t *testing.T, mClock *quartz.Mock, done func() bool) {
	t.Helper()
	for !done() {
		select {
		case <-ctx.Done():
			t.Fatal("context cancelled waiting for condition")
		default:
		}
		_, w := mClock.AdvanceNext()
		w.MustWait(ctx)
	}
}

// sendAndAdvance calls Send() in a goroutine and advances the mock clock until
// Send completes.
func sendAndAdvance(ctx context.Context, t *testing.T, c *st.PTYConversation, mClock *quartz.Mock, parts ...st.MessagePart) {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Send(parts...)
	}()
	advanceUntil(ctx, t, mClock, func() bool {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	})
}

func assertMessages(t *testing.T, c *st.PTYConversation, expected []st.ConversationMessage) {
	t.Helper()
	actual := c.Messages()
	for i := range actual {
		require.False(t, actual[i].Time.IsZero(), "message %d Time should be non-zero", i)
		actual[i].Time = time.Time{}
	}
	require.Equal(t, expected, actual)
}

type statusTestStep struct {
	snapshot string
	status   st.ConversationStatus
}
type statusTestParams struct {
	cfg   st.PTYConversationConfig
	steps []statusTestStep
}

func statusTest(t *testing.T, params statusTestParams) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	t.Run(fmt.Sprintf("interval-%s,stability_length-%s", params.cfg.SnapshotInterval, params.cfg.ScreenStabilityLength), func(t *testing.T) {
		mClock := quartz.NewMock(t)
		params.cfg.Clock = mClock
		agent := &testAgent{}
		if params.cfg.AgentIO != nil {
			if a, ok := params.cfg.AgentIO.(*testAgent); ok {
				agent = a
			}
		}
		params.cfg.AgentIO = agent
		params.cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

		c := st.NewPTY(ctx, params.cfg, &testEmitter{})
		c.Start(ctx)

		assert.Equal(t, st.ConversationStatusInitializing, c.Status())

		for i, step := range params.steps {
			agent.setScreen(step.snapshot)
			advanceFor(ctx, t, mClock, params.cfg.SnapshotInterval)
			assert.Equal(t, step.status, c.Status(), "step %d", i)
		}
	})
}

func TestConversation(t *testing.T) {
	changing := st.ConversationStatusChanging
	stable := st.ConversationStatusStable
	initializing := st.ConversationStatusInitializing

	statusTest(t, statusTestParams{
		cfg: st.PTYConversationConfig{
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 2 * time.Second,
			// stability threshold: 3
			AgentIO: &testAgent{
				screen: "1",
			},
		},
		steps: []statusTestStep{
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: stable},
			{snapshot: "1", status: stable},
			{snapshot: "2", status: changing},
		},
	})

	statusTest(t, statusTestParams{
		cfg: st.PTYConversationConfig{
			SnapshotInterval:      2 * time.Second,
			ScreenStabilityLength: 3 * time.Second,
			// stability threshold: 3
		},
		steps: []statusTestStep{
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: stable},
			{snapshot: "1", status: stable},
			{snapshot: "2", status: changing},
			{snapshot: "2", status: changing},
			{snapshot: "2", status: stable},
			{snapshot: "2", status: stable},
			{snapshot: "2", status: stable},
		},
	})

	statusTest(t, statusTestParams{
		cfg: st.PTYConversationConfig{
			SnapshotInterval:      6 * time.Second,
			ScreenStabilityLength: 14 * time.Second,
			// stability threshold: 4
		},
		steps: []statusTestStep{
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: initializing},
			{snapshot: "1", status: stable},
			{snapshot: "1", status: stable},
			{snapshot: "1", status: stable},
			{snapshot: "2", status: changing},
			{snapshot: "2", status: changing},
			{snapshot: "2", status: changing},
			{snapshot: "2", status: stable},
		},
	})
}

func TestMessages(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// newConversation creates a started conversation with a mock clock and
	// testAgent. Tests that Send() messages must use sendAndAdvance.
	newConversation := func(ctx context.Context, t *testing.T, opts ...func(*st.PTYConversationConfig)) (*st.PTYConversation, *testAgent, *quartz.Mock) {
		t.Helper()

		writeCounter := 0
		agent := &testAgent{}
		// Default onWrite: each write produces a unique screen so that
		// writeStabilize can detect screen changes.
		agent.onWrite = func(data []byte) {
			writeCounter++
			agent.screen = fmt.Sprintf("__write_%d", writeCounter)
		}
		mClock := quartz.NewMock(t)
		mClock.Set(now)
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			AgentIO:               agent,
			SnapshotInterval:      100 * time.Millisecond,
			ScreenStabilityLength: 200 * time.Millisecond,
			Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		for _, opt := range opts {
			opt(&cfg)
		}
		if a, ok := cfg.AgentIO.(*testAgent); ok {
			agent = a
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		return c, agent, mClock
	}

	// threshold = 3 (200ms / 100ms = 2, + 1 = 3)
	const threshold = 3
	const interval = 100 * time.Millisecond

	t.Run("messages are copied", func(t *testing.T) {
		c, _, _ := newConversation(context.Background(), t)
		messages := c.Messages()
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "", Role: st.ConversationRoleAgent},
		})

		messages[0].Message = "modification"

		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "", Role: st.ConversationRoleAgent},
		})
	})

	t.Run("whitespace-padding", func(t *testing.T) {
		c, _, _ := newConversation(context.Background(), t)
		for _, msg := range []string{"123 ", " 123", "123\t\t", "\n123", "123\n\t", " \t123\n\t"} {
			err := c.Send(st.MessagePartText{Content: msg})
			assert.ErrorIs(t, err, st.ErrMessageValidationWhitespace)
		}
	})

	t.Run("no-change-no-message-update", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		c, agent, mClock := newConversation(ctx, t)

		agent.setScreen("1")
		advanceFor(ctx, t, mClock, interval)
		msgs := c.Messages()
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "1", Role: st.ConversationRoleAgent},
		})

		advanceFor(ctx, t, mClock, interval)
		assert.Equal(t, msgs, c.Messages())
	})

	t.Run("tracking messages", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		c, agent, mClock := newConversation(ctx, t)

		// Agent message is recorded when the first snapshot is taken.
		agent.setScreen("1")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "1", Role: st.ConversationRoleAgent},
		})

		// Agent message is updated when the screen changes.
		agent.setScreen("2")
		advanceFor(ctx, t, mClock, interval)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "2", Role: st.ConversationRoleAgent},
		})

		// Fill to stable so Send can proceed (screen is "2").
		agent.setScreen("2")
		advanceFor(ctx, t, mClock, interval*threshold)

		// User message is recorded.
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "3"})

		// After send, screen is dirty from writeStabilize. Set to "4" and stabilize.
		agent.setScreen("4")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "2", Role: st.ConversationRoleAgent},
			{Id: 1, Message: "3", Role: st.ConversationRoleUser},
			{Id: 2, Message: "4", Role: st.ConversationRoleAgent},
		})

		// Agent message is updated when the screen changes before a user message.
		agent.setScreen("5")
		advanceFor(ctx, t, mClock, interval*threshold)
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "6"})

		agent.setScreen("7")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "2", Role: st.ConversationRoleAgent},
			{Id: 1, Message: "3", Role: st.ConversationRoleUser},
			{Id: 2, Message: "5", Role: st.ConversationRoleAgent},
			{Id: 3, Message: "6", Role: st.ConversationRoleUser},
			{Id: 4, Message: "7", Role: st.ConversationRoleAgent},
		})
		assert.Equal(t, st.ConversationStatusStable, c.Status())

		// Send another message.
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "8"})

		// After filling to stable, messages and status are correct.
		agent.setScreen("7")
		advanceFor(ctx, t, mClock, interval*threshold)
		assert.Equal(t, st.ConversationStatusStable, c.Status())
	})

	t.Run("tracking messages overlap", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		c, agent, mClock := newConversation(ctx, t)

		// Common overlap between screens is removed after a user message.
		agent.setScreen("1")
		advanceFor(ctx, t, mClock, interval*threshold)
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "2"})
		agent.setScreen("1\n3")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "1", Role: st.ConversationRoleAgent},
			{Id: 1, Message: "2", Role: st.ConversationRoleUser},
			{Id: 2, Message: "3", Role: st.ConversationRoleAgent},
		})

		agent.setScreen("1\n3x")
		advanceFor(ctx, t, mClock, interval*threshold)
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "4"})
		agent.setScreen("1\n3x\n5")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "1", Role: st.ConversationRoleAgent},
			{Id: 1, Message: "2", Role: st.ConversationRoleUser},
			{Id: 2, Message: "3x", Role: st.ConversationRoleAgent},
			{Id: 3, Message: "4", Role: st.ConversationRoleUser},
			{Id: 4, Message: "5", Role: st.ConversationRoleAgent},
		})
	})

	t.Run("format-message", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		c, agent, mClock := newConversation(ctx, t, func(cfg *st.PTYConversationConfig) {
			cfg.FormatMessage = func(message string, userInput string) string {
				return message + " " + userInput
			}
		})

		// Fill to stable with screen "1", then send.
		agent.setScreen("1")
		advanceFor(ctx, t, mClock, interval*threshold)
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "2"})

		// After send, set screen to "x" and take snapshots for new agent message.
		agent.setScreen("x")
		advanceFor(ctx, t, mClock, interval*threshold)
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "1 ", Role: st.ConversationRoleAgent},
			{Id: 1, Message: "2", Role: st.ConversationRoleUser},
			{Id: 2, Message: "x 2", Role: st.ConversationRoleAgent},
		})
	})

	t.Run("format-message-initial", func(t *testing.T) {
		c, _, _ := newConversation(context.Background(), t, func(cfg *st.PTYConversationConfig) {
			cfg.FormatMessage = func(message string, userInput string) string {
				return "formatted"
			}
		})
		assertMessages(t, c, []st.ConversationMessage{
			{Id: 0, Message: "", Role: st.ConversationRoleAgent},
		})
	})

	t.Run("send-message-status-check", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		c, agent, mClock := newConversation(ctx, t)

		sendMsg := func(msg string) error {
			return c.Send(st.MessagePartText{Content: msg})
		}

		// Status is initializing, send should fail.
		assert.ErrorIs(t, sendMsg("1"), st.ErrMessageValidationChanging)

		// Fill to stable.
		agent.setScreen("1")
		advanceFor(ctx, t, mClock, interval*threshold)
		assert.Equal(t, st.ConversationStatusStable, c.Status())

		// Now send should succeed.
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "4"})

		// After send, screen is dirty. Set to "2" (different from "1") so status is changing.
		agent.setScreen("2")
		advanceFor(ctx, t, mClock, interval)
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
		assert.ErrorIs(t, sendMsg("5"), st.ErrMessageValidationChanging)
	})

	t.Run("send-message-empty-message", func(t *testing.T) {
		c, _, _ := newConversation(context.Background(), t)
		assert.ErrorIs(t, c.Send(st.MessagePartText{Content: ""}), st.ErrMessageValidationEmpty)
	})

	t.Run("send-message-no-echo-agent-reacts", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)

		// Given: an agent that doesn't echo typed input but
		// reacts to carriage return by updating the screen.
		c, _, mClock := newConversation(ctx, t, func(cfg *st.PTYConversationConfig) {
			a := &testAgent{screen: "prompt"}
			a.onWrite = func(data []byte) {
				if string(data) == "\r" {
					a.screen = "processing..."
				}
			}
			cfg.AgentIO = a
		})
		advanceFor(ctx, t, mClock, interval*threshold)

		// When: a message is sent. Phase 1 times out (no echo),
		// Phase 2 writes \r and the agent reacts.
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "hello"})

		// Then: Send succeeds and the user message is recorded.
		msgs := c.Messages()
		require.True(t, len(msgs) >= 2)
		assert.True(t, slices.ContainsFunc(msgs, func(m st.ConversationMessage) bool {
			return m.Role == st.ConversationRoleUser && m.Message == "hello"
		}), "expected user message 'hello' in conversation")
	})

	t.Run("send-message-no-echo-no-react", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)

		// Given: an agent that is completely unresponsive. It
		// neither echoes input nor reacts to carriage return.
		c, _, mClock := newConversation(ctx, t, func(cfg *st.PTYConversationConfig) {
			a := &testAgent{screen: "prompt"}
			a.onWrite = func(data []byte) {}
			cfg.AgentIO = a
		})
		advanceFor(ctx, t, mClock, interval*threshold)

		// When: a message is sent. Both Phase 1 (echo) and
		// Phase 2 (processing) time out.
		// Note: can't use sendAndAdvance here because it calls
		// require.NoError internally.
		var sendErr error
		var sendDone atomic.Bool
		go func() {
			sendErr = c.Send(st.MessagePartText{Content: "hello"})
			sendDone.Store(true)
		}()
		advanceUntil(ctx, t, mClock, func() bool { return sendDone.Load() })

		// Then: Send fails with a Phase 2 error (not Phase 1).
		require.Error(t, sendErr)
		assert.Contains(t, sendErr.Error(), "failed to wait for processing to start")
		assert.ErrorIs(t, sendErr, st.ErrMessageNotSubmitted)
	})

	t.Run("send-message-no-echo-context-cancelled", func(t *testing.T) {
		// Given: a non-echoing agent and a cancellable context.
		// The onWrite signals when writeStabilize starts writing
		// message parts. This is used to synchronize the cancel.
		sendCtx, sendCancel := context.WithCancel(context.Background())
		t.Cleanup(sendCancel)

		writeStarted := make(chan struct{}, 1)
		c, _, mClock := newConversation(sendCtx, t, func(cfg *st.PTYConversationConfig) {
			a := &testAgent{screen: "prompt"}
			a.onWrite = func(data []byte) {
				select {
				case writeStarted <- struct{}{}:
				default:
				}
			}
			cfg.AgentIO = a
		})
		advanceFor(sendCtx, t, mClock, interval*threshold)

		// When: a message is sent and the context is cancelled
		// during Phase 1 (after the message is written to the
		// PTY, before echo detection completes).
		var sendErr error
		var sendDone atomic.Bool
		go func() {
			sendErr = c.Send(st.MessagePartText{Content: "hello"})
			sendDone.Store(true)
		}()

		// Advance tick-by-tick until writeStabilize starts
		// (onWrite fires). This gives the send loop goroutine
		// scheduling time between advances.
		advanceUntil(sendCtx, t, mClock, func() bool {
			select {
			case <-writeStarted:
				return true
			default:
				return false
			}
		})

		// writeStabilize Phase 1 is now running. Its WaitFor is
		// blocked on a mock timer sleep select. Cancel: the
		// select sees ctx.Done() immediately.
		sendCancel()

		// WaitFor returns ctx.Err(). The errors.Is guard in
		// Phase 1 propagates it as fatal. Use Eventually since
		// the goroutine needs scheduling time.
		require.Eventually(t, sendDone.Load, 5*time.Second, 10*time.Millisecond)

		// Then: the error wraps context.Canceled, not a Phase 2
		// timeout. This validates the errors.Is(WaitTimedOut)
		// guard.
		require.Error(t, sendErr)
		assert.ErrorIs(t, sendErr, context.Canceled)
		assert.NotContains(t, sendErr.Error(), "failed to wait for processing to start")
	})
}

// A conversation restored from the state database continues: the agent
// starts anew below the restored messages.
func TestRestoredConversation(t *testing.T) {
	restoredConfig := func(mClock *quartz.Mock, agent *testAgent, messages []st.ConversationMessage) st.PTYConversationConfig {
		return st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      100 * time.Millisecond,
			ScreenStabilityLength: 200 * time.Millisecond,
			AgentIO:               agent,
			Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
			FormatMessage: func(message string, userInput string) string {
				return message
			},
			ReadyForInitialPrompt: func(message string) bool {
				return message == "ready"
			},
			InitialMessages: messages,
		}
	}

	t.Run("keeps the last agent message until the user sends", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "ready"}
		c := st.NewPTY(ctx, restoredConfig(mClock, agent, []st.ConversationMessage{
			{Id: 0, Message: "agent message 1", Role: st.ConversationRoleAgent, Time: time.Now()},
			{Id: 1, Message: "user message 1", Role: st.ConversationRoleUser, Time: time.Now()},
			{Id: 2, Message: "agent message 2", Role: st.ConversationRoleAgent, Time: time.Now()},
		}), &testEmitter{})
		// Right away: the emitter, restored from the same database, must
		// not see the messages go.
		require.Len(t, c.Messages(), 3)
		c.Start(ctx)
		advanceFor(ctx, t, mClock, 300*time.Millisecond)

		messages := c.Messages()
		require.Len(t, messages, 3)
		assert.Equal(t, "agent message 1", messages[0].Message)
		assert.Equal(t, "user message 1", messages[1].Message)
		assert.Equal(t, "agent message 2", messages[2].Message, "the new agent's screen doesn't replace it")
	})

	t.Run("a restored user message gets the new agent's reply", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "ready"}
		// AgentAPI stopped before the agent answered.
		c := st.NewPTY(ctx, restoredConfig(mClock, agent, []st.ConversationMessage{
			{Id: 0, Message: "agent greeting", Role: st.ConversationRoleAgent, Time: time.Now()},
			{Id: 1, Message: "user question", Role: st.ConversationRoleUser, Time: time.Now()},
		}), &testEmitter{})
		c.Start(ctx)
		advanceFor(ctx, t, mClock, 300*time.Millisecond)

		messages := c.Messages()
		require.Len(t, messages, 3)
		last := messages[2]
		assert.Equal(t, st.ConversationRoleAgent, last.Role)
		assert.Equal(t, 2, last.Id)
		assert.Contains(t, last.Message, "ready", "from the screen, not the user's message")
	})

	t.Run("an initial prompt goes out after the restored messages", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "ready"}
		writes := 0
		agent.onWrite = func([]byte) {
			writes++
			agent.screen = fmt.Sprintf("__write_%d", writes)
		}
		cfg := restoredConfig(mClock, agent, []st.ConversationMessage{
			{Id: 0, Message: "agent greeting", Role: st.ConversationRoleAgent, Time: time.Now()},
		})
		cfg.InitialPrompt = []st.MessagePart{st.MessagePartText{Content: "next prompt"}}
		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)
		advanceUntil(ctx, t, mClock, func() bool { return len(c.Messages()) >= 2 })

		messages := c.Messages()
		assert.Equal(t, "agent greeting", messages[0].Message)
		assert.Equal(t, st.ConversationMessage{Id: 1, Message: "next prompt", Role: st.ConversationRoleUser, Time: messages[1].Time}, messages[1])
	})
}

func TestInitialPromptReadiness(t *testing.T) {
	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("agent not ready - status is changing until agent becomes ready", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "loading..."}
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 0,
			AgentIO:               agent,
			ReadyForInitialPrompt: func(message string) bool {
				return message == "ready"
			},
			InitialPrompt: []st.MessagePart{st.MessagePartText{Content: "initial prompt here"}},
			Logger:        discardLogger,
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		// Take a snapshot with "loading...". Threshold is 1 (stability 0 / interval 1s = 0 + 1 = 1).
		advanceFor(ctx, t, mClock, 1*time.Second)

		// Screen is stable but agent is not ready. Status must be
		// "changing" so that Send() rejects instead of blocking.
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
	})

	t.Run("agent becomes ready - prompt enqueued and status changes to changing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "loading..."}
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 0,
			AgentIO:               agent,
			ReadyForInitialPrompt: func(message string) bool {
				return message == "ready"
			},
			InitialPrompt: []st.MessagePart{st.MessagePartText{Content: "initial prompt here"}},
			Logger:        discardLogger,
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		// Agent not ready initially, status should be changing.
		advanceFor(ctx, t, mClock, 1*time.Second)
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
		// Agent becomes ready, prompt gets enqueued, status becomes "changing"
		agent.setScreen("ready")
		advanceFor(ctx, t, mClock, 1*time.Second)
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
	})

	t.Run("initial prompt lifecycle - status stays changing until sent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "loading..."}
		writeCounter := 0
		agent.onWrite = func(data []byte) {
			writeCounter++
			agent.screen = fmt.Sprintf("__write_%d", writeCounter)
		}
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 0,
			AgentIO:               agent,
			ReadyForInitialPrompt: func(message string) bool {
				return message == "ready"
			},
			InitialPrompt: []st.MessagePart{st.MessagePartText{Content: "initial prompt here"}},
			Logger:        discardLogger,
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		// Status is "changing" while waiting for readiness (prompt not yet enqueued).
		advanceFor(ctx, t, mClock, 1*time.Second)
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
		// Agent becomes ready. The snapshot loop detects this, enqueues the prompt,
		// then sees queue + stable + ready and signals the send loop.
		// writeStabilize runs with onWrite changing the screen, so it completes.
		agent.setScreen("ready")
		// Drive clock until the initial prompt is sent (queue drains).
		advanceUntil(ctx, t, mClock, func() bool {
			return len(c.Messages()) >= 2
		})

		// The initial prompt should have been sent. Set a clean screen and
		// advance enough ticks for the snapshot loop to record it as an
		// agent message and fill the stability buffer (threshold=1).
		agent.setScreen("response")
		advanceFor(ctx, t, mClock, 2*time.Second)
		assert.Equal(t, st.ConversationStatusStable, c.Status())
	})

	t.Run("ReadyForInitialPrompt always false - status is changing", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "loading..."}
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 0,
			AgentIO:               agent,
			ReadyForInitialPrompt: func(message string) bool {
				return false
			},
			Logger: discardLogger,
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		advanceFor(ctx, t, mClock, 1*time.Second)

		// Even without an initial prompt, stableSignal gates on
		// initialPromptReady. Status must reflect that Send()
		// would block.
		assert.Equal(t, st.ConversationStatusChanging, c.Status())
	})

	t.Run("no initial prompt configured - normal status logic applies", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		mClock := quartz.NewMock(t)
		agent := &testAgent{screen: "ready"}
		cfg := st.PTYConversationConfig{
			Clock:                 mClock,
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 2 * time.Second, // threshold = 3
			AgentIO:               agent,
			Logger:                discardLogger,
		}

		c := st.NewPTY(ctx, cfg, &testEmitter{})
		c.Start(ctx)

		// Fill buffer to reach stability with "ready" screen.
		agent.setScreen("ready")
		advanceFor(ctx, t, mClock, 3*time.Second)
		assert.Equal(t, st.ConversationStatusStable, c.Status())

		// After screen changes, status becomes changing.
		agent.setScreen("processing...")
		advanceFor(ctx, t, mClock, 1*time.Second)
		assert.Equal(t, st.ConversationStatusChanging, c.Status())

		// After screen is stable again (3 identical snapshots), status becomes stable.
		advanceFor(ctx, t, mClock, 1*time.Second)
		advanceFor(ctx, t, mClock, 1*time.Second)
		assert.Equal(t, st.ConversationStatusStable, c.Status())
	})
}

func TestSendRejectsWhenInitialPromptNotReady(t *testing.T) {
	// Regression test for https://github.com/coder/agentapi/issues/209.
	// Send() used to block forever when ReadyForInitialPrompt never
	// returned true, because statusLocked() reported "stable" while
	// stableSignal required initialPromptReady. Now statusLocked()
	// returns "changing" and Send() rejects immediately.
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	mClock := quartz.NewMock(t)
	agent := &testAgent{screen: "onboarding screen without message box"}
	cfg := st.PTYConversationConfig{
		Clock:                 mClock,
		SnapshotInterval:      100 * time.Millisecond,
		ScreenStabilityLength: 200 * time.Millisecond,
		AgentIO:               agent,
		ReadyForInitialPrompt: func(message string) bool {
			return false // Simulates failed message box detection.
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	c := st.NewPTY(ctx, cfg, &testEmitter{})
	c.Start(ctx)

	// Fill snapshot buffer to reach stability.
	advanceFor(ctx, t, mClock, 300*time.Millisecond)

	// Status reports "changing" because initialPromptReady is false.
	assert.Equal(t, st.ConversationStatusChanging, c.Status())

	// Send() rejects immediately instead of blocking forever.
	err := c.Send(st.MessagePartText{Content: "hello"})
	assert.ErrorIs(t, err, st.ErrMessageValidationChanging)
}

// tickingAgent simulates a TUI that keeps redrawing a footer below its input
// box (e.g. Claude Code's background agents panel with running timers).
// Everything after "|" changes on every read.
type tickingAgent struct {
	mu      sync.Mutex
	prompt  string
	ticks   int
	onWrite func(a *tickingAgent, data []byte)
}

func (a *tickingAgent) ReadScreen() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ticks++
	return fmt.Sprintf("%s|%ds", a.prompt, a.ticks)
}

func (a *tickingAgent) Write(data []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onWrite != nil {
		a.onWrite(a, data)
	}
	return len(data), nil
}

func stripFooter(screen string) string {
	if i := strings.Index(screen, "|"); i != -1 {
		return screen[:i]
	}
	return screen
}

func TestStabilityScreen(t *testing.T) {
	changing := st.ConversationStatusChanging
	stable := st.ConversationStatusStable
	initializing := st.ConversationStatusInitializing

	statusTest(t, statusTestParams{
		cfg: st.PTYConversationConfig{
			SnapshotInterval:      1 * time.Second,
			ScreenStabilityLength: 2 * time.Second,
			StabilityScreen:       stripFooter,
		},
		steps: []statusTestStep{
			{snapshot: "idle|1s", status: initializing},
			{snapshot: "idle|2s", status: initializing},
			{snapshot: "idle|3s", status: stable},
			{snapshot: "idle|4s", status: stable},
			{snapshot: "busy|5s", status: changing},
		},
	})

	newTickingConversation := func(ctx context.Context, t *testing.T, agent *tickingAgent) (*st.PTYConversation, *quartz.Mock) {
		t.Helper()
		mClock := quartz.NewMock(t)
		c := st.NewPTY(ctx, st.PTYConversationConfig{
			Clock:                 mClock,
			AgentIO:               agent,
			SnapshotInterval:      100 * time.Millisecond,
			ScreenStabilityLength: 200 * time.Millisecond,
			StabilityScreen:       stripFooter,
			Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		}, &testEmitter{})
		c.Start(ctx)
		advanceUntil(ctx, t, mClock, func() bool { return c.Status() == stable })
		return c, mClock
	}

	t.Run("send succeeds when agent reacts despite ticking footer", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		agent := &tickingAgent{prompt: "> ", onWrite: func(a *tickingAgent, data []byte) {
			if string(data) == "\r" {
				a.prompt = "working..."
			} else {
				a.prompt += string(data)
			}
		}}
		c, mClock := newTickingConversation(ctx, t, agent)
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "hello"})
	})

	t.Run("ticking footer is not mistaken for the agent reacting", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		t.Cleanup(cancel)
		// The agent ignores all input; only the footer keeps changing.
		c, mClock := newTickingConversation(ctx, t, &tickingAgent{prompt: "> "})
		var sendErr error
		var sendDone atomic.Bool
		go func() {
			sendErr = c.Send(st.MessagePartText{Content: "hello"})
			sendDone.Store(true)
		}()
		advanceUntil(ctx, t, mClock, func() bool { return sendDone.Load() })
		assert.ErrorIs(t, sendErr, st.ErrMessageNotSubmitted)
	})
}

// roles lists each message as "role:text", for asserting the conversation's
// shape.
func roles(c *st.PTYConversation) []string {
	var out []string
	for _, m := range c.Messages() {
		out = append(out, string(m.Role)+":"+m.Message)
	}
	return out
}

func TestRepeatedRepliesEndTheirTurns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	// Every write drops the last line: the screen changes, so the message is
	// taken as submitted, but it only ever shows lines already on screen, so
	// every reply is empty.
	agent := &testAgent{screen: "L1\nL2\nL3\nL4\nL5\nL6\nL7\nL8"}
	agent.onWrite = func([]byte) {
		if lines := strings.Split(agent.screen, "\n"); len(lines) > 1 {
			agent.screen = strings.Join(lines[:len(lines)-1], "\n")
		}
	}
	mClock := quartz.NewMock(t)
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		Clock:                 mClock,
		AgentIO:               agent,
		SnapshotInterval:      100 * time.Millisecond,
		ScreenStabilityLength: 200 * time.Millisecond,
		Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &testEmitter{})
	c.Start(ctx)
	advanceFor(ctx, t, mClock, 300*time.Millisecond)
	require.Equal(t, st.ConversationStatusStable, c.Status())

	// The second reply is the same as the first one (empty): it still ends
	// its turn, rather than leaving the user's message last and the status
	// changing for good.
	for _, q := range []string{"q1", "q2"} {
		sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: q})
		advanceFor(ctx, t, mClock, time.Second)
		require.Equal(t, st.ConversationStatusStable, c.Status(), "after %s: %q", q, roles(c))
	}
	require.Equal(t, []string{"agent:" + strings.Join(strings.Split("L1 L2 L3 L4 L5 L6 L7 L8", " "), "\n"), "user:q1", "agent:", "user:q2", "agent:"}, roles(c))
}

func TestResetWhileTheInitialPromptWaits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	agent := &testAgent{screen: "ready-0"}
	mClock := quartz.NewMock(t)
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		Clock:                 mClock,
		AgentIO:               agent,
		SnapshotInterval:      100 * time.Millisecond,
		ScreenStabilityLength: 200 * time.Millisecond,
		ReadyForInitialPrompt: func(s string) bool { return strings.HasPrefix(s, "ready") },
		InitialPrompt:         []st.MessagePart{st.MessagePartText{Content: "initial"}},
		Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &testEmitter{})
	c.Start(ctx)

	// The agent is ready, so the initial prompt waits for a stable screen.
	_, w := mClock.AdvanceNext()
	w.MustWait(ctx)
	// A restart now (DELETE /messages) drops it; the next snapshot queues
	// it again for the new agent, without blocking.
	c.Reset()
	agent.setScreen("ready-1")
	_, w = mClock.AdvanceNext()
	tickCtx, tickCancel := context.WithTimeout(ctx, 2*time.Second)
	defer tickCancel()
	require.NoError(t, w.Wait(tickCtx), "the snapshot blocked")

	status := make(chan st.ConversationStatus, 1)
	go func() { status <- c.Status() }()
	select {
	case <-status:
	case <-time.After(2 * time.Second):
		t.Fatal("Status blocked: a snapshot holds the lock")
	}

	// The initial prompt goes to the new agent once, then messages work.
	var written []string
	agent.mu.Lock()
	agent.onWrite = func(data []byte) {
		if s := string(data); s != "\r" {
			written = append(written, s)
		}
		agent.screen += "."
	}
	agent.mu.Unlock()
	writes := func() []string {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return slices.Clone(written)
	}
	// The send loop delivers the prompt in its own time: wait for it, and
	// for the screen to settle after it.
	advanceUntil(ctx, t, mClock, func() bool {
		return len(writes()) == 1 && c.Status() == st.ConversationStatusStable
	})
	sendAndAdvance(ctx, t, c, mClock, st.MessagePartText{Content: "hello"})
	require.Equal(t, []string{"initial", "hello"}, writes())
}

// errorEmitter records the errors a conversation reports.
type errorEmitter struct {
	testEmitter
	mu     sync.Mutex
	errors []string
}

func (e *errorEmitter) EmitError(message string, _ st.ErrorLevel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errors = append(e.errors, message)
}

func (e *errorEmitter) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.errors...)
}

func TestFailedInitialPromptIsReported(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)

	// The agent never reacts to the carriage return.
	agent := &testAgent{screen: "ready"}
	emitter := &errorEmitter{}
	mClock := quartz.NewMock(t)
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		Clock:                 mClock,
		AgentIO:               agent,
		SnapshotInterval:      100 * time.Millisecond,
		ScreenStabilityLength: 200 * time.Millisecond,
		InitialPrompt:         []st.MessagePart{st.MessagePartText{Content: "initial"}},
		Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, emitter)
	c.Start(ctx)
	advanceUntil(ctx, t, mClock, func() bool { return len(emitter.get()) > 0 })
	require.Contains(t, emitter.get()[0], "Failed to send the initial prompt")
}

func TestSendAfterStopFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mClock := quartz.NewMock(t)
	c := st.NewPTY(ctx, st.PTYConversationConfig{
		Clock:                 mClock,
		AgentIO:               &testAgent{screen: "ready"},
		SnapshotInterval:      100 * time.Millisecond,
		ScreenStabilityLength: 200 * time.Millisecond,
		Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &testEmitter{})
	c.Start(ctx)
	advanceFor(ctx, t, mClock, 300*time.Millisecond)
	require.Equal(t, st.ConversationStatusStable, c.Status())
	cancel()
	require.Eventually(t, func() bool {
		return errors.Is(c.Send(st.MessagePartText{Content: "late"}), context.Canceled)
	}, testTimeout, 10*time.Millisecond, "Send blocks or succeeds after the conversation stopped")
}
