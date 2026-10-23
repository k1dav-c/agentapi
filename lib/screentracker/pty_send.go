package screentracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/util"
)

const (
	// writeStabilizeEchoTimeout is the timeout for the echo
	// detection WaitFor loop in writeStabilize Phase 1. The
	// effective ceiling may be slightly longer because the
	// stability check inside the condition runs outside
	// WaitFor's timeout select. Non-echoing agents (e.g. TUI
	// agents using bracketed paste) will hit this timeout,
	// which is non-fatal.
	//
	// TODO: move to PTYConversationConfig if agents need
	// different echo detection windows.
	writeStabilizeEchoTimeout = 2 * time.Second

	// writeStabilizeProcessTimeout is the maximum time to wait
	// for the screen to change after sending a carriage return.
	// This detects whether the agent is actually processing the
	// input.
	writeStabilizeProcessTimeout = 15 * time.Second
)

// outboundMessage is a message waiting to be sent to the agent.
type outboundMessage struct {
	parts []MessagePart
	// errCh receives the outcome for the Send waiting on it; nil for the
	// initial prompt.
	errCh chan error
	// initial marks the initial prompt, whose failure is reported here.
	initial bool
	// gen is the reset generation it was queued in.
	gen uint64
}

// finish reports the outcome to the Send waiting for it, if any.
func (m *outboundMessage) finish(err error) {
	if m != nil && m.errCh != nil {
		m.errCh <- err
		close(m.errCh)
	}
}

// sendLoop sends the pending message whenever the snapshot loop signals
// that the agent is ready for it.
func (c *PTYConversation) sendLoop(ctx context.Context) {
	defer func() {
		// Fail the message that waits, and the ones Send queues from now
		// on, so no caller blocks forever.
		c.lock.Lock()
		c.sendErr = ctx.Err()
		msg := c.pending
		c.pending = nil
		c.lock.Unlock()
		msg.finish(ctx.Err())
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stableSignal:
		}
		c.lock.Lock()
		msg := c.pending
		c.pending = nil
		var sendCtx context.Context
		if msg != nil {
			sendCtx, c.cancelSend = context.WithCancel(ctx)
		}
		c.lock.Unlock()
		var err error
		if msg != nil {
			err = c.sendMessage(sendCtx, msg.gen, msg.parts...)
		}
		c.lock.Lock()
		c.sendingMessage = false
		if c.cancelSend != nil {
			c.cancelSend()
			c.cancelSend = nil
		}
		c.lock.Unlock()
		c.wakeSnapshot()
		if msg == nil {
			// Reset dropped it after the signal.
			continue
		}
		if msg.initial && err != nil && !errors.Is(err, ErrConversationReset) {
			c.cfg.Logger.Error("Failed to send the initial prompt", "error", err)
			c.emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), ErrorLevelError)
		}
		msg.finish(err)
	}
}

func (c *PTYConversation) Send(messageParts ...MessagePart) error {
	// Validate message content before enqueueing
	message := buildStringFromMessageParts(messageParts)
	if message != msgfmt.TrimWhitespace(message) {
		return ErrMessageValidationWhitespace
	}
	if message == "" {
		return ErrMessageValidationEmpty
	}

	// Checking the status and queueing the message happen under one lock,
	// so no second message can slip in.
	c.lock.Lock()
	if c.sendErr != nil {
		err := c.sendErr
		c.lock.Unlock()
		return err
	}
	if c.statusLocked() != ConversationStatusStable {
		c.lock.Unlock()
		return ErrMessageValidationChanging
	}
	errCh := make(chan error, 1)
	c.pending = &outboundMessage{parts: messageParts, errCh: errCh, gen: c.sendGen}
	c.lock.Unlock()
	c.wakeSnapshot()
	return <-errCh
}

// sendMessage sends a message, queued in reset generation gen, to the
// agent. It acquires and releases c.lock around the parts that access
// shared state, but releases it during writeStabilize to avoid blocking the
// snapshot loop.
func (c *PTYConversation) sendMessage(ctx context.Context, gen uint64, messageParts ...MessagePart) error {
	message := buildStringFromMessageParts(messageParts)

	c.lock.Lock()
	if gen != c.sendGen {
		c.lock.Unlock()
		return ErrConversationReset
	}
	screenBeforeMessage := c.cfg.AgentIO.ReadScreen()
	now := c.cfg.Clock.Now()
	c.updateLastAgentMessageLocked(screenBeforeMessage, now)
	c.writingMessage = true
	c.lock.Unlock()

	err := c.writeStabilize(ctx, messageParts...)
	c.lock.Lock()
	defer c.lock.Unlock()
	c.writingMessage = false
	if gen != c.sendGen {
		// Reset while it was on its way: the message belongs to the
		// conversation that was cleared.
		return ErrConversationReset
	}
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	c.screenBeforeLastUserMessage = screenBeforeMessage
	c.messages = append(c.messages, ConversationMessage{
		Id:      len(c.messages),
		Message: message,
		Role:    ConversationRoleUser,
		Time:    now,
	})
	c.restored = false
	// screenBeforeLastUserMessage and messages changed; the next
	// updateLastAgentMessageLocked pass must recompute the diff.
	c.fmtCacheValid = false
	return nil
}

