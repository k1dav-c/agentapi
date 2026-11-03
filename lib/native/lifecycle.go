package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
)

// Start launches the agent in the background. Status reports initializing
// until the agent accepts turns.
func (c *Conversation) Start(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	gen, resume := c.generation, c.resumeLocked()
	c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
	c.mu.Unlock()
	c.launchAsync(ctx, gen, resume, true)
}

// launchAsync launches the adapter of generation gen in the background and
// reports a failure to start, which is tried again if retry is set (see
// failStart). The returned channel receives launch's result.
func (c *Conversation) launchAsync(ctx context.Context, gen int, resume Resume, retry bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		err := c.launch(ctx, gen, resume)
		if err != nil {
			c.failStart(gen, err, retry)
		}
		done <- err
	}()
	return done
}

// launch creates and starts the adapter of generation gen. It returns nil
// once the adapter accepts turns, when it waits for a login, and when it was
// stopped while it started.
func (c *Conversation) launch(ctx context.Context, gen int, resume Resume) error {
	adapter, err := c.cfg.Factory(resume)
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
			c.awaitLogin(ctx, gen, resume, login.Login)
			return nil
		default:
			return err
		}
	}
	// Adapters know what they support once they started.
	caps := adapter.Capabilities()
	startedAt := c.cfg.Clock.Now()
	go func() { c.signalExit(gen, adapter, startedAt, adapter.Wait()) }()
	settings := c.applyChosen(ctx, gen, adapter, caps)
	var prompt Input
	if c.cfg.InitialPrompt != "" {
		// Before the lock: it looks for the files the prompt names.
		prompt = InputFromText(c.cfg.InitialPrompt)
	}

	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return nil
	}
	c.markStartedLocked(caps, settings)
	// The prompt's turn begins before the lock is released, so no other
	// message can start a turn first.
	turn := c.beginInitialPromptLocked(prompt)
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()

	if turn != nil {
		c.sendInitialPrompt(ctx, *turn)
	}
	return nil
}

// markStartedLocked records that the agent started, with caps and
// settings: it accepts turns, and no start waits for a login any more.
func (c *Conversation) markStartedLocked(caps Capabilities, settings Settings) {
	c.started = true
	c.ran = true
	c.caps = caps
	c.setSettingsLocked(settings)
	c.loginWaiting = false
	c.startErrors = nil
	c.clearLoginLocked()
	c.cfg.Observer.Started(caps)
}

// beginInitialPromptLocked begins the turn of the initial prompt, read as
// prompt, unless there is none or it was sent already. It returns the turn
// to send, or nil.
func (c *Conversation) beginInitialPromptLocked(prompt Input) *pendingTurn {
	if c.cfg.InitialPrompt == "" || c.promptSent {
		return nil
	}
	c.promptSent = true
	t, err := c.beginTurnLocked(prompt)
	if err != nil {
		c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), conv.ErrorLevelError)
		return nil
	}
	return &t
}

// sendInitialPrompt sends the initial prompt's turn.
func (c *Conversation) sendInitialPrompt(ctx context.Context, turn pendingTurn) {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := c.deliverTurn(sendCtx, turn); err != nil {
		c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), conv.ErrorLevelError)
	}
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
			c.cfg.Emitter.EmitError(fmt.Sprintf("The agent didn't take the session's %s: %v", settingName(change), err), conv.ErrorLevelWarning)
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

// AgentExit is an agent that exited on its own or failed to start, and
// what the conversation does about it.
type AgentExit struct {
	// Err is why: the exit's error (nil for a clean exit), or the start's.
	Err error
	// Attempt is the start the conversation makes after Delay, from 1 to
	// Attempts; 0 once it gave up: no agent runs until a restart (Restart,
	// Switch).
	Attempt  int
	Attempts int
	Delay    time.Duration
}

// failStart handles the failed start of the adapter of generation gen. A
// start the conversation made by itself (retry) is made again after the
// next restart delay; one a caller waits for (Replacement.Start) leaves no
// agent running.
func (c *Conversation) failStart(gen int, err error, retry bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		return
	}
	c.cfg.Logger.Error("Failed to start the agent", "error", err)
	c.downLocked()
	what := fmt.Sprintf("Failed to start the agent: %v", err)
	if retry {
		c.startAgainLocked(sentence(what), err)
		return
	}
	c.cfg.Emitter.EmitError(what, conv.ErrorLevelError)
	c.cfg.Observer.AgentExited(AgentExit{Err: err})
}

