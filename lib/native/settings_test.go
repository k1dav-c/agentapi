package native_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	st "github.com/coder/agentapi/lib/screentracker"
)

var testOptions = native.SettingOptions{
	PermissionModes: []native.PermissionMode{native.PermissionModeBypass, native.PermissionModeAsk},
	Models: []native.ModelOption{
		{ID: "fast", Name: "Fast", Efforts: []string{"low", "high"}, DefaultEffort: "low"},
		{ID: "plain", Name: "Plain"},
	},
}

// configurable makes the fake adapters configurable, starting with current.
func configurable(current native.Settings) func(*nativetest.Adapter) {
	return func(a *nativetest.Adapter) {
		a.Caps.Settings = true
		a.Options = testOptions
		a.Current = current
	}
}

func TestSettingOptionsCheck(t *testing.T) {
	t.Parallel()
	current := native.Settings{Model: "fast"}
	for _, tc := range []struct {
		name string
		s    native.Settings
		ok   bool
	}{
		{"nothing", native.Settings{}, true},
		{"mode", native.Settings{PermissionMode: native.PermissionModeAsk}, true},
		{"mode it lacks", native.Settings{PermissionMode: native.PermissionModeAuto}, false},
		{"model", native.Settings{Model: "plain"}, true},
		{"unknown model", native.Settings{Model: "huge"}, false},
		{"effort of the current model", native.Settings{Effort: "high"}, true},
		{"effort the current model lacks", native.Settings{Effort: "max"}, false},
		{"effort of a model without efforts", native.Settings{Model: "plain", Effort: "low"}, false},
		{"effort with a new model", native.Settings{Model: "fast", Effort: "low"}, true},
	} {
		err := testOptions.Check(tc.s, current)
		if tc.ok {
			assert.NoError(t, err, tc.name)
		} else {
			assert.ErrorIs(t, err, native.ErrInvalidSetting, tc.name)
		}
	}
	// A model that isn't one of the options (one the command line chose)
	// takes any effort; the agent decides.
	assert.NoError(t, testOptions.Check(native.Settings{Effort: "turbo"}, native.Settings{Model: "custom"}))
	assert.ErrorIs(t, native.SettingOptions{}.Check(native.Settings{PermissionMode: native.PermissionModeAsk}, current), native.ErrInvalidSetting)
}

func TestApplySettings(t *testing.T) {
	c, factory, rec := start(t, configurable(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"}), native.Config{})
	a := factory.Last()
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"}, c.Settings())
	options, err := c.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, testOptions, options)

	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk, Effort: "high"}))
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "fast", Effort: "high"}, c.Settings())
	assert.Equal(t, []native.Settings{{PermissionMode: native.PermissionModeAsk, Effort: "high"}}, a.Applied())
	got := rec.get()
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "fast", Effort: "high"}, got.settings[len(got.settings)-1])
	// Only what was chosen is kept for the next agent, not the agent's own
	// defaults.
	assert.Equal(t, []native.Settings{{PermissionMode: native.PermissionModeAsk, Effort: "high"}}, got.chosen)

	err = c.ApplySettings(context.Background(), native.Settings{Model: "huge"})
	assert.ErrorIs(t, err, native.ErrInvalidSetting)
	assert.Len(t, rec.get().chosen, 1, "a refused setting isn't chosen")
	assert.Equal(t, "fast", c.Settings().Model)

	// A model without the effort chosen takes an effort of its own, which
	// the next agent gets too.
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{Model: "plain"}))
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "plain"}, c.Settings())
	got = rec.get()
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "plain"}, got.chosen[len(got.chosen)-1])

	// A change that failed partway shows what the agent took, but isn't
	// chosen.
	a.SettingsErr = errors.New("no such effort")
	a.Current = native.Settings{PermissionMode: native.PermissionModeAsk, Model: "fast", Effort: "low"}
	assert.Error(t, c.ApplySettings(context.Background(), native.Settings{Model: "fast", Effort: "max"}))
	assert.Equal(t, "fast", c.Settings().Model)
	assert.Equal(t, "plain", rec.get().chosen[len(rec.get().chosen)-1].Model)
}

