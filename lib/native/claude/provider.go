package claude

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerproxy"
)

var _ native.ProviderRunner = (*Adapter)(nil)

// providerModelEnv are the variables that name the models Claude Code uses
// besides the main one: the aliases, its small fast model (titles,
// summaries) and its subagents'. On a provider they all name the model
// the session runs on, which the provider has.
var providerModelEnv = []string{
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL",
}

// providerEnv is the environment that has Claude Code call p on model,
// through AgentAPI's provider proxy: the proxy's token as a bearer token,
// never p's key, and no Anthropic API key, which would go to p too.
func (a *Adapter) providerEnv(p provider.Provider, model string) (map[string]string, error) {
	endpoint, err := native.ProviderEndpoint(a.cfg.ProviderProxy, p)
	if err != nil {
		return nil, err
	}
	return endpointEnv(endpoint, model), nil
}

// endpointEnv is the environment that has Claude Code call the endpoint on
// model.
func endpointEnv(endpoint providerproxy.Endpoint, model string) map[string]string {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":   endpoint.AnthropicBaseURL,
		"ANTHROPIC_AUTH_TOKEN": endpoint.Token,
		"ANTHROPIC_API_KEY":    "",
	}
	for _, name := range providerModelEnv {
		env[name] = model
	}
	return env
}

// launchEnv is the environment a new process on the launch provider
// starts with: also the model it starts on, unless --model chose one.
func (a *Adapter) launchEnv() ([]string, error) {
	run := a.launch
	if run == nil {
		return nil, nil
	}
	env, err := a.providerEnv(run.Provider, run.Model)
	if err != nil {
		return nil, err
	}
	env["ANTHROPIC_MODEL"] = run.Model
	return envList(env), nil
}

// envList is env as NAME=value entries, sorted.
func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for _, name := range slices.Sorted(maps.Keys(env)) {
		out = append(out, name+"="+env[name])
	}
	return out
}

// SwitchProvider implements native.ProviderRunner. Claude Code takes
// another provider while it runs: its environment changes through flag
// settings, from the next API call. Back on its own provider it starts
// again, resuming the session: only a new process has the environment it
// had (the user's ANTHROPIC_* variables, or none for a login).
func (a *Adapter) SwitchProvider(ctx context.Context, to *provider.Provider, model string) error {
	if to == nil {
		return &native.RelaunchError{}
	}
	a.mu.Lock()
	from, fromModel := a.provider, a.model
	a.mu.Unlock()
	env, err := a.providerEnv(*to, model)
	if err != nil {
		return fmt.Errorf("switch to %s: %w", to.DisplayName(), err)
	}
	if err := a.applyEnv(ctx, env); err != nil {
		failure := fmt.Errorf("switch to %s: %w", to.DisplayName(), err)
		if errors.Is(err, native.ErrUnconfirmed) {
			// apply_flag_settings may have changed any of the variables. Only
			// a new process is known to have the complete previous environment.
			return providerRecovery(from, fromModel, failure)
		}
		return failure
	}
	// Claude Code checks the model with the provider before it switches.
	if _, err := a.request(ctx, "set_model", map[string]any{"model": model}, nil); err != nil {
		failure := fmt.Errorf("%s didn't take model %s: %w", to.DisplayName(), model, err)
		if from == nil || errors.Is(err, native.ErrUnconfirmed) {
			// The original process environment can't be reconstructed, and an
			// unconfirmed set_model may have taken effect. Restore in a new one.
			return providerRecovery(from, fromModel, failure)
		}
		rollback, rollbackErr := a.providerEnv(*from, fromModel)
		if rollbackErr == nil {
			rollbackErr = a.applyEnv(ctx, rollback)
		}
		if rollbackErr != nil {
			return providerRecovery(from, fromModel, fmt.Errorf("%v; restore %s: %w", failure, from.DisplayName(), rollbackErr))
		}
		return failure
	}
	a.mu.Lock()
	p := *to
	a.provider = &p
	a.model = model
	a.mu.Unlock()
	return nil
}

// providerRecovery asks the conversation to replace the process, whose
// provider/model is no longer known, with one on the complete last known
// configuration. The replacement must never use the failed switch's target.
func providerRecovery(from *provider.Provider, fromModel string, cause error) error {
	change := agentstate.Settings{Provider: provider.Default, Model: fromModel}
	if from != nil {
		change.Provider = from.ID
	}
	return &native.RelaunchError{Recovery: &change, Cause: cause}
}

// applyEnv sets Claude Code's environment variables in env.
func (a *Adapter) applyEnv(ctx context.Context, env map[string]string) error {
	_, err := a.request(ctx, "apply_flag_settings", map[string]any{"settings": map[string]any{"env": env}}, nil)
	return err
}

// onProvider reports whether the agent runs on a provider, and which.
func (a *Adapter) onProvider() (provider.Provider, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider == nil {
		return provider.Provider{}, false
	}
	return *a.provider, true
}

// providerModelChanged has the models besides the main one follow it on a
// provider.
func (a *Adapter) providerModelChanged(ctx context.Context, model string) error {
	env := map[string]string{}
	for _, name := range providerModelEnv {
		env[name] = model
	}
	return a.applyEnv(ctx, env)
}

// launchProvider has the adapter start on run (nil: its own provider).
func (a *Adapter) launchProvider(run *native.ProviderRun) {
	a.launch = run
	if run != nil {
		p := run.Provider
		a.provider = &p
	}
}