// signalExit handles the exit of adapter, of generation gen, which started
// at startedAt, with err (nil for a clean exit): the conversation starts
// the agent again. One that lost its login before it exited waits for the
// login instead, as when a start finds none. Exits of replaced adapters
// are expected and ignored.
func (c *Conversation) signalExit(gen int, adapter Adapter, startedAt time.Time, err error) {
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return
	}
	if c.cfg.Clock.Since(startedAt) >= restartReset {
		c.restarts = 0
	}
	c.downLocked()
	if c.login != nil {
		c.cfg.Logger.Warn("The agent exited after it lost its login; waiting for the login", "error", err)
		c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
		c.awaitLoginLocked(c.ctx, c.resumeLocked())
	} else {
		what := "The agent exited."
		if err != nil {
			what = sentence(fmt.Sprintf("The agent exited: %v", err))
		}
		c.startAgainLocked(what, err)
	}
	c.mu.Unlock()
	// Gone already; closing lets go of what the adapter still holds.
	_ = adapter.Close()
}

// sentence ends s with a full stop, unless it ends with one already.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// downLocked records that the agent is gone, as it exited or failed to
// start: no turn runs, and its requests expire. The next agent gets a
// generation of its own, so the old one's events are ignored.
func (c *Conversation) downLocked() {
	c.generation++
	c.adapter = nil
	c.started = false
	c.turnActive = false
	c.unconfirmedTurn = false
	c.compacting = false
	c.expireRequestsLocked(true)
	c.messages.forgetRich()
}

// startAgainLocked starts the agent again after the next restart delay,
// on its session, and reports it; what says what happened to it. Once the
// delays are used up it gives up: no agent runs until a restart.
func (c *Conversation) startAgainLocked(what string, err error) {
	delays := c.cfg.RestartDelays
	// A start that fails now isn't a check for a login.
	c.loginWaiting = false
	if c.restarts >= len(delays) {
		c.cfg.Logger.Error("The agent is gone; not starting it again", "error", err, "attempts", c.restarts)
		c.cfg.Emitter.EmitError(what+" "+gaveUpText(c.restarts), conv.ErrorLevelError)
		// Nothing waits for a login now.
		c.clearLoginLocked()
		c.cfg.Observer.AgentExited(AgentExit{Err: err})
		return
	}
	delay := delays[c.restarts]
	c.restarts++
	resume := c.restartResumeLocked()
	var how string
	switch {
	case resume.Ref != "":
		how = " on its session"
	case c.ran:
		how = " in a new session"
	}
	c.cfg.Logger.Warn("Starting the agent again", "error", err, "in", delay, "attempt", c.restarts, "of", len(delays))
	c.cfg.Emitter.EmitError(fmt.Sprintf("%s AgentAPI starts it again%s in %s (attempt %d of %d).", what, how, delay, c.restarts, len(delays)), conv.ErrorLevelError)
	c.cfg.Observer.AgentExited(AgentExit{Err: err, Attempt: c.restarts, Attempts: len(delays), Delay: delay})
	c.relaunchLocked(c.ctx, resume, delay, "restart")
}

// gaveUpText says that AgentAPI stopped starting the agent after attempts.
func gaveUpText(attempts int) string {
	if attempts == 0 {
		return "Restart the agent to go on."
	}
	return fmt.Sprintf("AgentAPI gave up after %d attempts to start it again; restart the agent to go on.", attempts)
}

// restartResumeLocked is the session the agent opens when it is started
// again after an exit: its own, unless the last agent couldn't resume it.
func (c *Conversation) restartResumeLocked() Resume {
	if !c.ran || c.caps.Resume {
		return c.resumeLocked()
	}
	return Resume{}
}

// relaunchLocked starts the agent of a new generation, which opens the
// session resume says, once delay passed (closing relaunchWake cuts the
// wait short), unless it is replaced or closed meanwhile. tag names the
// wait's timer. A start that fails is made again (see failStart).
func (c *Conversation) relaunchLocked(ctx context.Context, resume Resume, delay time.Duration, tag string) {
	wake := make(chan struct{})
	c.relaunchWake = wake
	go c.relaunchAfter(ctx, wake, c.generation, resume, delay, tag)
}

func (c *Conversation) relaunchAfter(ctx context.Context, wake <-chan struct{}, gen int, resume Resume, delay time.Duration, tag string) {
	timer := c.cfg.Clock.NewTimer(delay, "Conversation", tag)
	defer timer.Stop("Conversation", tag)
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	case <-wake:
	}
	c.mu.Lock()
	current := gen == c.generation
	if current {
		c.relaunchWake = nil
		c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
	}
	c.mu.Unlock()
	if !current {
		return
	}
	if err := c.launch(ctx, gen, resume); err != nil {
		c.failStart(gen, err, true)
	}
}

// Replacement replaces the conversation's agent with a new one, in two
// steps, so that a caller can make the change at once and wait for the new
// agent without holding up anything else. The first step (BeginRestart,
// BeginSwitch) retires the agent: from then on its events are ignored, its
// requests can't be answered and no turn starts. Start, the second, closes
// the retired agent and starts the new one. A Replacement must be started,
// or the retired agent stays open.
type Replacement struct {
	c      *Conversation
	gen    int
	resume Resume
	runCtx context.Context
	old    Adapter
	once   sync.Once
}

