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
	return a.settings
}

// SettingOptions implements native.Configurable.
func (a *Adapter) SettingOptions(ctx context.Context) (native.SettingOptions, error) {
	models, err := a.loadModels(ctx)
	if err != nil {
		return native.SettingOptions{}, err
	}
	return native.SettingOptions{
		PermissionModes: []native.PermissionMode{native.PermissionModeBypass, native.PermissionModeAuto, native.PermissionModeAsk},
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
	if err := options.Check(s, a.settings); err != nil {
		return err
	}
	if s.Model != "" && s.Effort == "" {
		if m, ok := options.Model(s.Model); ok && a.settings.Effort != "" && !slices.Contains(m.Efforts, a.settings.Effort) {
			s.Effort = m.DefaultEffort
		}
	}
	a.settings = a.settings.Merge(s)
	a.pending = a.pending.Merge(s)
	return nil
}

// takePending returns the settings changes the next turn/start carries and
// clears them; restorePending puts them back if the turn didn't start.
func (a *Adapter) takePending(params *turnStartParams) native.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	pending := a.pending
	a.pending = native.Settings{}
	if p, ok := modePolicies(pending.PermissionMode); ok {
		params.ApprovalPolicy = p.approval
		params.SandboxPolicy = &sandboxPolicy{Type: p.sandbox}
		params.ApprovalsReviewer = p.reviewer
	}
	params.Model = pending.Model
	params.Effort = pending.Effort
	return pending
}

func (a *Adapter) restorePending(pending native.Settings) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A change made meanwhile wins.
	a.pending = pending.Merge(a.pending)
}
