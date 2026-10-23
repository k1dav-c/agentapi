package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/sessions"
	"github.com/coder/agentapi/lib/transcript"
	"github.com/coder/agentapi/lib/util"
)

// SubagentStatus is how far a subagent got.
type SubagentStatus string

const (
	SubagentRunning   SubagentStatus = "running"
	SubagentCompleted SubagentStatus = "completed"
	SubagentFailed    SubagentStatus = "failed"
	// SubagentUnknown: neither the subagent's log nor the agent's tool
	// calls say.
	SubagentUnknown SubagentStatus = "unknown"
)

var SubagentStatusValues = []SubagentStatus{SubagentRunning, SubagentCompleted, SubagentFailed, SubagentUnknown}

func (s SubagentStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "SubagentStatus", SubagentStatusValues)
}

// SubagentState is a subagent and how far it got.
type SubagentState struct {
	sessions.Subagent
	Status SubagentStatus `json:"status" doc:"running, completed or failed: for Claude Code from the Agent tool call that started it, for Codex from the subagent's own log; unknown when neither says."`
}

type SubagentsResponse struct {
	Body struct {
		Subagents []SubagentState `json:"subagents" nullable:"false" doc:"The subagents the agent ran in its session, in the order they started."`
	}
}

type SubagentPath struct {
	ID string `path:"id" doc:"The subagent's id, from GET /subagents."`
}

type SubagentResponse struct {
	Body struct {
		Subagent SubagentState `json:"subagent" doc:"The subagent."`
		Prompt   string        `json:"prompt" doc:"The task the agent gave the subagent."`
		// Messages are rich messages, like GET /rich-messages'.
		Messages []transcript.RichMessage `json:"messages" nullable:"false" doc:"What the subagent did, as rich messages: its thinking, tool calls with their results, and text."`
	}
}

// requireSubagents fails for a server whose agent's subagents can't be
// read: the native transport of Claude Code and Codex.
func (s *Server) requireSubagents() error {
	if err := s.requireNative(); err != nil {
		return err
	}
	if !sessions.HasHistory(string(s.agentType)) {
		return huma.Error400BadRequest("this agent's subagents can't be read")
	}
	return nil
}

// subagentCalls is what the agent's tool calls say of its subagents.
type subagentCalls struct {
	// statuses says how far the subagent each tool call started got, by
	// the call's result (Claude Code's Agent tool).
	statuses map[string]SubagentStatus
	// spawns maps a subagent's thread to the tool call that spawned it
	// (Codex's spawn_agent, whose input names the thread).
	spawns map[string]string
}

// subagentCalls reads the agent's tool calls for its subagents.
func (s *Server) subagentCalls() subagentCalls {
	statuses := make(map[string]SubagentStatus)
	spawns := make(map[string]string)
	for _, m := range s.emitter.RichMessages() {
		for _, b := range m.Content {
			switch {
			case b.Type == "tool_use" && b.ToolUseID != "":
				if _, ok := statuses[b.ToolUseID]; !ok {
					statuses[b.ToolUseID] = SubagentRunning
				}
				if b.ToolName == "spawn_agent" {
					var in struct {
						Threads []string `json:"receiver_thread_ids"`
					}
					if json.Unmarshal(b.ToolInput, &in) == nil {
						for _, thread := range in.Threads {
							spawns[thread] = b.ToolUseID
						}
					}
				}
			case b.Type == "tool_result" && b.ToolUseID != "":
				switch {
				case b.Status == "running":
					// A backgrounded subagent: its result came at once.
					statuses[b.ToolUseID] = SubagentRunning
				case b.Status == "failed" || (b.IsError != nil && *b.IsError):
					statuses[b.ToolUseID] = SubagentFailed
				default:
					statuses[b.ToolUseID] = SubagentCompleted
				}
			}
		}
	}
	return subagentCalls{statuses: statuses, spawns: spawns}
}

// stateOf says how far sub got: as its log says (Codex: its spawn call
// completes at once, while it goes on), else as the result of the tool call
// that started it.
func (c subagentCalls) stateOf(sub sessions.Subagent) SubagentState {
	if sub.ParentToolUseID == "" {
		sub.ParentToolUseID = c.spawns[sub.ID]
	}
	status, ok := c.statuses[sub.ParentToolUseID]
	switch {
	case sub.LogStatus != "":
		status = SubagentStatus(sub.LogStatus)
	case sub.ParentToolUseID == "" || !ok:
		status = SubagentUnknown
	}
	return SubagentState{Subagent: sub, Status: status}
}

// listSubagents handles GET /subagents, without s.mu: it reads the
// subagents' logs.
func (s *Server) listSubagents(_ context.Context, _ *struct{}) (*SubagentsResponse, error) {
	subs, err := sessions.Subagents(string(s.agentType), s.cwd, s.native.SessionRef())
	if err != nil {
		return nil, fmt.Errorf("failed to read the subagents: %w", err)
	}
	calls := s.subagentCalls()
	resp := &SubagentsResponse{}
	resp.Body.Subagents = make([]SubagentState, 0, len(subs))
	for _, sub := range subs {
		resp.Body.Subagents = append(resp.Body.Subagents, calls.stateOf(sub))
	}
	return resp, nil
}

// getSubagent handles GET /subagents/{id}.
func (s *Server) getSubagent(_ context.Context, input *SubagentPath) (*SubagentResponse, error) {
	sub, run, err := sessions.ReadSubagent(string(s.agentType), s.cwd, s.native.SessionRef(), input.ID)
	if errors.Is(err, sessions.ErrSubagentNotFound) {
		return nil, huma.Error404NotFound("the agent ran no such subagent in this session")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the subagent: %w", err)
	}
	for i := range run.Messages {
		// Images become URLs, as in the agent's own messages.
		s.storeRichImages(&run.Messages[i])
	}
	resp := &SubagentResponse{}
	resp.Body.Subagent = s.subagentCalls().stateOf(sub)
	resp.Body.Prompt = run.Prompt
	resp.Body.Messages = run.Messages
	if resp.Body.Messages == nil {
		resp.Body.Messages = []transcript.RichMessage{}
	}
	return resp, nil
}

func (s *Server) registerSubagentRoutes() {
	huma.Get(s.api, "/subagents", guard(s.requireSubagents, s.listSubagents), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "List the agent's subagents"
		o.Description = "Lists the subagents the agent ran in the session it has open, read from their own logs, in the order they started: each with what it was asked to do, the tool call that started it and whether it still runs (Claude Code). Native transport of Claude Code and Codex; Pi runs no subagents, and returns an empty list."
		o.Errors = []int{400}
	})
	huma.Get(s.api, "/subagents/{id}", guard(s.requireSubagents, s.getSubagent), func(o *huma.Operation) {
		o.Tags = []string{"Sessions"}
		o.Summary = "Get what a subagent did"
		o.Description = "Returns a subagent's task and what it did so far, as rich messages, read from its log; ask again for a running one. Native transport of Claude Code and Codex; returns 404 for a subagent the session has no log of."
		o.Errors = []int{400, 404}
	})
}
