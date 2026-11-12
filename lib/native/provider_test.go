package native_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/provider"
)

// testProviders offers kimi, which lists its models.
func testProviders() *provider.Source {
	source := provider.NewSource("", nil)
	source.SetHub([]provider.Provider{{
		ID: "kimi", Name: "Kimi", BaseURL: "https://gw.example.com/v1", APIKey: "sk-1",
		Models: []provider.Model{{ID: "Kimi-K3"}, {ID: "Kimi-K2", Name: "Kimi K2"}},
	}})
	return source
}

// startOnProviders starts a conversation whose agents can switch providers.
func startOnProviders(t *testing.T, relaunch *native.RelaunchError, cfg native.Config) (*native.Conversation, *nativetest.Factory, *recorder) {
	t.Helper()
	factory := &nativetest.Factory{Providers: true, Configure: func(a *nativetest.Adapter) {
		configurable(agentstate.Settings{PermissionMode: agentstate.PermissionModeBypass, Model: "fast", Effort: "low"})(a)
		a.Ref = "session-1"
		a.Relaunch = relaunch
	}}
	cfg.Providers = testProviders()
	c, rec := run(t, factory, cfg)
	require.Eventually(t, func() bool { return c.Status() != conversation.StatusInitializing }, 5*time.Second, time.Millisecond)
	return c, factory, rec
}

func TestProviderOptions(t *testing.T) {
	c, _, _ := startOnProviders(t, nil, native.Config{})
	assert.Equal(t, "default", c.Settings().Provider)
	options, err := c.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, testOptions.Models, options.Models)
	assert.Equal(t, []native.ProviderOption{{
		ID: "kimi", Name: "Kimi", Origin: provider.OriginHub,
		Models: []native.ModelOption{{ID: "Kimi-K3", Name: "Kimi-K3"}, {ID: "Kimi-K2", Name: "Kimi K2"}},
	}}, options.Providers)

	// An agent that can't switch providers has none, and no provider
	// setting.
	plain, _, _ := start(t, configurable(agentstate.Settings{Model: "fast"}), native.Config{Providers: testProviders()})
	options, err = plain.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Nil(t, options.Providers)
	assert.Empty(t, plain.Settings().Provider)
	assert.ErrorIs(t, plain.ApplySettings(context.Background(), agentstate.Settings{Provider: "kimi"}), native.ErrInvalidSetting)
}

func TestSwitchProviderLive(t *testing.T) {
	c, factory, rec := startOnProviders(t, nil, native.Config{})
	a := factory.Last()
	ctx := context.Background()

	assert.ErrorIs(t, c.ApplySettings(ctx, agentstate.Settings{Provider: "nope"}), native.ErrInvalidSetting)
	assert.ErrorIs(t, c.ApplySettings(ctx, agentstate.Settings{Provider: "kimi", Model: "fast"}), native.ErrInvalidSetting, "not a model kimi offers")

	// Without a model, the provider's first.
	require.NoError(t, c.ApplySettings(ctx, agentstate.Settings{Provider: "kimi"}))
	assert.Equal(t, []nativetest.Switch{{Provider: "kimi", Model: "Kimi-K3"}}, a.Switches())
	assert.Equal(t, agentstate.Settings{PermissionMode: agentstate.PermissionModeBypass, Model: "Kimi-K3", Effort: "low", Provider: "kimi"}, c.Settings())
	chosen := rec.get().chosen
	assert.Equal(t, agentstate.Settings{Provider: "kimi", Model: "Kimi-K3"}, chosen[len(chosen)-1])

	// On the provider, its models are the choices.
	require.NoError(t, c.ApplySettings(ctx, agentstate.Settings{Model: "Kimi-K2"}))
	assert.Equal(t, "Kimi-K2", c.Settings().Model)
	assert.ErrorIs(t, c.ApplySettings(ctx, agentstate.Settings{Model: "plain"}), native.ErrInvalidSetting)

	// Back on its own provider, the agent goes back to the model it had.
	require.NoError(t, c.ApplySettings(ctx, agentstate.Settings{Provider: "default"}))
	assert.Equal(t, nativetest.Switch{Model: "fast"}, a.Switches()[1])
	assert.Equal(t, "default", c.Settings().Provider)
	assert.Equal(t, "fast", c.Settings().Model)

	// A switch with other settings applies them on the new provider.
	require.NoError(t, c.ApplySettings(ctx, agentstate.Settings{Provider: "kimi", PermissionMode: agentstate.PermissionModeAsk}))
	assert.Equal(t, agentstate.PermissionModeAsk, c.Settings().PermissionMode)
	assert.Equal(t, "kimi", c.Settings().Provider)
}