func TestSettingsNeedTheCapability(t *testing.T) {
	c, _, _ := start(t, nil, native.Config{})
	assert.ErrorIs(t, c.ApplySettings(context.Background(), native.Settings{Model: "fast"}), native.ErrUnsupported)
	_, err := c.SettingOptions(context.Background())
	assert.ErrorIs(t, err, native.ErrUnsupported)
	assert.Equal(t, native.Settings{}, c.Settings())
}

func TestSettingsWhileNoAgentRuns(t *testing.T) {
	factory := &nativetest.Factory{Configure: func(a *nativetest.Adapter) {
		configurable(native.Settings{Model: "fast"})(a)
		a.StartBlock = make(chan struct{})
	}}
	c, _ := run(t, factory, native.Config{})
	require.Eventually(t, inStart(factory, ""), 5*time.Second, time.Millisecond)
	assert.ErrorIs(t, c.ApplySettings(context.Background(), native.Settings{Model: "plain"}), native.ErrNotStarted)
	_, err := c.SettingOptions(context.Background())
	assert.ErrorIs(t, err, native.ErrNotStarted)
}

func TestChosenSettingsOutliveTheAgent(t *testing.T) {
	c, factory, _ := start(t, configurable(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"}), native.Config{})
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{Model: "fast", Effort: "high"}))
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk}))

	require.NoError(t, c.Restart(context.Background()))
	a := factory.Last()
	require.Len(t, factory.Adapters(), 2)
	// The model was the new agent's already; the effort, then the mode, are
	// applied one at a time.
	assert.Equal(t, []native.Settings{{Effort: "high"}, {PermissionMode: native.PermissionModeAsk}}, a.Applied())
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "fast", Effort: "high"}, c.Settings())
}

func TestRestoredSettingsAreApplied(t *testing.T) {
	c, factory, rec := start(t, configurable(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"}),
		native.Config{Settings: native.Settings{Model: "plain", PermissionMode: native.PermissionModeAsk}})
	assert.Equal(t, []native.Settings{{Model: "plain"}, {PermissionMode: native.PermissionModeAsk}}, factory.Last().Applied())
	// The model has no efforts.
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "plain"}, c.Settings())
	assert.Empty(t, rec.get().chosen, "restoring doesn't change the choice")
}

func TestRestoredSettingTheAgentRefuses(t *testing.T) {
	c, factory, rec := start(t, configurable(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast"}),
		native.Config{Settings: native.Settings{Model: "gone", PermissionMode: native.PermissionModeAsk}})
	// The model is gone; the mode is still applied.
	assert.Equal(t, []native.Settings{{PermissionMode: native.PermissionModeAsk}}, factory.Last().Applied())
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "fast"}, c.Settings())
	errs := rec.get().errors
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "model gone")
}

func TestAgentChangesItsSettings(t *testing.T) {
	c, factory, rec := start(t, configurable(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"}), native.Config{})
	a := factory.Last()
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk}))

	// An approved plan switched the agent to a mode of its own: the next
	// agent gets the chosen mode again, not its default, which may let it
	// do more.
	a.ChangeSettings(native.Settings{Model: "fast", Effort: "low"})
	assert.Equal(t, native.Settings{Model: "fast", Effort: "low"}, c.Settings())
	assert.Len(t, rec.get().chosen, 1)

	// A mode of AgentAPI's the agent switched to is followed.
	a.ChangeSettings(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "fast", Effort: "low"})
	got := rec.get()
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeBypass}, got.chosen[len(got.chosen)-1])

	// A setting nobody chose isn't recorded as chosen when the agent
	// changes it.
	a.ChangeSettings(native.Settings{PermissionMode: native.PermissionModeBypass, Model: "plain"})
	assert.Len(t, rec.get().chosen, 2)
}

