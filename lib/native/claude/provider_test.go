package claude

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestSwitchProviderFailureRecovery(t *testing.T) {
	target := provider.Provider{ID: "target", Name: "Target", BaseURL: "https://target.example.com/v1", APIKey: "sk-target"}
	from := provider.Provider{ID: "old", Name: "Old", BaseURL: "https://old.example.com/v1", APIKey: "sk-old"}

	t.Run("default model refusal relaunches default", func(t *testing.T) {
		a, _, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
		done := switchProviderAsync(a, &target, "target-model")
		env, i := proc.waitRequest(t, 1, "apply_flag_settings")
		proc.answer(t, env, `{}`)
		model, _ := proc.waitRequest(t, i+1, "set_model")
		refuseControl(t, proc, model, "model refused")
		assertProviderRecovery(t, <-done, provider.Default, "default")
	})

	t.Run("third-party rollback restores environment", func(t *testing.T) {
		a, _, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
		a.provider, a.model = &from, "old-model"
		done := switchProviderAsync(a, &target, "target-model")
		env, i := proc.waitRequest(t, 1, "apply_flag_settings")
		proc.answer(t, env, `{}`)
		model, i := proc.waitRequest(t, i+1, "set_model")
		refuseControl(t, proc, model, "model refused")
		rollback, _ := proc.waitRequest(t, i+1, "apply_flag_settings")
		env = rollback["request"].(map[string]any)["settings"].(map[string]any)["env"].(map[string]any)
		assert.Equal(t, "https://old.example.com", env["ANTHROPIC_BASE_URL"])
		assert.Equal(t, "sk-old", env["ANTHROPIC_AUTH_TOKEN"])
		assert.Equal(t, "old-model", env["ANTHROPIC_DEFAULT_OPUS_MODEL"])
		proc.answer(t, rollback, `{}`)
		err := <-done
		assert.ErrorContains(t, err, "model refused")
		var relaunch *native.RelaunchError
		assert.False(t, errors.As(err, &relaunch), "a definite model refusal and rollback need no new process")
		got, ok := a.onProvider()
		require.True(t, ok)
		assert.Equal(t, from, got)
	})

	t.Run("rollback failure relaunches third-party", func(t *testing.T) {
		a, _, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
		a.provider, a.model = &from, "old-model"
		done := switchProviderAsync(a, &target, "target-model")
		env, i := proc.waitRequest(t, 1, "apply_flag_settings")
		proc.answer(t, env, `{}`)
		model, i := proc.waitRequest(t, i+1, "set_model")
		refuseControl(t, proc, model, "model refused")
		rollback, _ := proc.waitRequest(t, i+1, "apply_flag_settings")
		refuseControl(t, proc, rollback, "rollback refused")
		assertProviderRecovery(t, <-done, "old", "old-model")
	})

	for _, control := range []string{"apply_flag_settings", "set_model"} {
		t.Run("unconfirmed "+control+" relaunches third-party", func(t *testing.T) {
			a, _, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
			a.provider, a.model = &from, "old-model"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- a.SwitchProvider(ctx, &target, "target-model") }()
			env, i := proc.waitRequest(t, 1, "apply_flag_settings")
			if control == "set_model" {
				proc.answer(t, env, `{}`)
				_, i = proc.waitRequest(t, i+1, "set_model")
			}
			_ = i
			cancel()
			err := <-done
			assert.ErrorIs(t, err, native.ErrUnconfirmed)
			assertProviderRecovery(t, err, "old", "old-model")
		})
	}
}

func switchProviderAsync(a *Adapter, to *provider.Provider, model string) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.SwitchProvider(context.Background(), to, model) }()
	return done
}

func refuseControl(t *testing.T, proc *fakeProcess, request map[string]any, message string) {
	t.Helper()
	proc.send(t, []byte(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":%q}}`, request["request_id"], message)))
}

func assertProviderRecovery(t *testing.T, err error, providerID, model string) {
	t.Helper()
	require.Error(t, err)
	var relaunch *native.RelaunchError
	require.ErrorAs(t, err, &relaunch)
	require.NotNil(t, relaunch.Recovery)
	assert.Equal(t, providerID, relaunch.Recovery.Provider)
	assert.Equal(t, model, relaunch.Recovery.Model)
	assert.ErrorContains(t, err, relaunch.Cause.Error())
}
