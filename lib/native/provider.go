package native

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/provider"
)

// ErrProviderRelaunch is returned by ApplySettings for a switch of
// providers the agent can't make while it runs: BeginProviderSwitch makes
// it with another agent.
var ErrProviderRelaunch = errors.New("the switch of providers starts the agent again")

// ProviderRelaunch prepares the replacement for a provider switch. It runs
// inside ApplySettingsAndRelaunch's mutation sequence.
type ProviderRelaunch func() (*Replacement, error)

// ApplySettingsAndRelaunch applies change and, when that requires another
// process, runs relaunch with the replacement's prepare step in the same
// mutation sequence. No second settings request can modify the old adapter
// in between. A recovery relaunch runs on RelaunchError.Recovery, then still
// returns the original switch failure after a successful start.
func (c *Conversation) ApplySettingsAndRelaunch(ctx context.Context, change agentstate.Settings, relaunch func(ProviderRelaunch) error) error {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	err := c.applySettings(ctx, change)
	var switchErr *RelaunchError
	if !errors.Is(err, ErrProviderRelaunch) || !errors.As(err, &switchErr) || relaunch == nil {
		return err
	}
	target := change
	if switchErr.Recovery != nil {
		target = *switchErr.Recovery
	}
	begin := func() (*Replacement, error) {
		return c.beginProviderSwitch(ctx, target, switchErr.Carry)
	}
	if switchErr.Recovery == nil {
		return relaunch(begin)
	}
	if relaunchErr := relaunch(begin); relaunchErr != nil {
		// The server couldn't replace the agent now (a turn runs, it
		// restarts, the previous provider is gone). The conversation
		// recovers by itself, at once or when the turn ends.
		c.mu.Lock()
		deferred := c.providerUnsafe && c.started && c.adapter != nil
		c.recoverProviderLocked()
		c.mu.Unlock()
		if deferred {
			return err
		}
		return relaunchErr
	}
	return err
}

// recoverProviderLocked replaces an agent whose provider/model a failed
// switch left unknown with one on c.chosen, the last known configuration,
// in the same session, once no turn runs or compaction is under way. It is
// what recovers when the server can't relaunch the agent itself. c.mu is
// held.
func (c *Conversation) recoverProviderLocked() {
	if !c.providerUnsafe || !c.started || c.adapter == nil || c.turnActive || c.compacting {
		return
	}
	c.cfg.Logger.Warn("Starting the agent again on its previous provider after a failed switch", "provider", c.chosen.Provider)
	c.cfg.Emitter.EmitError("The provider switch failed; AgentAPI starts the agent again on its previous provider and model.", conv.ErrorLevelWarning)
	ctx, resume := c.ctx, c.resumeLocked()
	r := c.replaceLocked(resume)
	gen := r.gen
	c.cfg.Emitter.EmitStatus(conv.StatusInitializing)
	go func() {
		// Closed first, as Replacement.Start does: the new agent opens the
		// same session.
		r.CloseOld()
		if !c.current(gen) {
			return
		}
		if err := c.launch(ctx, gen, resume); err != nil {
			c.failStart(gen, err, true)
		}
	}()
}

// launchProviderLocked is the provider the next agent starts on: the one
// chosen, nil for the agent's own. A chosen provider no longer defined is
// dropped, with a warning: the agent starts on its own. One the hub may
// have, before it answered, stays chosen while the agent starts on its own.
func (c *Conversation) launchProviderLocked() *ProviderRun {
	id := c.chosen.Provider
	if id == "" || id == provider.Default {
		return nil
	}
	listed, ok := c.cfg.Providers.Lookup(id)
	if !ok && !c.cfg.Providers.HubSettled() {
		// It may be the hub's, which didn't answer yet: it stays chosen,
		// for the next start.
		c.cfg.Logger.Warn("The provider chosen for the session isn't known yet (the hub didn't answer); the agent starts on its own this time", "provider", id)
		c.cfg.Emitter.EmitError(fmt.Sprintf("The model provider %s isn't known yet: the hub didn't answer. The agent starts on its own provider; it goes back to %s when it starts again once the hub answers.", id, id), conv.ErrorLevelWarning)
		return nil
	}
	if !ok {
		c.cfg.Logger.Warn("The provider chosen for the session is no longer defined; the agent starts on its own", "provider", id)
		c.cfg.Emitter.EmitError(fmt.Sprintf("The model provider %s is no longer defined; the agent starts on its own provider.", id), conv.ErrorLevelWarning)
		c.chosen.Provider = provider.Default
		c.chosen.Model = ""
		c.stateChangedLocked()
		return nil
	}
	return &ProviderRun{Provider: listed.Provider, Model: c.chosen.Model}
}

