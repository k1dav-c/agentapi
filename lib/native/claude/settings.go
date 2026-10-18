package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/coder/agentapi/lib/native"
)

// Claude Code's permission modes that AgentAPI's map to. Its others
// (acceptEdits, dontAsk) are reported as a mode of its own.
const (
	modeAuto = "auto"
	modePlan = "plan"
	// modeManual is what newer versions call default; they report it as
	// default.
	modeManual = "manual"
)

// claudeModel is a model of the initialize response.
type claudeModel struct {
	Value            string   `json:"value"`
	ResolvedModel    string   `json:"resolvedModel"`
	DisplayName      string   `json:"displayName"`
	Description      string   `json:"description"`
	SupportsEffort   bool     `json:"supportsEffort"`
	EffortLevels     []string `json:"supportedEffortLevels"`
	SupportsAutoMode *bool    `json:"supportsAutoMode"`
}

// initializeInfo is what the adapter reads from the initialize response.
type initializeInfo struct {
	Version        string        `json:"claude_code_version"`
	PermissionMode string        `json:"current_permission_mode"`
	Models         []claudeModel `json:"models"`
}

// permissionMode maps Claude Code's permission mode to AgentAPI's, "" for
// one of its own.
func permissionMode(mode string) native.PermissionMode {
	switch mode {
	case modeBypass:
		return native.PermissionModeBypass
	case modeAuto:
		return native.PermissionModeAuto
	case modeDefault, modeManual:
		return native.PermissionModeAsk
	case modePlan:
		return native.PermissionModePlan
	}
	return ""
}

// claudeMode maps AgentAPI's permission mode to Claude Code's.
func claudeMode(mode native.PermissionMode) string {
	switch mode {
	case native.PermissionModeBypass:
		return modeBypass
	case native.PermissionModeAuto:
		return modeAuto
	case native.PermissionModeAsk:
		return modeDefault
	case native.PermissionModePlan:
		return modePlan
	}
	return ""
}

// readSettings records the settings Claude Code started with: the
// permission mode and models from the initialize response, and the model
// and effort it applies, which get_settings reports.
func (a *Adapter) readSettings(ctx context.Context, info initializeInfo) {
	var applied struct {
		Applied struct {
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"applied"`
	}
	response, err := a.request(ctx, "get_settings", nil, nil)
	if err == nil {
		err = json.Unmarshal(response, &applied)
	}
	if err != nil {
		// Older versions don't have it; the model and effort stay unknown.
		a.log.Debug("Claude Code didn't report its settings", "error", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mode = info.PermissionMode
	a.models = info.Models
	a.model = a.modelIDLocked(applied.Applied.Model, a.args.model)
	a.effort = applied.Applied.Effort
}

// modelIDLocked returns the value of the model option for the model Claude
// Code runs (its full name), preferring the one the user chose with
// --model, then the default; the full name if no option has it.
func (a *Adapter) modelIDLocked(model, chosen string) string {
	if model == "" {
		return ""
	}
	matches := func(m claudeModel) bool { return m.ResolvedModel == model || m.Value == model }
	for _, preferred := range []string{chosen, "default"} {
		if i := slices.IndexFunc(a.models, func(m claudeModel) bool { return m.Value == preferred }); i >= 0 && matches(a.models[i]) {
			return preferred
		}
	}
	if i := slices.IndexFunc(a.models, matches); i >= 0 {
		return a.models[i].Value
	}
	return model
}

// modelRan records the model a turn runs on (a system init frame): the
// model setting of a CLI that didn't report one at the start, or another
// model Claude Code switched to (a fallback). It reports whether the
// settings changed.
func (a *Adapter) modelRan(model string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if model == "" || a.model == model {
		return false
	}
	if i := slices.IndexFunc(a.models, func(m claudeModel) bool { return m.Value == a.model }); i >= 0 && a.models[i].ResolvedModel == model {
		return false
	}
	id := a.modelIDLocked(model, a.args.model)
	if id == a.model {
		return false
	}
	a.model = id
	return true
}

// modeChanged records the permission mode Claude Code reports (a status
// frame, e.g. after an approved plan). It reports whether it changed.
func (a *Adapter) modeChanged(mode string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if mode == "" || mode == a.mode {
		return false
	}
	a.mode = mode
	return true
}

// reportSettings tells the conversation about settings Claude Code changed
// by itself.
func (a *Adapter) reportSettings() {
	native.ReportSettings(a.sink, a.Settings())
}

// Settings implements native.Configurable.
func (a *Adapter) Settings() native.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return native.Settings{PermissionMode: permissionMode(a.mode), Model: a.model, Effort: a.effort}
}

// SettingOptions implements native.Configurable. The modes are those
// Claude Code can switch to: bypassPermissions only where it allows it,
// auto for models that support it.
func (a *Adapter) SettingOptions(context.Context) (native.SettingOptions, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var options native.SettingOptions
	if a.args.allowBypass {
		options.PermissionModes = append(options.PermissionModes, native.PermissionModeBypass)
	}
	i := slices.IndexFunc(a.models, func(m claudeModel) bool { return m.Value == a.model })
	if i < 0 || a.models[i].SupportsAutoMode == nil || *a.models[i].SupportsAutoMode {
		options.PermissionModes = append(options.PermissionModes, native.PermissionModeAuto)
	}
	options.PermissionModes = append(options.PermissionModes, native.PermissionModeAsk, native.PermissionModePlan)
	for _, m := range a.models {
		option := native.ModelOption{ID: m.Value, Name: m.DisplayName, Description: m.Description}
		if m.SupportsEffort {
			option.Efforts = slices.Clone(m.EffortLevels)
		}
		options.Models = append(options.Models, option)
	}
	return options, nil
}

// ApplySettings implements native.Configurable. Each setting is a control
// request, so a change applies at once, also during a turn.
func (a *Adapter) ApplySettings(ctx context.Context, s native.Settings) error {
	options, _ := a.SettingOptions(ctx)
	current := a.Settings()
	if err := options.Check(s, current); err != nil {
		return err
	}
	if s.Model != "" && s.Model != current.Model {
		if _, err := a.request(ctx, "set_model", map[string]any{"model": s.Model}, nil); err != nil {
			return fmt.Errorf("switch the model: %w", err)
		}
		a.mu.Lock()
		a.model = s.Model
		a.mu.Unlock()
	}
	if s.Effort != "" && s.Effort != current.Effort {
		settings := map[string]any{"effortLevel": s.Effort}
		if _, err := a.request(ctx, "apply_flag_settings", map[string]any{"settings": settings}, nil); err != nil {
			return fmt.Errorf("change the reasoning effort: %w", err)
		}
		a.mu.Lock()
		a.effort = s.Effort
		a.mu.Unlock()
	}
	if s.PermissionMode != "" && s.PermissionMode != current.PermissionMode {
		mode := claudeMode(s.PermissionMode)
		response, err := a.request(ctx, "set_permission_mode", map[string]any{"mode": mode}, nil)
		if err != nil {
			return fmt.Errorf("switch the permission mode: %w", err)
		}
		// A mode it refuses (bypassPermissions in a session that doesn't
		// allow it) is answered without the mode it switched to.
		var switched struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(response, &switched)
		if permissionMode(switched.Mode) != s.PermissionMode {
			return fmt.Errorf("claude refused the %s permission mode", mode)
		}
		a.modeChanged(switched.Mode)
	}
	return nil
}