// replaceLocked retires the agent for one that opens the session resume
// says. The new agent's exits count from the first restart delay.
func (c *Conversation) replaceLocked(resume Resume) *Replacement {
	old := c.retireAdapterLocked()
	c.restarts = 0
	return &Replacement{c: c, gen: c.generation, resume: resume, runCtx: c.ctx, old: old}
}

// CloseOld closes the agent that was retired, once. Start closes it too;
// calling CloseOld first stops the old agent before something that waits
// for it (an update of the agent's CLI).
func (r *Replacement) CloseOld() {
	r.once.Do(func() {
		if r.old != nil {
			_ = r.old.Close()
		}
	})
}

// Start closes the retired agent and starts the new one, whose status is
// initializing until it accepts turns. It returns once the new agent
// accepts turns or waits for a login, or with the error it failed to start
// with, which the Observer gets too (AgentExited): the conversation
// doesn't start it again by itself. If ctx ends first, Start returns
// ctx.Err() and the agent goes on starting in the background. A
// conversation that was closed or replaced again meanwhile starts nothing.
func (r *Replacement) Start(ctx context.Context) error {
	r.CloseOld()
	r.c.mu.Lock()
	if r.gen == r.c.generation {
		r.c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
	}
	r.c.mu.Unlock()
	select {
	case err := <-r.c.launchAsync(r.runCtx, r.gen, r.resume, false):
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Restart replaces the agent with a new one in a new session and clears the
// conversation; the initial prompt is sent again. It returns like
// Replacement.Start.
func (c *Conversation) Restart(ctx context.Context) error {
	return c.BeginRestart(nil).Start(ctx)
}

// BeginRestart is the first step of Restart: it retires the agent and
// clears the conversation, and returns what starts the new agent. The
// status stays as it was until Start reports the new agent's. reset, if
// set, runs once the agent is retired, before the cleared conversation is
// reported, with the conversation's lock held: like an Observer, it must
// not call back into the conversation.
func (c *Conversation) BeginRestart(reset func()) *Replacement {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.replaceLocked(Resume{})
	if reset != nil {
		reset()
	}
	c.sessionRef = ""
	c.forking = false
	c.promptSent = false
	c.messages.reset()
	c.setLimitStopLocked(nil)
	c.clearSessionUsageLocked()
	c.cfg.Emitter.EmitMessages(nil)
	return r
}

// Switch replaces the agent with a new one that opens the session resume
// says (an earlier one, or a copy of it), and the conversation with
// messages, that session's so far; the initial prompt is not sent. It
// returns like Replacement.Start, and ErrTurnActive while the agent works
// on a turn or compacts its context, which a switch would cut short.
func (c *Conversation) Switch(ctx context.Context, resume Resume, messages []conv.Message) error {
	r, err := c.BeginSwitch(resume, messages, nil)
	if err != nil {
		return err
	}
	return r.Start(ctx)
}

// BeginSwitch is the first step of Switch. Checking that no turn runs and
// retiring the agent are one step: while the agent works on a turn or
// compacts its context, BeginSwitch returns ErrTurnActive and changes
// nothing, reset included. reset runs as BeginRestart's does.
func (c *Conversation) BeginSwitch(resume Resume, messages []conv.Message, reset func()) (*Replacement, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turnActive || c.compacting {
		return nil, ErrTurnActive
	}
	r := c.replaceLocked(resume)
	if reset != nil {
		reset()
	}
	c.sessionRef, c.forking = resume.Ref, resume.Fork
	c.promptSent = true
	c.messages = newMessageLog(c.cfg.Clock, messages)
	c.setLimitStopLocked(nil)
	// The agent reports the session's usage once it ran a turn.
	c.clearSessionUsageLocked()
	// Until the agent reports a fork's own ref, AgentAPI would resume the
	// session it forked after a restart.
	c.cfg.Observer.SessionRef(resume.Ref)
	c.cfg.Emitter.EmitMessages(c.messages.snapshot())
	return r, nil
}

// clearSessionUsageLocked forgets the usage that belongs to the session: a
// new one starts with an empty context, and nothing cached. The rate limits
// are the account's and stay.
func (c *Conversation) clearSessionUsageLocked() {
	if c.usage.Context != nil || c.usage.PromptCache != nil {
		usage := c.usage
		usage.Context = nil
		usage.PromptCache = nil
		c.setUsageLocked(usage)
	}
}

// Close stops the agent. Its exit isn't reported to the Observer.
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
// on, its requests can't be answered any more, and a wait to start the agent
// again (for a login, after an exit) ends.
func (c *Conversation) retireAdapterLocked() Adapter {
	old := c.adapter
	c.loginWaiting = false
	c.startErrors = nil
	c.wakeRelaunchLocked()
	c.downLocked()
	return old
}

// wakeRelaunchLocked cuts short the wait before the agent is started
// again, if one runs (see relaunchLocked).
func (c *Conversation) wakeRelaunchLocked() {
	if c.relaunchWake != nil {
		close(c.relaunchWake)
		c.relaunchWake = nil
	}
}
