package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// thinkingLevels are Pi's thinking levels, from the least to the most.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// piModel is a model of get_state and get_available_models.
type piModel struct {
	Provider  string   `json:"provider"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Input     []string `json:"input"`
	Reasoning bool     `json:"reasoning"`
	// ContextWindow is how many tokens the model's context holds.
	ContextWindow int64 `json:"contextWindow"`
	// ThinkingLevelMap maps a level to the provider's value; null means
	// the model doesn't have the level.
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap"`
}

// modelID is the value of the model setting for m.
func (m *piModel) modelID() string { return m.Provider + "/" + m.ID }

// levels returns the thinking levels m supports, as Pi's
// getSupportedThinkingLevels does: none to choose from for a model that
// doesn't reason, and xhigh and max only where the model maps them.
func (m *piModel) levels() []string {
	if !m.Reasoning {
		return nil
	}
	var levels []string
	for _, level := range thinkingLevels {
		mapped, ok := m.ThinkingLevelMap[level]
		if (ok && mapped == nil) || (!ok && (level == "xhigh" || level == "max")) {
			continue
		}
		levels = append(levels, level)
	}
	return levels
}

// setModelCommand is set_model.
type setModelCommand struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func (c setModelCommand) withID(id string) any {
	c.ID = id
	return c
}

// setThinkingLevelCommand is set_thinking_level.
type setThinkingLevelCommand struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Level string `json:"level"`
}

func (c setThinkingLevelCommand) withID(id string) any {
	c.ID = id
	return c
}

// thinkingLevelChanged is thinking_level_changed.
type thinkingLevelChanged struct {
	Level string `json:"level"`
}

// stateSettings returns the settings a session state reports.
func stateSettings(state sessionState) native.Settings {
	if !state.hasModel() {
		return native.Settings{}
	}
	settings := native.Settings{Model: state.Model.modelID()}
	if state.Model.Reasoning {
		settings.Effort = state.ThinkingLevel
	}
	return settings
}

// setContextWindow records the context window of the session's model.
func (a *Adapter) setContextWindow(state sessionState) {
	if !state.hasModel() {
		return
	}
	a.mu.Lock()
	a.contextWindow = state.Model.ContextWindow
	a.mu.Unlock()
}

// contextUsed records how full the context is after an assistant message's
// model call. Event loop only.
func (a *Adapter) contextUsed(usage *messageUsage) {
	a.mu.Lock()
	window := a.contextWindow
	a.mu.Unlock()
	tokens := usage.contextTokens()
	if window <= 0 || tokens <= 0 {
		return
	}
	a.loop.usage.Context = &native.ContextUsage{Used: int(tokens), Size: int(window)}
	if sink, ok := a.loop.sink.(native.UsageSink); ok {
		sink.UsageChanged(a.loop.usage)
	}
}

// setSettings records the settings, and reports them if report is set and
// they changed. Event loop only when it reports.
func (a *Adapter) setSettings(settings native.Settings, report bool) {
	a.mu.Lock()
	changed := a.settings != settings
	a.settings = settings
	a.mu.Unlock()
	if !changed || !report {
		return
	}
	if sink, ok := a.loop.sink.(native.SettingsSink); ok {
		sink.SettingsChanged(settings)
	}
}

// handleThinkingLevel follows a thinking level Pi changed, by a switch or
// because a new model clamped it.
func (a *Adapter) handleThinkingLevel(level string) {
	settings := a.Settings()
	if settings.Effort == "" || settings.Effort == level {
		return
	}
	settings.Effort = level
	a.setSettings(settings, true)
}

// Settings implements native.Configurable. Pi has no permission modes: it
// runs tools without asking.
func (a *Adapter) Settings() native.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// SettingOptions implements native.Configurable: the models Pi has a
// provider for.
func (a *Adapter) SettingOptions(ctx context.Context) (native.SettingOptions, error) {
	a.mu.Lock()
	models := a.models
	a.mu.Unlock()
	if models == nil {
		res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_available_models"})
		if err == nil {
			err = res.err
		}
		if err != nil {
			return native.SettingOptions{}, fmt.Errorf("list the models: %w", err)
		}
		var data struct {
			Models []piModel `json:"models"`
		}
		if err := json.Unmarshal(res.data, &data); err != nil {
			return native.SettingOptions{}, fmt.Errorf("list the models: %w", err)
		}
		models = []native.ModelOption{}
		for _, m := range data.Models {
			models = append(models, native.ModelOption{ID: m.modelID(), Name: m.Name, Description: m.Provider, Efforts: m.levels()})
		}
		a.mu.Lock()
		a.models = models
		a.mu.Unlock()
	}
	return native.SettingOptions{PermissionModes: []native.PermissionMode{}, Models: slices.Clone(models)}, nil
}

// ApplySettings implements native.Configurable. Pi fits the thinking level
// to a new model by itself; the state it reports afterwards says which.
func (a *Adapter) ApplySettings(ctx context.Context, s native.Settings) error {
	if err := a.ready(); err != nil {
		return err
	}
	options, err := a.SettingOptions(ctx)
	if err != nil {
		return err
	}
	if err := options.Check(s, a.Settings()); err != nil {
		return err
	}
	if s.Model != "" {
		provider, id, _ := strings.Cut(s.Model, "/")
		if err := a.command(ctx, setModelCommand{Type: "set_model", Provider: provider, ModelID: id}); err != nil {
			return fmt.Errorf("switch the model: %w", err)
		}
	}
	if s.Effort != "" {
		if err := a.command(ctx, setThinkingLevelCommand{Type: "set_thinking_level", Level: s.Effort}); err != nil {
			return fmt.Errorf("change the thinking level: %w", err)
		}
	}
	res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_state"})
	if err == nil {
		err = res.err
	}
	if err != nil {
		return fmt.Errorf("read the settings: %w", err)
	}
	var state sessionState
	if err := json.Unmarshal(res.data, &state); err != nil {
		return fmt.Errorf("read the settings: %w", err)
	}
	a.setSettings(stateSettings(state), false)
	a.setContextWindow(state)
	return nil
}

// Compact implements native.Compacter: Pi summarizes the session, and
// answers once it has (compaction_end reports a failure).
func (a *Adapter) Compact(ctx context.Context) error {
	if err := a.ready(); err != nil {
		return err
	}
	if a.isTurnActive() {
		return native.ErrTurnActive
	}
	if err := a.command(ctx, simpleCommand{Type: "compact"}); err != nil {
		return fmt.Errorf("compact the session: %w", err)
	}
	return nil
}

// command sends a command and waits for its success.
func (a *Adapter) command(ctx context.Context, cmd command) error {
	res, err := a.request(ctx, cmdQuery, cmd)
	if err == nil {
		err = res.err
	}
	return err
}
