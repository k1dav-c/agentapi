package codex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/native/procutil"
	"github.com/coder/agentapi/lib/provider"
)

func TestProviderArgs(t *testing.T) {
	t.Parallel()
	p := provider.Provider{ID: "kimi", Name: `Kimi "K3"`, BaseURL: "https://gw.example.com", APIKey: "sk-1"}
	endpoint, err := nativetest.ProviderProxy{}.Endpoint(p.ID)
	require.NoError(t, err)
	args := providerArgs(p, endpoint)
	assert.Equal(t, []string{
		"-c", `model_providers.agentapi-kimi={name="Kimi \"K3\"",base_url="http://127.0.0.1:1/kimi/openai",env_key="AGENTAPI_PROVIDER_API_KEY",wire_api="responses"}`,
		"-c", `model_provider="agentapi-kimi"`,
	}, args)

	// The provider's flags follow the user's, which they override; the
	// proxy's token is in the environment, and the key nowhere.
	a := newAdapter(Config{ProviderProxy: nativetest.ProviderProxy{}}, launchOptions{appServerArgs: []string{"-c", `model_provider="mine"`}}, "")
	a.launch = &native.ProviderRun{Provider: p, Model: "Kimi-K3"}
	launch, err := a.launchEndpoint()
	require.NoError(t, err)
	command, env := a.command(launch), a.env(launch)
	assert.Equal(t, append([]string{"app-server", "-c", `model_provider="mine"`}, args...), command)
	assert.Contains(t, env, "AGENTAPI_PROVIDER_API_KEY="+nativetest.ProxyToken)
	for _, s := range append(command, env...) {
		assert.NotContains(t, s, "sk-1", "the key stays in AgentAPI")
		assert.NotContains(t, s, "gw.example.com")
	}
	assert.Equal(t, "Kimi-K3", a.launchModel())

	// Without a proxy, or one that can't start, no provider.
	a.cfg.ProviderProxy = nil
	_, err = a.launchEndpoint()
	assert.ErrorContains(t, err, "no provider proxy")
	a.cfg.ProviderProxy = nativetest.ProviderProxy{Err: errors.New("no port")}
	_, err = a.launchEndpoint()
	assert.ErrorContains(t, err, `can't run on Kimi "K3": no port`)
	started := false
	a.cfg.StartProcess = func(procutil.Command) (procutil.Process, error) { started = true; return nil, errors.New("unexpected") }
	assert.ErrorContains(t, a.Start(context.Background(), nativetest.NewSink(t)), "no port")
	assert.False(t, started)

	// Codex's own provider: the command line's model.
	a = newAdapter(Config{}, launchOptions{model: "gpt-x"}, "")
	launch, err = a.launchEndpoint()
	require.NoError(t, err)
	assert.Nil(t, launch)
	assert.Equal(t, []string{"app-server"}, a.command(launch))
	assert.NotContains(t, strings.Join(a.env(launch), "\n"), "AGENTAPI_PROVIDER_API_KEY")
	assert.Equal(t, "gpt-x", a.launchModel())
}

func TestCarryItems(t *testing.T) {
	t.Parallel()
	items := carryItems([]conv.Message{
		{Role: conv.RoleUser, Message: "remember 7"},
		{Role: conv.RoleAgent, Message: ""},
		{Role: conv.RoleAgent, Message: "ok"},
	})
	assert.Len(t, items, 3, "a note, then the messages with text")
	assert.Equal(t, "developer", items[0].(map[string]any)["role"])
	assert.Equal(t, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "remember 7"}}}, items[1])
	assert.Equal(t, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}, items[2])

	// The latest messages that fit.
	long := strings.Repeat("x", maxCarry-4)
	items = carryItems([]conv.Message{{Role: conv.RoleUser, Message: "old"}, {Role: conv.RoleUser, Message: long}, {Role: conv.RoleAgent, Message: "new"}})
	assert.Len(t, items, 3)
	assert.Equal(t, "new", items[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"])
}