func TestSwitchProviderRelaunch(t *testing.T) {
	messages := []conversation.Message{
		{Id: 0, Role: conversation.RoleUser, Message: "remember 7"},
		{Id: 1, Role: conversation.RoleAgent, Message: "ok"},
	}
	c, factory, _ := startOnProviders(t, &native.RelaunchError{Carry: true}, native.Config{ResumeRef: "session-1", InitialMessages: messages})
	ctx := context.Background()

	err := c.ApplySettings(ctx, agentstate.Settings{Provider: "kimi", Model: "Kimi-K2"})
	require.ErrorIs(t, err, native.ErrProviderRelaunch)
	assert.Equal(t, "default", c.Settings().Provider, "nothing changed yet")

	r, err := c.BeginProviderSwitch(ctx, agentstate.Settings{Provider: "kimi", Model: "Kimi-K2"}, true)
	require.NoError(t, err)
	require.NoError(t, r.Start(ctx))
	a := factory.Last()
	require.Len(t, factory.Adapters(), 2)
	// A new session on the provider, with the conversation's text; the
	// conversation stays.
	require.NotNil(t, a.Launch)
	assert.Equal(t, "kimi", a.Launch.Provider.ID)
	assert.Equal(t, "sk-1", a.Launch.Provider.APIKey)
	assert.Equal(t, "Kimi-K2", a.Launch.Model)
	assert.Empty(t, a.ResumeRef)
	assert.Equal(t, messages, a.Carry)
	assert.Equal(t, messages, c.Messages())
	assert.Equal(t, "kimi", c.Settings().Provider)

	// A restart starts on the provider again, in its own session.
	require.Eventually(t, func() bool { return c.SessionRef() == "session-1" }, 5*time.Second, time.Millisecond)
	require.NoError(t, c.Restart(ctx))
	a = factory.Last()
	require.NotNil(t, a.Launch)
	assert.Equal(t, "kimi", a.Launch.Provider.ID)
}

func TestSwitchProviderRelaunchResumes(t *testing.T) {
	messages := []conversation.Message{{Id: 0, Role: conversation.RoleUser, Message: "hi"}}
	c, factory, _ := startOnProviders(t, &native.RelaunchError{}, native.Config{ResumeRef: "session-1", InitialMessages: messages})
	r, err := c.BeginProviderSwitch(context.Background(), agentstate.Settings{Provider: "kimi"}, false)
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))
	a := factory.Last()
	assert.Equal(t, "session-1", a.ResumeRef, "the same session goes on")
	assert.Empty(t, a.Carry)
	assert.Equal(t, "Kimi-K3", a.Launch.Model)
}

func TestChosenProviderNoLongerDefined(t *testing.T) {
	source := testProviders()
	factory := &nativetest.Factory{Providers: true, Configure: configurable(agentstate.Settings{Model: "fast"})}
	c, rec := run(t, factory, native.Config{
		Providers: source,
		State:     agentstate.StoredState{Settings: agentstate.Settings{Provider: "kimi", Model: "Kimi-K3"}},
	})
	require.Eventually(t, func() bool { return c.Status() != conversation.StatusInitializing }, 5*time.Second, time.Millisecond)
	require.NotNil(t, factory.Last().Launch, "the restored provider")
	assert.Equal(t, "kimi", c.Settings().Provider)

	source.SetHub(nil)
	require.NoError(t, c.Restart(context.Background()))
	assert.Nil(t, factory.Last().Launch)
	assert.Equal(t, "default", c.Settings().Provider)
	assert.Contains(t, rec.get().errors, "The model provider kimi is no longer defined; the agent starts on its own provider.")
}