// writeStabilize writes messageParts to the PTY and waits for
// the agent to process them. It operates in two phases:
//
// Phase 1 (echo detection): writes the message text and waits
// for the screen to change and stabilize. This detects agents
// that echo typed input. If the screen doesn't change within
// writeStabilizeEchoTimeout, this is non-fatal. Many TUI
// agents buffer bracketed-paste input without rendering it.
//
// Phase 2 (processing detection): writes a carriage return
// and waits for the screen to change, indicating the agent
// started processing. This phase is fatal on timeout: if the
// agent doesn't react to Enter, it's unresponsive.
func (c *PTYConversation) writeStabilize(ctx context.Context, messageParts ...MessagePart) error {
	// Compare stability regions rather than whole screens so output that
	// changes on its own (e.g. timers below the input box) is not mistaken
	// for the agent echoing input or reacting to the carriage return.
	readRegion := func() string {
		return c.cfg.StabilityScreen(c.cfg.AgentIO.ReadScreen())
	}
	screenBeforeMessage := readRegion()
	for _, part := range messageParts {
		if err := part.Do(c.cfg.AgentIO); err != nil {
			return fmt.Errorf("failed to write message part: %w", err)
		}
	}
	// Phase 1: wait for the screen to stabilize after the
	// message is written (echo detection).
	if err := util.WaitFor(ctx, util.WaitTimeout{
		Timeout:     writeStabilizeEchoTimeout,
		MinInterval: 50 * time.Millisecond,
		InitialWait: true,
		Clock:       c.cfg.Clock,
	}, func() (bool, error) {
		screen := readRegion()
		if screen != screenBeforeMessage {
			stabilityTimer := c.cfg.Clock.NewTimer(1 * time.Second)
			select {
			case <-ctx.Done():
				stabilityTimer.Stop()
				return false, ctx.Err()
			case <-stabilityTimer.C:
			}
			stabilityTimer.Stop()
			newScreen := readRegion()
			return newScreen == screen, nil
		}
		return false, nil
	}); err != nil {
		if !errors.Is(err, util.WaitTimedOut) {
			// Context cancellation or condition errors are fatal.
			return fmt.Errorf("failed to wait for screen to stabilize: %w", err)
		}
		// Phase 1 timeout is non-fatal: the agent may not echo
		// input (e.g. TUI agents buffer bracketed-paste content
		// internally). Proceed to Phase 2 to send the carriage
		// return.
		c.cfg.Logger.Info(
			"echo detection timed out, sending carriage return",
			"timeout", writeStabilizeEchoTimeout,
		)
	}

	// Phase 2: wait for the screen to change after the
	// carriage return is written (processing detection).
	screenBeforeCarriageReturn := readRegion()
	lastCarriageReturnTime := time.Time{}
	if err := util.WaitFor(ctx, util.WaitTimeout{
		Timeout:     writeStabilizeProcessTimeout,
		MinInterval: 25 * time.Millisecond,
		Clock:       c.cfg.Clock,
	}, func() (bool, error) {
		// we don't want to spam additional carriage returns because the agent may process them
		// (aider does this), but we do want to retry sending one if nothing's
		// happening for a while
		if c.cfg.Clock.Since(lastCarriageReturnTime) >= 3*time.Second {
			lastCarriageReturnTime = c.cfg.Clock.Now()
			if _, err := c.cfg.AgentIO.Write([]byte("\r")); err != nil {
				return false, fmt.Errorf("failed to write carriage return: %w", err)
			}
		}
		crTimer := c.cfg.Clock.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			crTimer.Stop()
			return false, ctx.Err()
		case <-crTimer.C:
		}
		crTimer.Stop()
		screen := readRegion()

		return screen != screenBeforeCarriageReturn, nil
	}); err != nil {
		if errors.Is(err, util.WaitTimedOut) {
			return fmt.Errorf("failed to wait for processing to start: %w", ErrMessageNotSubmitted)
		}
		return fmt.Errorf("failed to wait for processing to start: %w", err)
	}

	return nil
}
