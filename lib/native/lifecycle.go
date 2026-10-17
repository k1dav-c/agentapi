package native

import (
	"context"
	"errors"
	"fmt"

	st "github.com/coder/agentapi/lib/screentracker"
)

// Start launches the agent in the background. Status reports initializing
// until the agent accepts turns.
func (c *Conversation) Start(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	gen, ref := c.generation, c.sessionRef
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	c.launchAsync(ctx, gen, ref)
}

// launchAsync launches the adapter of generation gen in the background and
// reports a failure to start. The returned channel receives launch's
// result.
func (c *Conversation) launchAsync(ctx context.Context, gen int, resumeRef string) <-chan error {
	done := make(chan error, 1)
	go func() {
		err := c.launch(ctx, gen, resumeRef)
		if err != nil {
			c.failStart(gen, err)
		}
		done <- err
	}()
	return done
}

// launch creates and starts the adapter of generation gen. It returns nil
// once the adapter accepts turns, when it waits for a login, and when it was
// stopped while it started.
func (c *Conversation) launch(ctx context.Context, gen int, resumeRef string) error {
	adapter, err := c.cfg.Factory(resumeRef)
	if err != nil {
		return fmt.Errorf("create adapter: %w", err)
	}
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		_ = adapter.Close()
		return nil
	}
	c.adapter = adapter
	c.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, c.cfg.StartTimeout)
	defer cancel()
	if err := adapter.Start(startCtx, &sink{c: c, gen: gen}); err != nil {
		_ = adapter.Close()
		var login *LoginRequiredError
		switch {
		case !c.current(gen) || ctx.Err() != nil:
			// Restart or Close stopped the adapter while it started, or
			// AgentAPI stops.
			c.cfg.Logger.Debug("The agent stopped while it started", "error", err)
			return nil
		case errors.As(err, &login):
			c.awaitLogin(ctx, gen, resumeRef, login.Login)
			return nil
		default:
			return err
		}
	}
	// Adapters know what they support once they started.
	caps := adapter.Capabilities()
	go func() { c.signalExit(gen, adapter.Wait()) }()
	var prompt Input
	if c.cfg.InitialPrompt != "" {
		prompt = InputFromText(c.cfg.InitialPrompt)
	}

	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.caps = caps
	c.loginWaiting = false
	c.startErrors = nil
	if c.login != nil {
		c.login = nil
		c.cfg.Observer.LoginRequired(nil)
	}
	c.cfg.Observer.Started(caps)
	// The prompt's turn begins before the lock is released, so no other
	// message can start a turn first.
	var turn *pendingTurn
	if c.cfg.InitialPrompt != "" && !c.promptSent {
		c.promptSent = true
		if t, err := c.beginTurnLocked(prompt); err != nil {
			c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), st.ErrorLevelError)
		} else {
			turn = &t
		}
	}
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()

	if turn != nil {
		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		defer cancel()
		if err := c.deliverTurn(sendCtx, *turn); err != nil {
			c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), st.ErrorLevelError)
		}
	}
	return nil
}

// current reports whether gen is the current generation.
func (c *Conversation) current(gen int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return gen == c.generation
}

// failStart reports that the adapter of generation gen failed to start.
func (c *Conversation) failStart(gen int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		return
	}
	c.cfg.Logger.Error("Failed to start the agent", "error", err)
	c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to start the agent: %v", err), st.ErrorLevelError)
	c.exitLocked(err)
}

// signalExit reports that the adapter of generation gen exited. Exits of
// replaced adapters are expected and ignored.
func (c *Conversation) signalExit(gen int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen == c.generation {
		c.exitLocked(err)
	}
}

// exitLocked records that the agent is gone and delivers err on Exited.
func (c *Conversation) exitLocked(err error) {
	c.started = false
	c.turnActive = false
	c.expireRequestsLocked(true)
	select {
	case c.exited <- err:
	default:
	}
}

// Exited delivers the error (nil for a clean exit) when the agent exits on
// its own or fails to start, also when Restart or RetryLogin started it.
// The exits Restart and Close cause are not delivered.
func (c *Conversation) Exited() <-chan error { return c.exited }

// Restart replaces the agent with a new one in a new session and clears the
// conversation; the initial prompt is sent again. It returns once the new
// agent accepts turns or waits for a login, or with the error it failed to
// start with, which Exited delivers too. If ctx ends first, Restart returns
// ctx.Err() and the agent goes on starting in the background.
func (c *Conversation) Restart(ctx context.Context) error {
	c.mu.Lock()
	old := c.retireAdapterLocked()
	c.sessionRef = ""
	c.promptSent = false
	c.resetMessagesLocked()
	gen, runCtx := c.generation, c.ctx
	c.cfg.Emitter.EmitMessages(nil)
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	select {
	case err := <-c.launchAsync(runCtx, gen, ""):
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the agent. The exit is not reported on Exited.
func (c *Conversation) Close() error {
	c.mu.Lock()
	old := c.retireAdapterLocked()
	c.mu.Unlock()
	if old == nil {
		return nil
	}
	return old.Close()
}

// retireAdapterLocked stops using the current adapter and returns it for the
// caller to close once it unlocked. The adapter's events are ignored from now
// on, its requests can't be answered any more, and a wait for a login ends.
func (c *Conversation) retireAdapterLocked() Adapter {
	c.generation++
	old := c.adapter
	c.adapter = nil
	c.started = false
	c.turnActive = false
	c.loginWaiting = false
	c.startErrors = nil
	if c.loginWake != nil {
		close(c.loginWake)
		c.loginWake = nil
	}
	c.expireRequestsLocked(true)
	// The next adapter may use this one's rich message IDs again.
	c.agent = nil
	clear(c.rich)
	return old
}
