package claude

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// canUseTool is the request of a can_use_tool control request.
type canUseTool struct {
	ToolName              string            `json:"tool_name"`
	DisplayName           string            `json:"display_name"`
	Input                 json.RawMessage   `json:"input"`
	Description           string            `json:"description"`
	Title                 string            `json:"title"`
	DecisionReason        string            `json:"decision_reason"`
	BlockedPath           string            `json:"blocked_path"`
	ToolUseID             string            `json:"tool_use_id"`
	AgentID               string            `json:"agent_id"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
}

// Option IDs. Approvals and plans answer with Claude Code's own decisions:
// allow or deny, and for plans the permission mode to continue in.
const (
	optionAllow       = "allow"
	optionAllowAlways = "allow_always"
	optionDeny        = "deny"
	optionAcceptEdits = "acceptEdits"
	optionDefault     = "default"
	optionBypass      = modeBypass
)

const (
	toolAskUserQuestion = "AskUserQuestion"
	toolExitPlanMode    = "ExitPlanMode"
)

// pendingRequest is a can_use_tool request waiting for the user.
type pendingRequest struct {
	req native.Request
	raw canUseTool
	// questions maps question IDs to the question text Claude Code keys
	// answers by.
	questions map[string]string
	// answering: an answer is being written to the CLI.
	answering bool
}

type askQuestion struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

// newPendingRequest describes a can_use_tool request. bypass says the
// session can switch to bypassPermissions, so approving a plan can too.
func newPendingRequest(id string, r canUseTool, bypass bool) *pendingRequest {
	p := &pendingRequest{raw: r, req: native.Request{
		ID:        id,
		ToolName:  r.ToolName,
		ToolUseID: r.ToolUseID,
		Input:     r.Input,
	}}
	switch r.ToolName {
	case toolAskUserQuestion:
		if p.question() {
			return p
		}
	case toolExitPlanMode:
		p.plan(bypass)
		return p
	}
	p.approval()
	return p
}

func (p *pendingRequest) approval() {
	r := p.raw
	name := r.DisplayName
	if name == "" {
		name = r.ToolName
	}
	p.req.Kind = native.RequestKindApproval
	p.req.Title = r.Title
	if p.req.Title == "" {
		p.req.Title = name
		if summary := inputSummary(r.Input); summary != "" {
			p.req.Title = name + ": " + summary
		}
	}
	var detail []string
	if r.Description != "" && !strings.Contains(p.req.Title, r.Description) {
		detail = append(detail, r.Description)
	}
	if r.DecisionReason != "" {
		detail = append(detail, "Reason: "+r.DecisionReason)
	}
	if r.BlockedPath != "" {
		detail = append(detail, "Path: "+r.BlockedPath)
	}
	if r.AgentID != "" {
		detail = append(detail, "Requested by a subagent.")
	}
	p.req.Detail = strings.Join(detail, "\n")
	p.req.Feedback = true
	p.req.Options = []native.RequestOption{{ID: optionAllow, Label: "Allow", Kind: native.OptionKindAllow}}
	if len(p.raw.PermissionSuggestions) > 0 {
		p.req.Options = append(p.req.Options, native.RequestOption{
			ID: optionAllowAlways, Label: "Allow always (this session)",
			Description: suggestionSummary(p.sessionPermissions()), Kind: native.OptionKindAllowAlways,
		})
	}
	p.req.Options = append(p.req.Options, native.RequestOption{ID: optionDeny, Label: "Deny", Kind: native.OptionKindDeny})
}

// question maps AskUserQuestion. It reports false if the input holds no
// questions, which then is asked as an approval.
func (p *pendingRequest) question() bool {
	var input struct {
		Questions []askQuestion `json:"questions"`
	}
	if json.Unmarshal(p.raw.Input, &input) != nil || len(input.Questions) == 0 {
		return false
	}
	p.req.Kind = native.RequestKindQuestion
	p.questions = make(map[string]string, len(input.Questions))
	for i, q := range input.Questions {
		id := fmt.Sprintf("q%d", i+1)
		p.questions[id] = q.Question
		question := native.Question{
			ID: id, Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect,
			// Claude Code always lets the user type another answer.
			FreeText: true,
		}
		for _, o := range q.Options {
			question.Options = append(question.Options, native.QuestionOption{Label: o.Label, Description: o.Description})
		}
		p.req.Questions = append(p.req.Questions, question)
	}
	p.req.Title = input.Questions[0].Question
	if len(input.Questions) > 1 {
		p.req.Title = fmt.Sprintf("Claude has %d questions", len(input.Questions))
	}
	return true
}

func (p *pendingRequest) plan(bypass bool) {
	var input struct {
		Plan string `json:"plan"`
	}
	_ = json.Unmarshal(p.raw.Input, &input)
	p.req.Kind = native.RequestKindPlan
	p.req.Title = "Approve the plan?"
	p.req.Detail = input.Plan
	p.req.Feedback = true
	p.req.Options = nil
	if bypass {
		p.req.Options = append(p.req.Options, native.RequestOption{ID: optionBypass, Label: "Yes, and bypass permissions", Description: "Tools run without asking.", Kind: native.OptionKindAllow})
	}
	p.req.Options = append(p.req.Options, []native.RequestOption{
		{ID: optionAcceptEdits, Label: "Yes, and auto-accept edits", Kind: native.OptionKindAllow},
		{ID: optionDefault, Label: "Yes, and manually approve edits", Kind: native.OptionKindAllow},
		{ID: optionDeny, Label: "No, keep planning", Description: "Claude revises the plan; say what to change.", Kind: native.OptionKindDeny},
	}...)
}

// response builds the can_use_tool answer for resp.
func (p *pendingRequest) response(resp native.Response) (map[string]any, error) {
	var decide func(option, feedback string) map[string]any
	switch p.req.Kind {
	case native.RequestKindQuestion:
		return p.answers(resp)
	case native.RequestKindPlan:
		decide = p.planDecision
	case native.RequestKindApproval:
		decide = p.approvalDecision
	}
	// Only what was offered: e.g. no bypassPermissions for the plan of a
	// session that can't switch to it.
	if decide != nil && slices.ContainsFunc(p.req.Options, func(o native.RequestOption) bool { return o.ID == resp.OptionID }) {
		if out := decide(resp.OptionID, strings.TrimSpace(resp.Text)); out != nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("%w: unknown option %q", native.ErrInvalidResponse, resp.OptionID)
}

// answers is the response to a question: the tool's input with the
// answers, by question.
func (p *pendingRequest) answers(resp native.Response) (map[string]any, error) {
	if resp.OptionID != "" {
		return nil, fmt.Errorf("%w: questions take answers, not an option", native.ErrInvalidResponse)
	}
	answers := make(map[string]string, len(p.questions))
	for id, text := range p.questions {
		chosen := resp.Answers[id]
		if len(chosen) == 0 {
			return nil, fmt.Errorf("%w: question %q has no answer", native.ErrInvalidResponse, id)
		}
		answers[text] = strings.Join(chosen, ", ")
	}
	var input map[string]any
	if json.Unmarshal(p.raw.Input, &input) != nil || input == nil {
		input = map[string]any{}
	}
	input["answers"] = answers
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return p.allow(encoded, "user_temporary"), nil
}

// planDecision is the response to a plan for option: carry it out in a
// permission mode, or revise it with feedback. nil for another option.
func (p *pendingRequest) planDecision(option, feedback string) map[string]any {
	switch option {
	case optionBypass, optionAcceptEdits, optionDefault:
		out := p.allow(p.raw.Input, "user_temporary")
		out["updatedPermissions"] = []map[string]any{{"type": "setMode", "mode": option, "destination": "session"}}
		return out
	case optionDeny:
		if feedback == "" {
			feedback = "The user rejected the plan. Revise it and ask again."
		}
		return p.deny(feedback)
	}
	return nil
}

// approvalDecision is the response to a tool call's approval for option.
// nil for another option.
func (p *pendingRequest) approvalDecision(option, feedback string) map[string]any {
	switch option {
	case optionAllow:
		return p.allow(p.raw.Input, "user_temporary")
	case optionAllowAlways:
		// Offered only with suggestions.
		out := p.allow(p.raw.Input, "user_permanent")
		out["updatedPermissions"] = p.sessionPermissions()
		return out
	case optionDeny:
		if feedback == "" {
			feedback = "The user denied this tool call."
		}
		return p.deny(feedback)
	}
	return nil
}

// allow lets the tool call go ahead with input (classified for Claude
// Code's permission log).
func (p *pendingRequest) allow(input json.RawMessage, classification string) map[string]any {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	out := map[string]any{"behavior": "allow", "updatedInput": input, "decisionClassification": classification}
	if p.raw.ToolUseID != "" {
		out["toolUseID"] = p.raw.ToolUseID
	}
	return out
}

// deny refuses the tool call, telling the agent message.
func (p *pendingRequest) deny(message string) map[string]any {
	out := map[string]any{"behavior": "deny", "message": message, "decisionClassification": "user_reject"}
	if p.raw.ToolUseID != "" {
		out["toolUseID"] = p.raw.ToolUseID
	}
	return out
}

// sessionPermissions returns Claude Code's permission suggestions, limited
// to this session. A suggestion to switch the permission mode is left out
// when there are rules to add: "always allow this command" shouldn't also
// start accepting every edit.
func (p *pendingRequest) sessionPermissions() []map[string]any {
	var rules, modes []map[string]any
	for _, raw := range p.raw.PermissionSuggestions {
		var s map[string]any
		if json.Unmarshal(raw, &s) != nil || s == nil {
			continue
		}
		s["destination"] = "session"
		if s["type"] == "setMode" {
			modes = append(modes, s)
		} else {
			rules = append(rules, s)
		}
	}
	if len(rules) > 0 {
		return rules
	}
	return modes
}

// suggestionSummary describes what "allow always" allows.
func suggestionSummary(suggestions []map[string]any) string {
	var parts []string
	for _, s := range suggestions {
		switch s["type"] {
		case "addRules", "replaceRules":
			rules, _ := s["rules"].([]any)
			for _, r := range rules {
				rule, _ := r.(map[string]any)
				name, _ := rule["toolName"].(string)
				content, _ := rule["ruleContent"].(string)
				if content != "" {
					name += "(" + content + ")"
				}
				if name != "" {
					parts = append(parts, name)
				}
			}
		case "addDirectories":
			dirs, _ := s["directories"].([]any)
			for _, d := range dirs {
				if dir, ok := d.(string); ok {
					parts = append(parts, "access to "+dir)
				}
			}
		case "setMode":
			if mode, ok := s["mode"].(string); ok {
				parts = append(parts, "switch to "+mode+" mode")
			}
		}
	}
	return strings.Join(parts, ", ")
}

// inputSummary is the gist of a tool call: its command, file, pattern or
// URL.
func inputSummary(raw json.RawMessage) string {
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "notebook_path", "path", "pattern", "url", "query"} {
		if v, ok := input[key].(string); ok && v != "" {
			return native.OneLine(v, native.MaxTitleRunes)
		}
	}
	return ""
}
