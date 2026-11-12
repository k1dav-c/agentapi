package codex

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/provider"
)

func TestProviderArgs(t *testing.T) {
	t.Parallel()
	p := provider.Provider{ID: "kimi", Name: `Kimi "K3"`, BaseURL: "https://gw.example.com", APIKey: "sk-1"}
	args := providerArgs(p)
	assert.Equal(t, []string{
		"-c", `model_providers.agentapi-kimi={name="Kimi \"K3\"",base_url="https://gw.example.com/v1",env_key="AGENTAPI_PROVIDER_API_KEY",wire_api="responses"}`,
		"-c", `model_provider="agentapi-kimi"`,
	}, args)
	assert.NotContains(t, strings.Join(args, " "), "sk-1", "the key goes in the environment")

	// The provider's flags follow the user's, which they override.
	a := newAdapter(Config{}, launchOptions{appServerArgs: []string{"-c", `model_provider="mine"`}}, "")
	a.launch = &native.ProviderRun{Provider: p, Model: "Kimi-K3"}
	assert.Equal(t, append([]string{"app-server", "-c", `model_provider="mine"`}, args...), a.command())
	assert.Contains(t, a.env(), "AGENTAPI_PROVIDER_API_KEY=sk-1")
	assert.Equal(t, "Kimi-K3", a.launchModel())
	// Codex's own provider: the command line's model.
	a = newAdapter(Config{}, launchOptions{model: "gpt-x"}, "")
	assert.Equal(t, []string{"app-server"}, a.command())
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
