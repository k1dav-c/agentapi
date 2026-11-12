package native

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/provider"
)

// ErrInvalidSetting is returned by ApplySettings for a value a setting
// can't take.
var ErrInvalidSetting = errors.New("not a value the setting can take")

// SettingOptions are the values the settings can take.
type SettingOptions struct {
	PermissionModes []agentstate.PermissionMode `json:"permission_modes" nullable:"false" doc:"Permission modes the agent can switch to; empty when it has none (Pi)."`
	Models          []ModelOption               `json:"models" nullable:"false" doc:"Models the agent can switch to on its own provider."`
	// Providers is empty for an agent that can't switch providers.
	Providers []ProviderOption `json:"providers,omitempty" nullable:"false" doc:"Model providers the agent can switch to besides its own (default), from ~/.agentapi/config.yaml and the hub; missing for an agent that can't switch providers (Pi)."`
}

// ProviderOption is a model provider the agent can switch to.
type ProviderOption struct {
	ID     string          `json:"id" doc:"Value of the provider setting that picks the provider."`
	Name   string          `json:"name" doc:"Name to show."`
	Host   string          `json:"host" doc:"The host of the provider's API, to show where it runs."`
	Origin provider.Origin `json:"origin" enum:"config,hub" doc:"Where the provider is defined: config, the machine's ~/.agentapi/config.yaml; hub, the hub this AgentAPI reports to."`
	Models []ModelOption   `json:"models" nullable:"false" doc:"Models the provider offers; empty when it defines none and its API listed none, then any model ID goes."`
}

// ModelOption is a model the agent can switch to.
type ModelOption struct {
	ID          string `json:"id" doc:"Value of the model setting that picks the model."`
	Name        string `json:"name" doc:"Name to show."`
	Description string `json:"description,omitempty" doc:"What the model is good at."`
	// Efforts are ordered from the least effort to the most.
	Efforts       []string `json:"efforts,omitempty" nullable:"false" doc:"Reasoning efforts the model supports, from the least to the most; empty when it has no choice of effort."`
	DefaultEffort string   `json:"default_effort,omitempty" doc:"Effort the model uses unless one is chosen."`
}

// Model returns the option with the id.
func (o SettingOptions) Model(id string) (ModelOption, bool) {
	i := slices.IndexFunc(o.Models, func(m ModelOption) bool { return m.ID == id })
	if i < 0 {
		return ModelOption{}, false
	}
	return o.Models[i], true
}

// NewModelEffort returns the effort a change of model brings, the way the
// agents do: a model that doesn't have the effort in effect (current's)
// takes its default one ("" for a model without a choice of effort). ok
// is false when the effort stays: the change chooses one, or switches to
// no model it knows, or to one that has current's effort.
func (o SettingOptions) NewModelEffort(change, current agentstate.Settings) (effort string, ok bool) {
	if change.Model == "" || change.Effort != "" {
		return "", false
	}
	m, known := o.Model(change.Model)
	if !known || slices.Contains(m.Efforts, current.Effort) {
		return "", false
	}
	return m.DefaultEffort, true
}

// Check returns an ErrInvalidSetting for a value in s that its setting
// can't take. The effort must be one of those of the model s switches to,
// or else of current's model; with a model that isn't one of the options,
// any effort passes.
func (o SettingOptions) Check(s, current agentstate.Settings) error {
	if s.PermissionMode != "" && !slices.Contains(o.PermissionModes, s.PermissionMode) {
		if len(o.PermissionModes) == 0 {
			return fmt.Errorf("%w: the agent has no permission modes", ErrInvalidSetting)
		}
		return fmt.Errorf("%w: permission mode %q isn't one of %v", ErrInvalidSetting, s.PermissionMode, o.PermissionModes)
	}
	model := current.Model
	if s.Model != "" {
		if _, ok := o.Model(s.Model); !ok {
			return fmt.Errorf("%w: model %q isn't one the agent offers", ErrInvalidSetting, s.Model)
		}
		model = s.Model
	}
	if s.Effort != "" {
		if m, ok := o.Model(model); ok && !slices.Contains(m.Efforts, s.Effort) {
			if len(m.Efforts) == 0 {
				return fmt.Errorf("%w: %s has no choice of reasoning effort", ErrInvalidSetting, m.Name)
			}
			return fmt.Errorf("%w: reasoning effort %q isn't one of %s's %v", ErrInvalidSetting, s.Effort, m.Name, m.Efforts)
		}
	}
	return nil
}

// ProviderRunner is implemented by adapters whose agent can run on a model
// provider: it starts on Resume.Provider, and switches providers.
type ProviderRunner interface {
	// SwitchProvider moves the agent to to (nil: its own provider) on
	// model ("" for the agent's default model on its own provider). A
	// *RelaunchError says the agent can't switch while it runs: the
	// conversation starts another agent on to, which continues the session
	// or starts a new one with the conversation's text.
	SwitchProvider(ctx context.Context, to *provider.Provider, model string) error
}

// RelaunchError is returned by SwitchProvider for a switch that needs
// another agent.
type RelaunchError struct {
	// Carry: the new agent starts a new session with the conversation's
	// text (Resume.Carry). Codex binds a thread to its provider, and a
	// thread holds items of its provider's own that another refuses.
	Carry bool
	// Recovery, if set, is the last known provider/model to launch instead
	// of the requested target after a switch left the running agent's state
	// unknown or couldn't roll it back.
	Recovery *agentstate.Settings
	// Cause is the switch failure a recovery relaunch must still report
	// after the previous configuration is running again.
	Cause error
}

func (e *RelaunchError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("the provider switch failed; the agent starts again on its previous provider: %v", e.Cause)
	}
	return "the agent switches providers in a new process"
}

func (e *RelaunchError) Unwrap() error { return e.Cause }

// Configurable is implemented by adapters whose session settings can
// change while the agent runs; agentstate.Capabilities.Settings reports
// it.
type Configurable interface {
	// Settings reports the current settings.
	Settings() agentstate.Settings
	// SettingOptions reports the values the settings can take. It may ask
	// the agent.
	SettingOptions(ctx context.Context) (SettingOptions, error)
	// ApplySettings changes the settings that are set in s and leaves the
	// others. A value a setting can't take is an ErrInvalidSetting. An
	// agent may apply a change from its next turn.
	ApplySettings(ctx context.Context, s agentstate.Settings) error
}

// SettingsSink is implemented by the Sink a Conversation hands to adapters.
// A Configurable adapter reports through it the settings the agent changed
// by itself, e.g. when an approved plan switched the permission mode.
type SettingsSink interface {
	SettingsChanged(agentstate.Settings)
}

// ReportSettings reports settings the agent changed by itself to sink, if
// it is a SettingsSink.
func ReportSettings(sink Sink, s agentstate.Settings) {
	if ss, ok := sink.(SettingsSink); ok {
		ss.SettingsChanged(s)
	}
}