func TestApplySettingsFailure(t *testing.T) {
	boom := errors.New("boom")
	c, _, rec := start(t, func(a *nativetest.Adapter) {
		configurable(native.Settings{Model: "fast"})(a)
		a.SettingsErr = boom
	}, native.Config{})
	assert.ErrorIs(t, c.ApplySettings(context.Background(), native.Settings{Model: "plain"}), boom)
	assert.Empty(t, rec.get().chosen)
	assert.Equal(t, "fast", c.Settings().Model)
}

func TestSettingsLearnedWithTheOptions(t *testing.T) {
	c, _, rec := start(t, func(a *nativetest.Adapter) {
		configurable(native.Settings{Model: "fast"})(a)
		a.Learned = native.Settings{Effort: "low"}
	}, native.Config{})
	assert.Empty(t, c.Settings().Effort)
	_, err := c.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, native.Settings{Model: "fast", Effort: "low"}, c.Settings())
	got := rec.get()
	assert.Equal(t, native.Settings{Model: "fast", Effort: "low"}, got.settings[len(got.settings)-1])
	assert.Empty(t, got.chosen, "nobody chose it")
}

func TestUsageOutlivesTheSessionButNotItsContext(t *testing.T) {
	c, factory, rec := start(t, nil, native.Config{})
	percent := 12.0
	usage := native.AgentUsage{
		Context:    &native.ContextUsage{Used: 1000, Size: 200000},
		RateLimits: []native.RateLimit{{ID: "five_hour", Label: "5-hour limit", UsedPercent: &percent}},
	}
	factory.Last().Sink().(native.UsageSink).UsageChanged(usage)
	assert.Equal(t, usage, c.Usage())
	assert.Equal(t, []native.AgentUsage{usage}, rec.get().usage)

	// A new session has an empty context; the account's limits stay.
	require.NoError(t, c.Restart(context.Background()))
	assert.Nil(t, c.Usage().Context)
	assert.Equal(t, usage.RateLimits, c.Usage().RateLimits)
	got := rec.get().usage
	assert.Nil(t, got[len(got)-1].Context)
}

func TestCompactNeedsTheCapabilityAndNoTurn(t *testing.T) {
	c, _, _ := start(t, nil, native.Config{})
	assert.ErrorIs(t, c.Compact(context.Background()), native.ErrUnsupported, "an agent without the capability")
}

func TestCompactHoldsTurns(t *testing.T) {
	compacting, release := make(chan struct{}, 2), make(chan struct{})
	c, factory, _ := start(t, func(a *nativetest.Adapter) {
		a.Caps.Compact = true
		a.Caps.Resume = true
		a.OnCompact = func(context.Context) error {
			compacting <- struct{}{}
			<-release
			return nil
		}
	}, native.Config{})
	compact := func() <-chan error {
		done := make(chan error, 1)
		go func() { done <- c.Compact(context.Background()) }()
		<-compacting
		return done
	}
	done := compact()
	assert.False(t, c.Idle())
	assert.Equal(t, st.ConversationStatusChanging, c.Status())
	assert.ErrorIs(t, c.StartTurn(context.Background(), native.InputFromText("hi")), st.ErrMessageValidationChanging)
	assert.ErrorIs(t, c.Compact(context.Background()), native.ErrTurnActive, "one at a time")
	assert.ErrorIs(t, c.Reload(context.Background()), native.ErrTurnActive)
	release <- struct{}{}
	require.NoError(t, <-done)
	assert.True(t, c.Idle())
	assert.Equal(t, st.ConversationStatusStable, c.Status())

	// A restart doesn't wait for the old agent's compaction, which then
	// can't hold the new one's turns.
	done = compact()
	require.NoError(t, c.Restart(context.Background()))
	assert.True(t, c.Idle())
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, c.StartTurn(context.Background(), native.InputFromText("hi")))
	assert.Len(t, factory.Last().Turns(), 1)
	assert.ErrorIs(t, c.Compact(context.Background()), native.ErrTurnActive, "not during a turn")
}
