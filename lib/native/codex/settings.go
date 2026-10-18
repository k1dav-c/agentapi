package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/coder/agentapi/lib/native"
)

// policies are a thread's approval policy, sandbox and approvals reviewer:
// what a permission mode is made of.
type policies struct {
	approval string
	sandbox  string
	reviewer string
}

// modePolicies returns the policies of an AgentAPI permission mode:
// bypass runs everything without a sandbox; auto works in the workspace
// sandbox and Codex's auto review decides on what needs more; ask asks for
// every command Codex doesn't trust.
func modePolicies(mode native.PermissionMode) (policies, bool) {
	switch mode {
	case native.PermissionModeBypass:
		return policies{approval: "never", sandbox: "dangerFullAccess", reviewer: "user"}, true
	case native.PermissionModeAuto:
		return policies{approval: "on-request", sandbox: "workspaceWrite", reviewer: "auto_review"}, true
	case native.PermissionModeAsk:
		return policies{approval: "untrusted", sandbox: "workspaceWrite", reviewer: "user"}, true
	case native.PermissionModePlan:
		// Not a matter of policies.
		return policies{}, false
	default:
		return policies{}, false
	}
}

// policyMode returns the permission mode that policies amount to, or ""
// for a combination of the user's own (from the command line or config).
func policyMode(p policies) native.PermissionMode {
	switch {
	case p.approval == "never" && p.sandbox == "dangerFullAccess":
		return native.PermissionModeBypass
	case p.approval == "on-request" && (p.reviewer == "auto_review" || p.reviewer == "guardian_subagent"):
		return native.PermissionModeAuto
	case p.approval == "untrusted":
		return native.PermissionModeAsk
	}
	return ""
}

// threadSettings records the settings a thread/start or thread/resume
// result reports.
func (a *Adapter) threadSettings(resp threadStartResponse) {
	var approval string
	// A granular policy is an object: a combination of the user's own.
	_ = json.Unmarshal(resp.ApprovalPolicy, &approval)
	effort := ""
	if resp.ReasoningEffort != nil {
		effort = *resp.ReasoningEffort
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.settings = native.Settings{
		PermissionMode: policyMode(policies{approval: approval, sandbox: resp.Sandbox.Type, reviewer: resp.ApprovalsReviewer}),
		Model:          resp.Model,
		Effort:         effort,
	}
	a.collab, a.collabStale = "", false
	if resp.CollaborationMode != nil {
		a.collab = resp.CollaborationMode.Mode
	}
	a.plan = a.collab == collabPlan
}

// loadModels asks app-server for its models, unless the adapter has them:
// the first time the settings' options are needed.
func (a *Adapter) loadModels(ctx context.Context) ([]native.ModelOption, error) {
	a.mu.Lock()
	models := a.models
	a.mu.Unlock()
	if models != nil {
		return models, nil
	}
	models = []native.ModelOption{}
	cursor := ""
	for {
		params := modelListParams{}
		if cursor != "" {
			params.Cursor = &cursor
		}
		result, err := a.call(ctx, "model/list", params, nil)
		if err != nil {
			return nil, fmt.Errorf("list the models: %w", err)
		}
		var page modelListResponse
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("list the models: %w", err)
		}
		for _, m := range page.Data {
			if m.Hidden {
				continue
			}
			option := native.ModelOption{ID: m.Model, Name: m.DisplayName, Description: m.Description, DefaultEffort: m.DefaultReasoningEffort}
			if option.ID == "" {
				option.ID = m.ID
			}
			for _, e := range m.SupportedReasoningEfforts {
				option.Efforts = append(option.Efforts, e.ReasoningEffort)
			}
			models = append(models, option)
		}
		if page.NextCursor == nil || *page.NextCursor == "" || *page.NextCursor == cursor {
			break
		}
		cursor = *page.NextCursor
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.models = models
	// A thread on the model's default effort reports none.
	if a.settings.Effort == "" {
		if i := slices.IndexFunc(models, func(m native.ModelOption) bool { return m.ID == a.settings.Model }); i >= 0 {
			a.settings.Effort = models[i].DefaultEffort
		}
	}
	return models, nil
}

// Settings implements native.Configurable. A change shows here at once,
// though Codex applies it from the next turn.
func (a *Adapter) Settings() native.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settingsLocked()
}

