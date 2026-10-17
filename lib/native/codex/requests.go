package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
)

const (
	methodCommandApproval    = "item/commandExecution/requestApproval"
	methodFileChangeApproval = "item/fileChange/requestApproval"
	methodRequestUserInput   = "item/tool/requestUserInput"
	methodElicitation        = "mcpServer/elicitation/request"
	methodPermissions        = "item/permissions/requestApproval"
)

// JSON-RPC error codes for the server requests AgentAPI declines.
const (
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
)

// pendingRequest is a server request waiting for the user.
type pendingRequest struct {
	id     string
	rpcID  json.RawMessage
	method string
	turnID string
	// toolUseID is the tool call the request is about.
	toolUseID string
	// results maps option IDs to the response to send.
	results map[string]any
	// questions of a question request; form converts their answers for
	// an MCP form elicitation.
	questions []native.Question
	form      map[string]formField
	answered  bool
	// answer is the result of the request_user_input tool call once it is
	// answered, as the chat shows it.
	answer string
}

// formField is a field of an MCP elicitation form.
type formField struct {
	typ string // string | number | integer | boolean | array
	// values maps option labels to the values to send.
	values map[string]string
}

// serverRequest handles a request from app-server. Requests AgentAPI can't
// show are answered with an error, never approved.
func (s *loopState) serverRequest(msg rpcMessage) {
	var r *pendingRequest
	var req native.Request
	var ok bool
	switch msg.Method {
	case methodCommandApproval:
		r, req, ok = s.commandApproval(msg)
	case methodFileChangeApproval:
		r, req, ok = s.fileChangeApproval(msg)
	case methodRequestUserInput:
		r, req, ok = s.userInputRequest(msg)
	case methodElicitation:
		r, req, ok = s.elicitationRequest(msg)
	case methodPermissions:
		r, req, ok = s.permissionsRequest(msg)
	default:
		s.decline(msg, errCodeMethodNotFound, "AgentAPI does not handle "+msg.Method)
		return
	}
	if !ok {
		s.decline(msg, errCodeInvalidParams, "AgentAPI can't read the params of "+msg.Method)
		return
	}
	r.id = s.a.prefix + "-" + strings.Trim(string(msg.ID), `"`)
	r.rpcID = msg.ID
	r.method = msg.Method
	req.ID = r.id
	req.CreatedAt = s.now()
	req.Capability = native.ResponseLive
	s.a.mu.Lock()
	s.a.requests[r.id] = r
	s.a.mu.Unlock()
	s.sink().RequestOpened(req)
}

// decline answers a request AgentAPI can't show with an error.
func (s *loopState) decline(msg rpcMessage, code int, message string) {
	s.a.log.Warn("Declining a Codex request AgentAPI can't show", "method", msg.Method)
	s.a.writeAsync(rpcResponse{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: code, Message: message}})
}

// addOption offers opt and records the result it sends. An option already
// offered is skipped.
func addOption(req *native.Request, r *pendingRequest, opt native.RequestOption, result any) {
	if _, dup := r.results[opt.ID]; dup {
		return
	}
	req.Options = append(req.Options, opt)
	r.results[opt.ID] = result
}

// toolItem returns the open tool item itemID of the active turn.
func (s *loopState) toolItem(itemID string) *itemState {
	if s.turn == nil {
		return nil
	}
	if state := s.turn.items[itemID]; state != nil && state.blk != nil && state.blk.Type == "tool_use" {
		return state
	}
	return nil
}

