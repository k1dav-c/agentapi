package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// The plan permission mode is Codex's plan collaboration mode, an
// experimental part of app-server's API. Codex looks around, asks what it
// needs to know and ends its turn with a plan item. AgentAPI then asks
// whether to carry it out, as the Codex TUI does, and the turn goes on
// with the answer: an approved plan is implemented in the default mode,
// feedback goes back to Codex, still planning.

const (
	collabPlan    = "plan"
	collabDefault = "default"

	optionImplement    = "implement"
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

// planRequest is the request that asks whether to implement plan.
func (a *Adapter) planRequest(turnID, plan string) native.Request {
	return native.Request{
		ID:       a.prefix + "-plan-" + turnID,
		Kind:     native.RequestKindPlan,
		Title:    "Implement this plan?",
		Detail:   plan,
		Feedback: true,
		Options: []native.RequestOption{
			{ID: optionImplement, Label: "Yes, implement it", Description: "Codex leaves the plan mode and carries out the plan.", Kind: native.OptionKindAllow},
			{ID: optionKeepPlanning, Label: "No, keep planning", Description: "Say what to change, and Codex revises the plan.", Kind: native.OptionKindDeny},
		},
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

// answerPlan answers the plan request: implement the plan, revise it with
// the feedback, or end the turn when the user wants to go on planning
// without saying how.
func (a *Adapter) answerPlan(ctx context.Context, requestID string, response native.Response) error {
	feedback := strings.TrimSpace(response.Text)
	switch response.OptionID {
	case optionImplement, optionKeepPlanning:
	default:
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
	case response.OptionID == optionImplement:
		a.leavePlanMode()
		err = a.StartTurn(ctx, native.Input{Text: implementPrompt})
		if err == nil {
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

// leavePlanMode switches back to the default collaboration mode, with the
// next turn.
func (a *Adapter) leavePlanMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.plan {
		a.plan = false
		a.pendingCollab = collabDefault
	}
}

// reportSettings tells the conversation about settings that changed
// without ApplySettings. Read loop only.
func (a *Adapter) reportSettings() {
	if sink, ok := a.sink.(native.SettingsSink); ok {
		sink.SettingsChanged(a.Settings())
	}
}
