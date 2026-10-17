package codex

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// The plan permission mode is Codex's plan collaboration mode, an
// experimental part of app-server's API. Codex looks around, asks what it
// needs to know and ends its turn with a plan item. AgentAPI then asks
// whether to carry it out, as the Codex TUI does, and the turn goes on
// with the answer: an approved plan is implemented in the default
// collaboration mode and the permission mode the answer picks, as Claude
// Code's plan approval does; feedback goes back to Codex, still planning.

const (
	collabPlan    = "plan"
	collabDefault = "default"

	// Implementing options are optionImplement plus the permission mode.
	optionImplement    = "implement_"
	optionKeepPlanning = "keep_planning"

	// implementPrompt starts the turn that carries out an approved plan,
	// as the Codex TUI's does.
	implementPrompt = "Implement the plan."
)

// collaborationMode is CollaborationMode. Its settings take precedence
// over the turn's model and effort, so they repeat the thread's.
type collaborationMode struct {
	Mode     string                    `json:"mode"`
	Settings collaborationModeSettings `json:"settings"`
}

type collaborationModeSettings struct {
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
	// DeveloperInstructions is null: the mode's built-in instructions.
	DeveloperInstructions *string `json:"developer_instructions"`
}

// collaboration returns the collaboration mode to send for mode, on the
// model and effort of s.
func collaboration(mode string, s native.Settings) *collaborationMode {
	c := &collaborationMode{Mode: mode, Settings: collaborationModeSettings{Model: s.Model}}
	if s.Effort != "" {
		effort := s.Effort
		c.Settings.ReasoningEffort = &effort
	}
	return c
}

// implementOptions are the ways to implement a plan: one per permission
// mode, whose name follows optionImplement in the ID.
var implementOptions = []native.RequestOption{
	{ID: optionImplement + string(native.PermissionModeBypass), Label: "Yes, and bypass permissions", Description: "Codex carries out the plan without approvals or sandbox.", Kind: native.OptionKindAllow},
	{ID: optionImplement + string(native.PermissionModeAuto), Label: "Yes, with auto review", Description: "Codex works in its sandbox, and its auto review decides on what needs more.", Kind: native.OptionKindAllow},
	{ID: optionImplement + string(native.PermissionModeAsk), Label: "Yes, and ask before commands", Description: "Codex asks before it runs a command it doesn't trust.", Kind: native.OptionKindAllow},
}

// implementOption returns the index of the implementing option with id, or
// -1.
func implementOption(id string) int {
	return slices.IndexFunc(implementOptions, func(o native.RequestOption) bool { return o.ID == id })
}

// planRequest is the request that asks whether, and in which permission
// mode, to implement plan. The mode the thread had before the plan comes
// first.
func (a *Adapter) planRequest(turnID, plan string) native.Request {
	a.mu.Lock()
	before := a.settings.PermissionMode
	a.mu.Unlock()
	options := slices.Clone(implementOptions)
	if i := implementOption(optionImplement + string(before)); i > 0 {
		first := options[i]
		options = slices.Insert(slices.Delete(options, i, i+1), 0, first)
	}
	return native.Request{
		ID:       a.prefix + "-plan-" + turnID,
		Kind:     native.RequestKindPlan,
		Title:    "Implement this plan?",
		Detail:   plan,
		Feedback: true,
		Options: append(options, native.RequestOption{
			ID: optionKeepPlanning, Label: "No, keep planning", Description: "Say what to change, and Codex revises the plan.", Kind: native.OptionKindDeny,
		}),
	}
}

// awaitPlanDecision asks whether to implement the plan turn t ended with,
// instead of ending the turn: it goes on once the user answers. Read loop
// only.
func (s *loopState) awaitPlanDecision(t *turnState) {
	req := s.a.planRequest(t.id, t.plan)
	s.a.mu.Lock()
	s.a.planWait = req.ID
	s.a.mu.Unlock()
	s.sink().RequestOpened(req)
}

// answerPlan answers the plan request: implement the plan in the mode the
// answer picks, revise it with the feedback, or end the turn when the user
// wants to go on planning without saying how.
func (a *Adapter) answerPlan(ctx context.Context, requestID string, response native.Response) error {
	feedback := strings.TrimSpace(response.Text)
	implement := implementOption(response.OptionID) >= 0
	if !implement && response.OptionID != optionKeepPlanning {
		return fmt.Errorf("%w: unknown option %q", native.ErrInvalidResponse, response.OptionID)
	}
	a.mu.Lock()
	if a.planWait != requestID {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	a.planWait = ""
	a.mu.Unlock()

	var err error
	switch {
	case implement:
		undo := a.leavePlanMode(native.PermissionMode(strings.TrimPrefix(response.OptionID, optionImplement)))
		if err = a.StartTurn(ctx, native.Input{Text: implementPrompt}); err != nil {
			// The thread still plans; so does the answer tried again.
			undo()
		} else {
			err = a.inLoop(ctx, a.reportSettings)
		}
	case feedback != "":
		err = a.StartTurn(ctx, native.Input{Text: feedback})
	default:
		err = a.inLoop(ctx, func() { a.sink.TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn}) })
	}
	if err != nil {
		// The request can be answered again.
		a.mu.Lock()
		a.planWait = requestID
		a.mu.Unlock()
	}
	return err
}

// cancelPlanDecision ends the turn that waits for the plan request, which
// Interrupt does. It reports whether one waited.
func (a *Adapter) cancelPlanDecision(ctx context.Context) (bool, error) {
	a.mu.Lock()
	id := a.planWait
	a.planWait = ""
	a.mu.Unlock()
	if id == "" {
		return false, nil
	}
	return true, a.inLoop(ctx, func() {
		a.sink.RequestClosed(id)
		a.sink.TurnCompleted(native.TurnResult{StopReason: native.StopReasonInterrupted})
	})
}

// leavePlanMode switches back to the default collaboration mode, in mode's
// policies, with the next turn. undo puts back what it changed.
func (a *Adapter) leavePlanMode(mode native.PermissionMode) (undo func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, collab, current, pending := a.plan, a.pendingCollab, a.settings.PermissionMode, a.pending.PermissionMode
	a.plan = false
	a.pendingCollab = collabDefault
	a.settings.PermissionMode = mode
	a.pending.PermissionMode = mode
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.plan, a.pendingCollab, a.settings.PermissionMode, a.pending.PermissionMode = plan, collab, current, pending
	}
}

// reportSettings tells the conversation about settings that changed
// without ApplySettings. Read loop only.
func (a *Adapter) reportSettings() {
	native.ReportSettings(a.sink, a.Settings())
}