func (s *loopState) commandApproval(msg rpcMessage) (*pendingRequest, native.Request, bool) {
	var p commandExecutionRequestApprovalParams
	if !decode(msg, &p) {
		return nil, native.Request{}, false
	}
	// The command that runs, not Codex's reading of it: that may leave out
	// parts (pipes, the cd of `cd dir && ...`).
	command := unwrapShell(p.Command)
	req := native.Request{
		Kind: native.RequestKindApproval, Title: command, ToolName: "exec_command",
		ToolUseID: p.ItemID, Input: commandInput(p.Command, p.CWD),
	}
	var detail []string
	if p.Reason != "" {
		detail = append(detail, p.Reason)
	}
	detail = append(detail, actionsDetail(p.CommandActions, command)...)
	if p.NetworkApprovalContext != nil {
		detail = append(detail, fmt.Sprintf("Network access to %s (%s)", p.NetworkApprovalContext.Host, p.NetworkApprovalContext.Protocol))
		if command == "" {
			req.Title = "Allow network access to " + p.NetworkApprovalContext.Host
		}
	}
	if p.CWD != "" {
		detail = append(detail, "Working directory: "+p.CWD)
	}
	req.Detail = strings.Join(detail, "\n")
	if req.Title == "" {
		req.Title = "Run a command"
	}

	r := &pendingRequest{turnID: p.TurnID, toolUseID: p.ItemID, results: map[string]any{}}
	for _, decision := range commandDecisions(p) {
		addCommandDecision(&req, r, decision)
	}
	// app-server accepts decline even when it doesn't offer it; denying
	// must always be possible.
	addCommandDecision(&req, r, json.RawMessage(`"decline"`))
	sortOptions(req.Options)
	return r, req, true
}

