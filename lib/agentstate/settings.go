package agentstate

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

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
	// Provider is empty for an agent that can't run on another provider.
	Provider string `json:"provider,omitempty" doc:"Model provider the agent runs on: default for its own (Claude Code's Anthropic login or key, Codex's OpenAI login or config), else the id of one of the providers GET /settings lists. Missing for an agent that can't switch providers (Pi)."`
}

// ProviderDefault is the Provider setting of an agent on its own provider.
const ProviderDefault = "default"

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
	if o.Provider != "" {
		s.Provider = o.Provider
	}
	return s
}
