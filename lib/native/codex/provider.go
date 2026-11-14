package codex

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/coder/agentapi/lib/agentlog/codexlog"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerproxy"
)

var _ native.ProviderRunner = (*Adapter)(nil)

// providerKeyEnv holds the token of AgentAPI's provider proxy for
// app-server, the key it calls the provider with. Codex leaves variables
// named like a key out of the environment of the commands it runs
// (shell_environment_policy's default excludes).
const providerKeyEnv = "AGENTAPI_PROVIDER_API_KEY"

// providerName is the model_providers entry of p: AgentAPI's own, so it
// doesn't replace one of the user's config. The session log records it,
// which attributes the calls to p (codexlog.ProviderID).
func providerName(p provider.Provider) string {
	return codexlog.ProviderPrefix + p.ID
}

// providerArgs are the app-server flags that define p, called at
// endpoint, and run on it, through the Responses API.
func providerArgs(p provider.Provider, endpoint providerproxy.Endpoint) []string {
	quote := func(s string) string {
		// A JSON string is a TOML basic string.
		b, _ := json.Marshal(s)
		return string(b)
	}
	table := "{name=" + quote(p.DisplayName()) +
		",base_url=" + quote(endpoint.OpenAIBaseURL) +
		",env_key=" + quote(providerKeyEnv) +
		",wire_api=\"responses\"}"
	return []string{
		"-c", "model_providers." + providerName(p) + "=" + table,
		"-c", "model_provider=" + quote(providerName(p)),
	}
}

// launchEndpoint is where app-server calls the provider it runs on,
// through AgentAPI's provider proxy; nil on Codex's own.
func (a *Adapter) launchEndpoint() (*providerproxy.Endpoint, error) {
	if a.launch == nil {
		return nil, nil
	}
	endpoint, err := native.ProviderEndpoint(a.cfg.ProviderProxy, a.launch.Provider)
	if err != nil {
		return nil, err
	}
	return &endpoint, nil
}

// SwitchProvider implements native.ProviderRunner. Codex binds a thread to
// the provider of the app-server that runs it, which can't change, and a
// thread holds items of its provider's own (OpenAI's tool definitions and
// encrypted reasoning) that another provider refuses: the conversation
// starts app-server again on the other provider, in a new thread that
// gets the conversation's text (injectCarry).
func (a *Adapter) SwitchProvider(context.Context, *provider.Provider, string) error {
	return &native.RelaunchError{Carry: true}
}

// launchModel is the model a thread starts, resumes or forks on: the
// provider's, else the one the command line chose.
func (a *Adapter) launchModel() string {
	if a.launch != nil && a.launch.Model != "" {
		return a.launch.Model
	}
	return a.opts.model
}

// maxCarry is how much of the conversation's text a new thread gets, the
// latest messages first: about 50,000 tokens.
const maxCarry = 200_000

// carryItems are the Responses API items that give a new thread the
// conversation of another provider's: a note, then its messages as text,
// the latest that fit in maxCarry.
func carryItems(messages []conv.Message) []any {
	text := func(role, kind, s string) any {
		return map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": kind, "text": s}}}
	}
	var items []any
	size := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Message == "" {
			continue
		}
		size += len(m.Message)
		if size > maxCarry {
			break
		}
		if m.Role == conv.RoleUser {
			items = append(items, text("user", "input_text", m.Message))
		} else {
			items = append(items, text("assistant", "output_text", m.Message))
		}
	}
	slices.Reverse(items)
	note := "The conversation so far ran on another model provider; its messages follow as text, without the tool calls it made. Continue from them."
	return append([]any{text("developer", "input_text", note)}, items...)
}

// injectCarry gives the new thread the conversation it continues, if any.
func (a *Adapter) injectCarry(ctx context.Context) error {
	if len(a.carry) == 0 {
		return nil
	}
	a.mu.Lock()
	thread := a.threadID
	a.mu.Unlock()
	_, err := a.call(ctx, "thread/inject_items", threadInjectItemsParams{ThreadID: thread, Items: carryItems(a.carry)}, nil)
	return err
}
