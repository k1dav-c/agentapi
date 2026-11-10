package httpapi

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/transcript"
)

// getStatus handles GET /status. It only reads the emitter, which has its
// own lock, so it answers while s.mu is held for a slow call into the agent
// (a restart, a native turn).
func (s *Server) getStatus(ctx context.Context, input *struct{}) (*StatusResponse, error) {
	resp := &StatusResponse{}
	resp.Body.StatusChangeBody = s.emitter.StatusSnapshot()
	resp.Body.Epoch = s.emitter.Epoch()
	return resp, nil
}

// getTitle handles GET /title. Like the other reads it doesn't take s.mu:
// the conversation has a lock of its own, and the read answers while s.mu
// is held for a slow call into the agent (a restart can take minutes).
func (s *Server) getTitle(ctx context.Context, input *struct{}) (*TitleResponse, error) {
	status := convstate.ConvertStatus(s.native.Status())
	task := ""
	messages := s.native.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == conv.RoleUser {
			task = shortText(messages[i].Message, 60)
			break
		}
	}
	state := "Ready"
	if status == instanceapi.AgentStatusRunning {
		state = "Running"
	}
	title := state + " · AgentAPI"
	if task != "" {
		title = state + " · " + task + " — AgentAPI"
	}

	resp := &TitleResponse{}
	resp.Body.Title = title
	resp.Body.Task = task
	resp.Body.Status = status
	resp.Body.AgentType = s.agentType
	return resp, nil
}

// shortText returns s on one line, cut to at most n runes.
func shortText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n-1])) + "…"
}

func (s *Server) getWorkspace(_ context.Context, _ *struct{}) (*WorkspaceResponse, error) {
	var response WorkspaceResponse
	response.Body.URL = coderWorkspaceURL()
	return &response, nil
}

// coderWorkspaceURL is the dashboard page of the Coder workspace AgentAPI
// runs in, or empty outside one.
func coderWorkspaceURL() string {
	base := strings.TrimRight(os.Getenv("CODER_URL"), "/")
	owner := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_OWNER_NAME"))
	name := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_NAME"))
	if base == "" || owner == "" || name == "" {
		return ""
	}
	return base + "/@" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// getMessages handles GET /messages, without s.mu (see getTitle): the
// messages of a page of tasks (history.go).
func (s *Server) getMessages(ctx context.Context, input *PageInput) (*MessagesResponse, error) {
	if input == nil {
		input = &PageInput{}
	}
	r := s.emitter.Page(input.Before, input.Limit)
	msgs := s.emitter.MessagesIn(r)
	resp := &MessagesResponse{}
	resp.Body.Messages = make([]Message, len(msgs))
	for i, msg := range msgs {
		resp.Body.Messages[i] = Message{
			Id:      msg.Id,
			Role:    msg.Role,
			Content: msg.Message,
			Time:    msg.Time,
		}
	}
	resp.Body.Page = pageOf(r)
	return resp, nil
}

// getRichMessages handles GET /rich-messages: those of a page of tasks,
// from the state database when the server let go of them.
func (s *Server) getRichMessages(ctx context.Context, input *PageInput) (*RichMessagesResponse, error) {
	r := s.emitter.Page(input.Before, input.Limit)
	msgs, err := s.richMessagesIn(r)
	if err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("failed to read the rich messages: %v", err))
	}
	resp := &RichMessagesResponse{}
	resp.Body.Messages = msgs
	if resp.Body.Messages == nil {
		resp.Body.Messages = []transcript.RichMessage{}
	}
	resp.Body.Page = pageOf(r)
	return resp, nil
}

func (s *Server) getTimeline(ctx context.Context, input *TimelineRequest) (*TimelineResponse, error) {
	r := s.emitter.Page(input.Before, input.Limit)
	events, err := s.sessionEventsIn(r)
	if err != nil {
		return nil, huma.Error500InternalServerError(fmt.Sprintf("failed to read the timeline: %v", err))
	}
	if events == nil {
		events = []transcript.SessionEvent{}
	}
	if input.Subagents && s.sessions != nil && r.Before == 0 {
		events = append(events, s.subagentEvents(s.emitter.NextEventID())...)
	}
	resp := &TimelineResponse{}
	resp.Body.Events = events
	resp.Body.Page = pageOf(r)
	return resp, nil
}