// settingsLocked is a.settings, in the plan mode while the thread plans:
// a.settings keeps the mode whose policies apply meanwhile.
func (a *Adapter) settingsLocked() native.Settings {
	s := a.settings
	if a.plan {
		s.PermissionMode = native.PermissionModePlan
	}
	return s
}

// SettingOptions implements native.Configurable.
func (a *Adapter) SettingOptions(ctx context.Context) (native.SettingOptions, error) {
	models, err := a.loadModels(ctx)
	if err != nil {
		return native.SettingOptions{}, err
	}
	return native.SettingOptions{
		PermissionModes: native.PermissionModeValues,
		Models:          slices.Clone(models),
	}, nil
}

// ApplySettings implements native.Configurable. Codex takes the settings
// with the next turn/start, for that turn and the ones after it. A new
// model without the current effort gets its default one.
func (a *Adapter) ApplySettings(ctx context.Context, s native.Settings) error {
	options, err := a.SettingOptions(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := options.Check(s, a.settingsLocked()); err != nil {
		return err
	}
	if s.Model != "" && s.Effort == "" {
		if m, ok := options.Model(s.Model); ok && a.settings.Effort != "" && !slices.Contains(m.Efforts, a.settings.Effort) {
			s.Effort = m.DefaultEffort
		}
	}
	switch {
	case s.PermissionMode == native.PermissionModePlan:
		// The policies stay those of the mode before.
		s.PermissionMode = ""
		a.plan = true
	case s.PermissionMode != "":
		a.plan = false
	}
	a.settings = a.settings.Merge(s)
	a.pending = a.pending.Merge(s)
	if s.Model != "" || s.Effort != "" {
		a.collabStale = true
	}
	return nil
}

// turnChanges are the changes a turn/start carries.
type turnChanges struct {
	// settings changed since the last turn/start; stale is collabStale.
	settings native.Settings
	stale    bool
	// collab is the collaboration mode the turn switches to, or "".
	collab string
	// implement is the permission mode a turn that carries out the plan
	// switches to; plan and mode are what the thread had before.
	implement native.PermissionMode
	plan      bool
	mode      native.PermissionMode
}

// takeChanges puts the changes the next turn/start carries in params and
// clears them. With implement set, the turn leaves the plan mode, in the
// policies of implement. turnStarted applies what the turn changed once it
// started; restoreChanges puts the changes back if it didn't.
func (a *Adapter) takeChanges(params *turnStartParams, implement native.PermissionMode) turnChanges {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := turnChanges{settings: a.pending, stale: a.collabStale, implement: implement, plan: a.plan, mode: a.settings.PermissionMode}
	a.pending, a.collabStale = native.Settings{}, false
	mode, plan := c.settings.PermissionMode, a.plan
	if implement != "" {
		mode, plan = implement, false
	}
	if p, ok := modePolicies(mode); ok {
		params.ApprovalPolicy = p.approval
		params.SandboxPolicy = &sandboxPolicy{Type: p.sandbox}
		params.ApprovalsReviewer = p.reviewer
	}
	params.Model = c.settings.Model
	params.Effort = c.settings.Effort
	if c.collab = a.nextCollabLocked(plan, c.stale); c.collab != "" {
		params.CollaborationMode = collaboration(c.collab, a.settings)
	}
	return c
}

// nextCollabLocked returns the collaboration mode a turn switches to, ""
// to keep the thread's: into the plan mode or out of it, or the thread's
// again when stale, since its model and effort win over the turn's.
func (a *Adapter) nextCollabLocked(plan, stale bool) string {
	want := collabDefault
	if plan {
		want = collabPlan
	}
	if a.collab == "" && !plan {
		return ""
	}
	if a.collab != want || stale {
		return want
	}
	return ""
}

// turnStarted applies what a turn that started changed. A change made
// meanwhile wins.
func (a *Adapter) turnStarted(c turnChanges) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c.collab != "" {
		a.collab = c.collab
	}
	if c.implement != "" {
		if a.plan == c.plan {
			a.plan = false
		}
		if a.settings.PermissionMode == c.mode {
			a.settings.PermissionMode = c.implement
		}
	}
}

// restoreChanges puts back the changes of a turn that didn't start. A
// change made meanwhile wins.
func (a *Adapter) restoreChanges(c turnChanges) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = c.settings.Merge(a.pending)
	a.collabStale = a.collabStale || c.stale
}