// commandDecisions are the decisions to offer for a command: the ones
// app-server lists, or else the ones its proposals allow.
func commandDecisions(p commandExecutionRequestApprovalParams) []json.RawMessage {
	if len(p.AvailableDecisions) > 0 {
		return p.AvailableDecisions
	}
	decisions := []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"acceptForSession"`)}
	if len(p.ProposedExecpolicyAmendment) > 0 {
		decisions = append(decisions, mustJSON(map[string]any{
			"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": p.ProposedExecpolicyAmendment},
		}))
	}
	for _, amendment := range p.ProposedNetworkPolicyAmendments {
		decisions = append(decisions, mustJSON(map[string]json.RawMessage{
			"applyNetworkPolicyAmendment": mustJSON(map[string]json.RawMessage{"network_policy_amendment": amendment}),
		}))
	}
	return append(decisions, json.RawMessage(`"cancel"`))
}

// addCommandDecision offers one decision of a command approval: a name
// ("accept") or an amendment object.
func addCommandDecision(req *native.Request, r *pendingRequest, raw json.RawMessage) {
	add := func(id, label string, kind native.OptionKind, decision any) {
		addOption(req, r, native.RequestOption{ID: id, Label: label, Kind: kind}, map[string]any{"decision": decision})
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		switch name {
		case "accept":
			add(name, "Allow", native.OptionKindAllow, name)
		case "acceptForSession":
			add(name, "Allow for this session", native.OptionKindAllowAlways, name)
		case "decline":
			add(name, "Deny", native.OptionKindDeny, name)
		case "cancel":
			add(name, "Deny and stop the turn", native.OptionKindCancel, name)
		}
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return
	}
	if v, ok := obj["acceptWithExecpolicyAmendment"]; ok {
		var amendment struct {
			Prefix []string `json:"execpolicy_amendment"`
		}
		_ = json.Unmarshal(v, &amendment)
		label := "Always allow this command"
		if len(amendment.Prefix) > 0 {
			label = fmt.Sprintf("Always allow commands starting with `%s`", strings.Join(amendment.Prefix, " "))
		}
		add("acceptWithExecpolicyAmendment", label, native.OptionKindAllowAlways, raw)
	}
	if v, ok := obj["applyNetworkPolicyAmendment"]; ok {
		var amendment struct {
			Rule struct {
				Action string `json:"action"`
				Host   string `json:"host"`
			} `json:"network_policy_amendment"`
		}
		_ = json.Unmarshal(v, &amendment)
		id := "applyNetworkPolicyAmendment"
		if n := countPrefix(r.results, id); n > 0 {
			id = fmt.Sprintf("%s:%d", id, n+1)
		}
		if amendment.Rule.Action == "deny" {
			add(id, "Always block network access to "+amendment.Rule.Host, native.OptionKindDeny, raw)
		} else {
			add(id, "Always allow network access to "+amendment.Rule.Host, native.OptionKindAllowAlways, raw)
		}
	}
}

func countPrefix(m map[string]any, prefix string) int {
	n := 0
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}
	return n
}

// sortOptions puts allow options first, then deny, then cancel, keeping
// the agent's order otherwise.
func sortOptions(options []native.RequestOption) {
	rank := func(k native.OptionKind) int {
		switch k {
		case native.OptionKindAllow:
			return 0
		case native.OptionKindAllowAlways:
			return 1
		case native.OptionKindOther:
			return 2
		case native.OptionKindDeny:
			return 3
		case native.OptionKindCancel:
			return 4
		}
		return 2
	}
	slices.SortStableFunc(options, func(x, y native.RequestOption) int { return rank(x.Kind) - rank(y.Kind) })
}

func (s *loopState) fileChangeApproval(msg rpcMessage) (*pendingRequest, native.Request, bool) {
	var p fileChangeRequestApprovalParams
	if !decode(msg, &p) {
		return nil, native.Request{}, false
	}
	req := native.Request{Kind: native.RequestKindApproval, Title: "Edit files", ToolName: "apply_patch", ToolUseID: p.ItemID}
	var detail []string
	if p.Reason != "" {
		detail = append(detail, p.Reason)
	}
	// The diff is not in the request; the fileChange item has it.
	if state := s.toolItem(p.ItemID); state != nil {
		req.Input = state.blk.ToolInput
		if changes := state.changes; len(changes) > 0 {
			if len(changes) == 1 {
				req.Title = "Edit " + filepath.Base(changes[0].Path)
			} else {
				req.Title = fmt.Sprintf("Edit %d files", len(changes))
			}
			detail = append(detail, fileChangeDetail(changes))
		}
	}
	req.Detail = strings.Join(detail, "\n\n")
	sessionDesc := ""
	if p.GrantRoot != "" {
		sessionDesc = "Also allows writes under " + p.GrantRoot + " for the rest of the session."
	}
	r := &pendingRequest{turnID: p.TurnID, toolUseID: p.ItemID, results: map[string]any{}}
	for _, opt := range []native.RequestOption{
		{ID: "accept", Label: "Allow", Kind: native.OptionKindAllow},
		{ID: "acceptForSession", Label: "Allow for this session", Description: sessionDesc, Kind: native.OptionKindAllowAlways},
		{ID: "decline", Label: "Deny", Kind: native.OptionKindDeny},
		{ID: "cancel", Label: "Deny and stop the turn", Kind: native.OptionKindCancel},
	} {
		addOption(&req, r, opt, map[string]any{"decision": opt.ID})
	}
	return r, req, true
}

// userInputQuestionsInput is the tool input of request_user_input as the
// rollout records it.
func userInputQuestionsInput(questions []userInputQuestion) json.RawMessage {
	type option struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	type question struct {
		Header   string   `json:"header"`
		ID       string   `json:"id"`
		Question string   `json:"question"`
		Options  []option `json:"options,omitempty"`
	}
	in := struct {
		Questions []question `json:"questions"`
	}{Questions: []question{}}
	for _, q := range questions {
		qq := question{Header: q.Header, ID: q.ID, Question: q.Question}
		for _, o := range q.Options {
			qq.Options = append(qq.Options, option(o))
		}
		in.Questions = append(in.Questions, qq)
	}
	return mustJSON(in)
}

func (s *loopState) userInputRequest(msg rpcMessage) (*pendingRequest, native.Request, bool) {
	var p toolRequestUserInputParams
	if !decode(msg, &p) || len(p.Questions) == 0 {
		return nil, native.Request{}, false
	}
	input := userInputQuestionsInput(p.Questions)
	req := native.Request{
		Kind: native.RequestKindQuestion, ToolName: "request_user_input", ToolUseID: p.ItemID, Input: input,
	}
	for _, q := range p.Questions {
		nq := native.Question{
			ID: q.ID, Header: q.Header, Question: q.Question, Secret: q.IsSecret,
			FreeText: q.IsOther || len(q.Options) == 0,
		}
		for _, o := range q.Options {
			nq.Options = append(nq.Options, native.QuestionOption{Label: o.Label, Description: o.Description})
		}
		req.Questions = append(req.Questions, nq)
	}
	req.Title = p.Questions[0].Question
	if len(p.Questions) > 1 {
		req.Title = fmt.Sprintf("Codex asks %d questions", len(p.Questions))
	}
	// There is no item for the tool call; show it from the request.
	if s.ours(p.ThreadID) {
		if t := s.turnFor(p.TurnID); t != nil && t.items[p.ItemID] == nil {
			state := s.addItem(t, &threadItem{ID: p.ItemID, Type: "requestUserInput"}, &block{RichContentBlock: jsonlwatcher.RichContentBlock{
				Type: "tool_use", ToolUseID: p.ItemID, ToolName: "request_user_input", ToolInput: input, Status: "running",
			}})
			s.toolCallEvent(state.blk, s.now(), p.ItemID)
		}
	}
	return &pendingRequest{turnID: p.TurnID, toolUseID: p.ItemID, questions: req.Questions}, req, true
}

func (s *loopState) elicitationRequest(msg rpcMessage) (*pendingRequest, native.Request, bool) {
	var p mcpServerElicitationRequestParams
	if !decode(msg, &p) {
		return nil, native.Request{}, false
	}
	r := &pendingRequest{turnID: p.TurnID, results: map[string]any{}}
	req := native.Request{Kind: native.RequestKindApproval, Title: p.Message}
	if req.Title == "" {
		req.Title = fmt.Sprintf("The %s MCP server asks for input", p.ServerName)
	}
	// An MCP tool call waiting for approval: the latest open call of that
	// server in the turn.
	if s.turn != nil {
		for i := len(s.turn.order) - 1; i >= 0; i-- {
			state := s.turn.order[i]
			if state.kind == "mcpToolCall" && !state.done && state.server == p.ServerName {
				req.ToolUseID, req.ToolName, req.Input = state.id, state.blk.ToolName, state.blk.ToolInput
				r.toolUseID = state.id
				break
			}
		}
	}

	var properties map[string]mcpElicitationProperty
	if p.RequestedSchema != nil {
		properties = p.RequestedSchema.Properties
	}
	switch {
	case p.Mode == "url":
		req.Detail = "Open " + p.URL
		addOption(&req, r, native.RequestOption{ID: "accept", Label: "Continue", Description: "After completing the step at the link", Kind: native.OptionKindAllow},
			mcpServerElicitationResponse{Action: "accept"})
	case len(properties) == 0 || (p.Meta != nil && p.Meta.ApprovalKind == "mcp_tool_call"):
		if p.Meta != nil {
			var detail []string
			if p.Meta.ToolDescription != "" {
				detail = append(detail, p.Meta.ToolDescription)
			}
			if len(p.Meta.ToolParams) > 0 && string(p.Meta.ToolParams) != "null" {
				if req.Input == nil {
					req.Input = p.Meta.ToolParams
				}
				detail = append(detail, "Arguments: "+string(p.Meta.ToolParams))
			}
			req.Detail = strings.Join(detail, "\n")
		}
		addOption(&req, r, native.RequestOption{ID: "accept", Label: "Allow", Kind: native.OptionKindAllow},
			mcpServerElicitationResponse{Action: "accept", Content: map[string]any{}})
	default:
		// A form: one question per field, answered with accept + content.
		req.Kind = native.RequestKindQuestion
		r.form = map[string]formField{}
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			q, field := formQuestion(name, properties[name])
			req.Questions = append(req.Questions, q)
			r.form[name] = field
		}
		r.questions = req.Questions
	}
	addOption(&req, r, native.RequestOption{ID: "decline", Label: "Deny", Kind: native.OptionKindDeny},
		mcpServerElicitationResponse{Action: "decline"})
	addOption(&req, r, native.RequestOption{ID: "cancel", Label: "Cancel", Kind: native.OptionKindCancel},
		mcpServerElicitationResponse{Action: "cancel"})
	return r, req, true
}

func formQuestion(name string, prop mcpElicitationProperty) (native.Question, formField) {
	q := native.Question{ID: name, Header: prop.Title, Question: prop.Description}
	if q.Question == "" {
		q.Question = prop.Title
	}
	if q.Question == "" {
		q.Question = name
	}
	field := formField{typ: prop.Type, values: map[string]string{}}
	addOption := func(value, title string) {
		label := title
		if label == "" {
			label = value
		}
		q.Options = append(q.Options, native.QuestionOption{Label: label})
		field.values[label] = value
	}
	switch {
	case len(prop.OneOf) > 0:
		for _, o := range prop.OneOf {
			addOption(o.Const, o.Title)
		}
	case len(prop.Enum) > 0:
		for i, v := range prop.Enum {
			title := ""
			if i < len(prop.EnumNames) {
				title = prop.EnumNames[i]
			}
			addOption(v, title)
		}
	case prop.Items != nil:
		q.MultiSelect = true
		for _, o := range prop.Items.AnyOf {
			addOption(o.Const, o.Title)
		}
		for _, v := range prop.Items.Enum {
			addOption(v, "")
		}
	case prop.Type == "boolean":
		addOption("true", "Yes")
		addOption("false", "No")
	default:
		q.FreeText = true
	}
	return q, field
}

func (s *loopState) permissionsRequest(msg rpcMessage) (*pendingRequest, native.Request, bool) {
	var p permissionsRequestApprovalParams
	if !decode(msg, &p) {
		return nil, native.Request{}, false
	}
	req := native.Request{
		Kind: native.RequestKindApproval, Title: "Grant Codex additional permissions",
		ToolUseID: p.ItemID, Input: p.Permissions,
	}
	var detail []string
	if p.Reason != "" {
		detail = append(detail, p.Reason)
	}
	detail = append(detail, "Requested: "+string(p.Permissions))
	req.Detail = strings.Join(detail, "\n")
	granted := rawOrEmpty(p.Permissions)
	r := &pendingRequest{turnID: p.TurnID, toolUseID: p.ItemID, results: map[string]any{}}
	addOption(&req, r, native.RequestOption{ID: "turn", Label: "Allow for this turn", Kind: native.OptionKindAllow},
		permissionsRequestApprovalResponse{Permissions: granted, Scope: "turn"})
	addOption(&req, r, native.RequestOption{ID: "session", Label: "Allow for this session", Kind: native.OptionKindAllowAlways},
		permissionsRequestApprovalResponse{Permissions: granted, Scope: "session"})
	addOption(&req, r, native.RequestOption{ID: "decline", Label: "Deny", Kind: native.OptionKindDeny},
		permissionsRequestApprovalResponse{Permissions: json.RawMessage("{}"), Scope: "turn"})
	return r, req, true
}

// result translates a response into the JSON-RPC result for r.
func (r *pendingRequest) result(resp native.Response) (any, error) {
	if resp.OptionID != "" {
		res, ok := r.results[resp.OptionID]
		if !ok {
			return nil, fmt.Errorf("%w: unknown option %q", native.ErrInvalidResponse, resp.OptionID)
		}
		return res, nil
	}
	if len(r.questions) == 0 {
		return nil, fmt.Errorf("%w: choose one of the options", native.ErrInvalidResponse)
	}
	for _, q := range r.questions {
		if len(resp.Answers[q.ID]) == 0 {
			return nil, fmt.Errorf("%w: question %q has no answer", native.ErrInvalidResponse, q.ID)
		}
	}
	if r.form == nil {
		return r.userInputResponse(resp.Answers, false), nil
	}
	content := map[string]any{}
	for _, q := range r.questions {
		value, err := r.form[q.ID].convert(resp.Answers[q.ID])
		if err != nil {
			return nil, fmt.Errorf("%w: question %q: %v", native.ErrInvalidResponse, q.ID, err)
		}
		content[q.ID] = value
	}
	return mcpServerElicitationResponse{Action: "accept", Content: content}, nil
}

// userInputResponse is the answer to a request_user_input request; redact
// replaces the answers to secret questions, for everyone but the agent.
func (r *pendingRequest) userInputResponse(answers map[string][]string, redact bool) toolRequestUserInputResponse {
	out := toolRequestUserInputResponse{Answers: map[string]userInputAnswer{}}
	for _, q := range r.questions {
		a := answers[q.ID]
		if redact && q.Secret {
			a = []string{native.RedactedAnswer}
		}
		out.Answers[q.ID] = userInputAnswer{Answers: a}
	}
	return out
}

// convert turns the answers to a form field into the field's value.
func (f formField) convert(answers []string) (any, error) {
	mapped := make([]string, len(answers))
	for i, a := range answers {
		if v, ok := f.values[a]; ok {
			mapped[i] = v
		} else {
			mapped[i] = a
		}
	}
	switch f.typ {
	case "array":
		return mapped, nil
	case "boolean":
		return strconv.ParseBool(mapped[0])
	case "number":
		return strconv.ParseFloat(strings.TrimSpace(mapped[0]), 64)
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(mapped[0]), 10, 64)
	}
	return mapped[0], nil
}

// Respond implements native.Adapter.
func (a *Adapter) Respond(_ context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	r := a.requests[requestID]
	if r == nil || r.answered {
		a.mu.Unlock()
		return native.ErrRequestNotFound
	}
	result, err := r.result(response)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	r.answered = true
	if r.method == methodRequestUserInput {
		// The tool call shows it once app-server has it (requestResolved).
		r.answer = string(mustJSON(r.userInputResponse(response.Answers, true)))
	}
	a.mu.Unlock()

	if err := a.write(rpcResponse{JSONRPC: "2.0", ID: r.rpcID, Result: result}); err != nil {
		a.mu.Lock()
		r.answered = false
		a.mu.Unlock()
		return err
	}
	return nil
}

// requestResolved handles serverRequest/resolved: app-server no longer
// waits for the request, because it was answered or it gave up (the turn
// was interrupted). It comes before what the answer leads to, so the
// request_user_input tool call ends here.
func (s *loopState) requestResolved(rpcID string) {
	s.a.mu.Lock()
	var r *pendingRequest
	for id, pr := range s.a.requests {
		if string(pr.rpcID) == rpcID {
			r = pr
			delete(s.a.requests, id)
			break
		}
	}
	answered, answer := false, ""
	if r != nil {
		answered, answer = r.answered, r.answer
	}
	s.a.mu.Unlock()
	if r == nil {
		return
	}
	if !answered {
		s.sink().RequestClosed(r.id)
	}
	if r.method != methodRequestUserInput || s.turn == nil {
		return
	}
	state := s.turn.items[r.toolUseID]
	if state == nil || state.done {
		return
	}
	result := toolResultBlock(r.toolUseID, "Not answered", "failed")
	if answered {
		result = toolResultBlock(r.toolUseID, answer, "completed")
	}
	s.finishTool(state, result, s.now())
}

// closeTurnRequests forgets the open requests when a turn ends, so a late
// answer is refused: the conversation closes all of them then (see
// native.Sink.TurnCompleted). app-server drops those of the turn itself,
// but an MCP elicitation, say, outlives the turn, so each request not
// answered is dismissed; app-server ignores the answer to a request it
// dropped.
func (s *loopState) closeTurnRequests() {
	s.a.mu.Lock()
	defer s.a.mu.Unlock()
	for id, r := range s.a.requests {
		delete(s.a.requests, id)
		if result, ok := r.dismissal(); ok && !r.answered {
			s.a.writeAsync(rpcResponse{JSONRPC: "2.0", ID: r.rpcID, Result: result})
		}
	}
}

// dismissal is the answer to r when nobody can answer it: an MCP
// elicitation is cancelled, an approval declined.
func (r *pendingRequest) dismissal() (any, bool) {
	id := "decline"
	if r.method == methodElicitation {
		id = "cancel"
	}
	result, ok := r.results[id]
	return result, ok
}
