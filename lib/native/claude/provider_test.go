package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/provider"
)

func TestProviderEnv(t *testing.T) {
	t.Parallel()
	p := provider.Provider{ID: "kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1"}
	env := providerEnv(p, "Kimi-K3")
	assert.Equal(t, "https://gw.example.com", env["ANTHROPIC_BASE_URL"], "without /v1")
	assert.Equal(t, "sk-1", env["ANTHROPIC_AUTH_TOKEN"])
	// The user's Anthropic key doesn't go to the provider.
	assert.Equal(t, "", env["ANTHROPIC_API_KEY"])
	for _, name := range providerModelEnv {
		assert.Equal(t, "Kimi-K3", env[name], name)
	}
	assert.NotContains(t, env, "ANTHROPIC_MODEL", "set_model switches a running agent")

	launch := launchEnv(&native.ProviderRun{Provider: p, Model: "Kimi-K3"})
	assert.Contains(t, launch, "ANTHROPIC_MODEL=Kimi-K3")
	assert.Contains(t, launch, "ANTHROPIC_API_KEY=")
	assert.Nil(t, launchEnv(nil))

	// The provider's variables come last, over the user's.
	built := buildEnv([]string{"ANTHROPIC_API_KEY=sk-ant-mine", "HOME=/home/u"}, launch...)
	assert.Equal(t, "ANTHROPIC_API_KEY=sk-ant-mine", built[0])
	assert.Contains(t, built[2:], "ANTHROPIC_API_KEY=")
}