// startedOnLocked records the provider the agent of a launch started on.
func (c *Conversation) startedOnLocked(adapter Adapter, run *ProviderRun) {
	_, c.providerCap = adapter.(ProviderRunner)
	c.provider = provider.Default
	if run != nil {
		c.provider = run.Provider.ID
	}
	c.providerSince = c.cfg.Clock.Now()
}

// reportProviderLocked tells Config.ProviderUsed the provider the session
// runs on, for an agent that started and can switch providers, once its
// session is known: not the session a fork copies, whose own the agent
// reports later. The same report again is left out.
func (c *Conversation) reportProviderLocked() {
	if c.cfg.ProviderUsed == nil || !c.started || !c.providerCap || c.sessionRef == "" || c.forking {
		return
	}
	use := ProviderUse{At: c.providerSince, Session: c.sessionRef, Provider: c.provider}
	if use == c.providerReported {
		return
	}
	c.providerReported = use
	c.cfg.ProviderUsed(use)
}

// withProviderLocked is settings with the provider the agent runs on, for
// an agent that can switch providers.
func (c *Conversation) withProviderLocked(settings agentstate.Settings) agentstate.Settings {
	settings.Provider = ""
	if c.providerCap {
		settings.Provider = c.provider
	}
	return settings
}

// providerOptions are the providers an agent that can switch providers
// can switch to, with their models.
func (c *Conversation) providerOptions(ctx context.Context) []ProviderOption {
	all := c.cfg.Providers.List()
	options := make([]ProviderOption, len(all))
	// The providers that list their models are asked at once, not one
	// after the other.
	var wg sync.WaitGroup
	for i, listed := range all {
		options[i] = ProviderOption{ID: listed.ID, Name: listed.DisplayName(), Host: listed.Host(), Origin: listed.Origin, Models: []ModelOption{}}
		wg.Go(func() {
			for _, m := range c.cfg.Providers.Models(ctx, listed.Provider) {
				options[i].Models = append(options[i].Models, ModelOption{ID: m.ID, Name: m.DisplayName()})
			}
		})
	}
	wg.Wait()
	return options
}

// providerTarget resolves the provider a change switches to (nil for the
// agent's own) and the model the agent runs on there: the change's, else
// the provider's first. A model a provider doesn't offer is an
// ErrInvalidSetting.
func (c *Conversation) providerTarget(ctx context.Context, change agentstate.Settings) (*provider.Provider, string, error) {
	if change.Provider == provider.Default {
		model := change.Model
		if model == "" {
			c.mu.Lock()
			model = c.ownModel
			c.mu.Unlock()
		}
		return nil, model, nil
	}
	listed, ok := c.cfg.Providers.Lookup(change.Provider)
	if !ok {
		return nil, "", fmt.Errorf("%w: provider %q isn't one AgentAPI offers", ErrInvalidSetting, change.Provider)
	}
	model, err := c.providerModel(ctx, listed.Provider, change.Model)
	if err != nil {
		return nil, "", err
	}
	p := listed.Provider
	return &p, model, nil
}

// providerModel checks that p offers model, or picks its first for "".
// A provider that lists no models takes any.
func (c *Conversation) providerModel(ctx context.Context, p provider.Provider, model string) (string, error) {
	models := c.cfg.Providers.Models(ctx, p)
	switch {
	case model == "" && len(models) > 0:
		return models[0].ID, nil
	case model == "":
		return "", fmt.Errorf("%w: %s lists no models; choose one by its ID", ErrInvalidSetting, p.DisplayName())
	case len(models) > 0 && !slices.ContainsFunc(models, func(m provider.Model) bool { return m.ID == model }):
		return "", fmt.Errorf("%w: model %q isn't one %s offers", ErrInvalidSetting, model, p.DisplayName())
	}
	return model, nil
}

