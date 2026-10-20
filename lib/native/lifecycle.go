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
	settings := c.applyChosen(ctx, gen, adapter, caps)
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
	c.setSettingsLocked(settings)
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

// applyChosen gives the new adapter of generation gen the settings chosen
// for the session, and returns its settings. A setting it can't take is
// reported and left to the agent.
func (c *Conversation) applyChosen(ctx context.Context, gen int, adapter Adapter, caps Capabilities) Settings {
	configurable, ok := adapter.(Configurable)
	if !ok || !caps.Settings {
		return Settings{}
	}
	c.mu.Lock()
	chosen := c.chosen
	c.mu.Unlock()
	current := configurable.Settings()
	// One at a time, so that one failure doesn't stop the others; the
	// model goes first, as the effort depends on it.
	for _, change := range []Settings{
		{Model: chosen.Model},
		{Effort: chosen.Effort},
		{PermissionMode: chosen.PermissionMode},
	} {
		if change.IsZero() || current.Merge(change) == current {
			continue
		}
		applyCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := configurable.ApplySettings(applyCtx, change)
		cancel()
		if err != nil && !c.current(gen) {
			// Restart or Close stopped the adapter meanwhile.
			break
		}
		if err != nil {
			c.cfg.Logger.Warn("Failed to restore a setting of the session", "setting", change, "error", err)
			c.cfg.Emitter.EmitError(fmt.Sprintf("The agent didn't take the session's %s: %v", settingName(change), err), st.ErrorLevelWarning)
		}
	}
	return configurable.Settings()
}

// settingName names the setting that is set in s.
func settingName(s Settings) string {
	switch {
	case s.PermissionMode != "":
		return fmt.Sprintf("permission mode %s", s.PermissionMode)
	case s.Model != "":
		return fmt.Sprintf("model %s", s.Model)
	default:
		return fmt.Sprintf("reasoning effort %s", s.Effort)
	}
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
	c.compacting = false
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
	c.messages.reset()
	c.setLimitStopLocked(nil)
	if c.usage.Context != nil {
		// A new session starts with an empty context.
		usage := c.usage
		usage.Context = nil
		c.setUsageLocked(usage)
	}
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

// Reload replaces the agent with a new one that resumes the session, so the
// conversation goes on while what the agent reads when it starts (its MCP
// servers, its settings files) is read again. It returns like Restart. It
// returns ErrTurnActive while the agent works on a turn, which a reload
// would stop (or compacts its context), and ErrUnsupported for an agent
// whose session can't be resumed (Claude Code with
// --no-session-persistence).
func (c *Conversation) Reload(ctx context.Context) error {
	c.mu.Lock()
	if c.turnActive || c.compacting {
		c.mu.Unlock()
		return ErrTurnActive
	}
	if c.started && !c.caps.Resume {
		c.mu.Unlock()
		return ErrUnsupported
	}
	old := c.retireAdapterLocked()
	gen, ref, runCtx := c.generation, c.sessionRef, c.ctx
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	select {
	case err := <-c.launchAsync(runCtx, gen, ref):
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
	c.compacting = false
	c.loginWaiting = false
	c.startErrors = nil
	if c.loginWake != nil {
		close(c.loginWake)
		c.loginWake = nil
	}
	c.expireRequestsLocked(true)
	c.messages.forgetRich()
	return old
}
