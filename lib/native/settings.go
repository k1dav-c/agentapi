package native

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

// ErrInvalidSetting is returned by ApplySettings for a value a setting
// can't take.
var ErrInvalidSetting = errors.New("not a value the setting can take")

// PermissionMode is how freely the agent uses its tools.
type PermissionMode string

const (
	// PermissionModeBypass: tools run without asking. The default.
	PermissionModeBypass PermissionMode = "bypass"
	// PermissionModeAuto: the agent's own review decides what may run
	// (Claude Code's auto mode; Codex works in its sandbox and its auto
	// review decides on what needs more).
	PermissionModeAuto PermissionMode = "auto"
	// PermissionModeAsk: tool calls that need approval come as requests.
	PermissionModeAsk PermissionMode = "ask"
	// PermissionModePlan: the agent looks around and proposes a plan, which
	// comes as a plan request, before it changes anything.
	PermissionModePlan PermissionMode = "plan"
)

var PermissionModeValues = []PermissionMode{PermissionModeBypass, PermissionModeAuto, PermissionModeAsk, PermissionModePlan}

func (m PermissionMode) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "PermissionMode", PermissionModeValues)
}

// Settings are the settings of the agent's session that can change while it
// runs. An empty field is one the agent doesn't report or, in a change, one
// to leave as it is.
type Settings struct {
	// PermissionMode is empty while the agent runs in a mode of its own,
	// e.g. Claude Code's acceptEdits.
	PermissionMode PermissionMode `json:"permission_mode,omitempty" doc:"bypass: tools run without asking; auto: the agent's own review decides what may run; ask: tool calls that need approval come as requests; plan: the agent proposes a plan before it changes anything. Missing while the agent runs in a mode of its own, e.g. Claude Code's acceptEdits."`
	Model          string         `json:"model,omitempty" doc:"Model: the id of one of the models GET /settings lists."`
	Effort         string         `json:"effort,omitempty" doc:"Reasoning effort: one of the efforts GET /settings lists for the model, e.g. low, medium or high."`
}

// IsZero reports whether no setting is set.
func (s Settings) IsZero() bool { return s == Settings{} }

// Merge returns s with the fields that are set in o replaced.
func (s Settings) Merge(o Settings) Settings {
	if o.PermissionMode != "" {
		s.PermissionMode = o.PermissionMode
	}
	if o.Model != "" {
		s.Model = o.Model
	}
	if o.Effort != "" {
		s.Effort = o.Effort
	}
	return s
}

// SettingOptions are the values the settings can take.
type SettingOptions struct {
	PermissionModes []PermissionMode `json:"permission_modes" doc:"Permission modes the agent can switch to; empty when it has none (Pi)."`
	Models          []ModelOption    `json:"models" doc:"Models the agent can switch to."`
}

// ModelOption is a model the agent can switch to.
type ModelOption struct {
	ID          string `json:"id" doc:"Value of the model setting that picks the model."`
	Name        string `json:"name" doc:"Name to show."`
	Description string `json:"description,omitempty" doc:"What the model is good at."`
	// Efforts are ordered from the least effort to the most.
	Efforts       []string `json:"efforts,omitempty" doc:"Reasoning efforts the model supports, from the least to the most; empty when it has no choice of effort."`
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

// Check returns an ErrInvalidSetting for a value in s that its setting
// can't take. The effort must be one of those of the model s switches to,
// or else of current's model; with a model that isn't one of the options,
// any effort passes.
func (o SettingOptions) Check(s, current Settings) error {
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

// Configurable is implemented by adapters whose session settings can
// change while the agent runs; Capabilities.Settings reports it.
type Configurable interface {
	// Settings reports the current settings.
	Settings() Settings
	// SettingOptions reports the values the settings can take. It may ask
	// the agent.
	SettingOptions(ctx context.Context) (SettingOptions, error)
	// ApplySettings changes the settings that are set in s and leaves the
	// others. A value a setting can't take is an ErrInvalidSetting. An
	// agent may apply a change from its next turn.
	ApplySettings(ctx context.Context, s Settings) error
}

// SettingsSink is implemented by the Sink a Conversation hands to adapters.
// A Configurable adapter reports through it the settings the agent changed
// by itself, e.g. when an approved plan switched the permission mode.
type SettingsSink interface {
	SettingsChanged(Settings)
}

// ReportSettings reports settings the agent changed by itself to sink, if
// it is a SettingsSink.
func ReportSettings(sink Sink, s Settings) {
	if ss, ok := sink.(SettingsSink); ok {
		ss.SettingsChanged(s)
	}
}