// switchProvider makes the provider change of ApplySettings on the agent
// while it runs, then the rest of change. It returns ErrProviderRelaunch
// when the agent can't.
func (c *Conversation) switchProvider(ctx context.Context, gen int, runner ProviderRunner, configurable Configurable, change agentstate.Settings) error {
	to, model, err := c.providerTarget(ctx, change)
	if err != nil {
		return err
	}
	err = runner.SwitchProvider(ctx, to, model)
	var relaunch *RelaunchError
	if errors.As(err, &relaunch) {
		c.mu.Lock()
		if gen != c.generation {
			c.mu.Unlock()
			return ErrNotStarted
		}
		if relaunch.Recovery != nil {
			// The old process may be on the requested provider, the previous
			// one, or a mixture. No turn runs until a new process has a known
			// one, and every start from here (a recovery, a restart, an exit)
			// is on the previous configuration.
			c.providerUnsafe = true
			chosen := c.chosen
			chosen.Provider, chosen.Model = relaunch.Recovery.Provider, relaunch.Recovery.Model
			c.chooseLocked(chosen)
			c.cfg.Emitter.EmitStatus(c.statusLocked())
		}
		c.mu.Unlock()
		return fmt.Errorf("%w: %w", ErrProviderRelaunch, err)
	}
	if err != nil {
		return err
	}
	settings := configurable.Settings()
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return ErrNotStarted
	}
	c.leaveOwnLocked(change.Provider)
	c.provider = change.Provider
	c.providerSince = c.cfg.Clock.Now()
	chosen := c.chosen
	chosen.Provider = change.Provider
	chosen.Model = settings.Model
	c.chosen = chosen
	c.setSettingsLocked(settings)
	c.reportProviderLocked()
	c.mu.Unlock()
	// The rest applies on the new provider.
	rest := change
	rest.Provider, rest.Model = "", ""
	if rest.IsZero() {
		return nil
	}
	return c.applySettings(ctx, rest)
}

// BeginProviderSwitch starts the switch of providers ApplySettings
// returned ErrProviderRelaunch for, with the RelaunchError's carry: it
// retires the agent and returns what starts one on the provider change
// chooses, which continues the session, or with carry starts a new one
// with the conversation's text, the conversation staying as it is. The
// other settings of change are chosen too, for the new agent. It returns
// ErrTurnActive while the agent works on a turn or compacts its context,
// and an ErrInvalidSetting for a change it can't make.
func (c *Conversation) BeginProviderSwitch(ctx context.Context, change agentstate.Settings, carry bool) (*Replacement, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	return c.beginProviderSwitch(ctx, change, carry)
}

// beginProviderSwitch is BeginProviderSwitch with settingsMu held.
func (c *Conversation) beginProviderSwitch(ctx context.Context, change agentstate.Settings, carry bool) (*Replacement, error) {
	_, model, err := c.providerTarget(ctx, change)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started || c.adapter == nil {
		return nil, ErrNotStarted
	}
	if c.turnActive || c.compacting {
		return nil, ErrTurnActive
	}
	if !c.providerCap {
		return nil, fmt.Errorf("%w: the agent can't switch providers", ErrInvalidSetting)
	}
	chosen := c.chosen.Merge(change)
	chosen.Model = model
	c.chosen = chosen
	resume := Resume{}
	switch {
	case len(c.messages.messages) == 0:
		// Nothing to continue: the agent may not have kept the session.
	case carry:
		resume.Carry = c.messages.snapshot()
	default:
		resume.Ref = c.sessionRef
	}
	r := c.replaceLocked(resume)
	c.sessionRef, c.forking = resume.Ref, false
	c.setHoldLocked(hold{})
	c.clearSessionUsageLocked()
	c.leaveOwnLocked(change.Provider)
	c.provider = change.Provider
	c.setSettingsLocked(c.settings)
	return r, nil
}

// leaveOwnLocked remembers the model the agent runs on its own provider
// when it switches to another.
func (c *Conversation) leaveOwnLocked(to string) {
	if c.provider == provider.Default && to != provider.Default {
		c.ownModel = c.settings.Model
	}
}