// subagentEvents returns the events of the subagents the native agent ran in
// its session, numbered from next. A log that can't be read leaves them
// out rather than failing the timeline.
func (s *Server) subagentEvents(next int) []transcript.SessionEvent {
	sub, err := s.sessions.SubagentEvents(s.native.SessionRef())
	if err != nil {
		s.logger.Warn("Failed to read the subagents' logs", "error", err)
		return nil
	}
	spawns := s.subagentCalls().spawns
	for i := range sub {
		sub[i].EventID = next + i
		if sub[i].ParentToolUseID == nil && sub[i].SubagentID != nil {
			// A Codex subagent: the spawn_agent call that names its thread.
			if call, ok := spawns[*sub[i].SubagentID]; ok {
				sub[i].ParentToolUseID = &call
			}
		}
	}
	return sub
}

// createMessage handles POST /message
func (s *Server) createMessage(ctx context.Context, input *MessageRequest) (*MessageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, _ := s.pendingHandoff()

	resp := &MessageResponse{}
	mode := input.Body.DispatchMode
	if mode == "" {
		mode = DispatchQueue
	}
	if strings.TrimSpace(input.Body.Content) == "" {
		return nil, huma.Error400BadRequest("message must not be empty")
	}
	if s.turnGateLocked(turnUser, s.clock.Now()) != nil {
		// The new agent, or AgentAPI's new program, gets it once it runs.
		s.queueMessageLocked(resp, input.Body.Content, false)
		return resp, nil
	}
	if err := s.createNativeMessageLocked(input.Body.Content, mode, resp); err != nil {
		return nil, err
	}
	if resp.Body.Queued || resp.Body.Steered {
		// The message doesn't answer what the handoff offers, so it goes
		// on offering it.
		return resp, nil
	}

	resp.Body.Ok = true
	if pending != nil {
		s.handoff.suppress(pending.ID)
	}

	return resp, nil
}

func (s *Server) registerConversationRoutes() {
	// GET /status endpoint
	huma.Get(s.api, "/status", s.getStatus, func(o *huma.Operation) {
		o.Description = "Returns the current status of the agent."
	})
	huma.Get(s.api, "/title", s.getTitle, func(o *huma.Operation) {
		o.Description = "Returns the current human-readable session title and the server state used to derive it."
	})
	huma.Get(s.api, "/workspace", s.getWorkspace, func(o *huma.Operation) {
		o.Description = "Returns the Coder workspace URL injected into the process, when available."
	})

	// GET /messages endpoint
	huma.Get(s.api, "/messages", s.getMessages, func(o *huma.Operation) {
		o.Description = "Returns the messages of the conversation with the agent, a page of tasks at a time: without `before`, those of the latest tasks (20 by default, `limit` up to 100); " +
			"with `has_more`, pass `next_before` as `before` for the page before. AgentAPI 4.2 and earlier returned every message."
	})

	// GET /rich-messages endpoint
	huma.Get(s.api, "/rich-messages", s.getRichMessages, func(o *huma.Operation) {
		o.Description = "Returns the rich structured messages of a page of tasks, as GET /messages pages them. " +
			"Each message contains structured content blocks (text, thinking, tool_use, tool_result), " +
			"model information, and token usage data, from the agent's protocol. With --state-db, those of earlier tasks are read from the state database."
	})

	huma.Get(s.api, "/timeline", s.getTimeline, func(o *huma.Operation) {
		o.Description = "Returns the normalized events of a page of tasks of the current agent session, as GET /messages pages them, including text, thinking, tool calls, tool results, and system lifecycle events; GET /timeline/export streams them all. With `subagents=true`, the page of the latest tasks is followed by the events of the subagents the agent ran in the session, read from their own logs (native Claude Code and Codex): each subagent's in turn, starting with a system event, and each event with its `subagent_id` and, for Claude Code, the `parent_tool_use_id` of the Agent tool call that started it."
	})

	huma.Get(s.api, "/timeline/export", s.exportTimeline, func(o *huma.Operation) {
		o.Summary = "Download the whole timeline"
		o.Description = "Streams every normalized event of the agent session as JSON Lines, the earliest first: GET /timeline's events, of all the tasks. With --state-db they are read from the state database a part at a time, so the server doesn't hold them. With `subagents=true`, the events of the subagents follow, as in GET /timeline."
		o.Responses = map[string]*huma.Response{"200": {
			Description: "The events, one JSON object a line.",
			Content:     map[string]*huma.MediaType{"application/x-ndjson": {}},
		}}
	})

	huma.Delete(s.api, "/messages", s.deleteMessages, func(o *huma.Operation) {
		o.Summary = "Delete all messages and restart"
		o.Description = "Clears all conversation state (messages, rich messages, timeline, errors) and restarts the agent in a new session. Returns 409 while another restart is under way or AgentAPI stops. While the agent restarts, messages sent wait in the queue."
		o.Errors = []int{409}
	})

	// POST /message endpoint
	huma.Post(s.api, "/message", s.createMessage, func(o *huma.Operation) {
		o.Description = "Send a message to the agent. User messages are queued when the agent is busy."
	})
}
