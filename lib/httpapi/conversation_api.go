package httpapi

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	st "github.com/coder/agentapi/lib/screentracker"
)

// getStatus handles GET /status. It only reads the emitter, which has its
// own lock, so it answers while s.mu is held for a slow call into the agent
// (a restart, a native turn).
func (s *Server) getStatus(ctx context.Context, input *struct{}) (*StatusResponse, error) {
	snapshot := s.emitter.StatusSnapshot()

	resp := &StatusResponse{}
	resp.Body.Status = snapshot.Status
	resp.Body.Lifecycle = snapshot.Lifecycle
	resp.Body.SessionID = snapshot.SessionID
	resp.Body.RunID = snapshot.RunID
	resp.Body.AgentType = s.agentType
	resp.Body.Transport = s.transport
	resp.Body.Version = snapshot.Version
	resp.Body.TerminalPrompt = snapshot.TerminalPrompt
	resp.Body.TerminalColumns = snapshot.TerminalColumns
	resp.Body.SessionName = snapshot.SessionName
	resp.Body.PendingRequests = snapshot.PendingRequests
	resp.Body.Capabilities = snapshot.Capabilities
	resp.Body.LoginRequired = snapshot.LoginRequired
	resp.Body.Settings = snapshot.Settings
	resp.Body.Usage = snapshot.Usage
	resp.Body.Terminal = snapshot.Terminal
	resp.Body.Epoch = s.emitter.Epoch()

	return resp, nil
}

// getTitle handles GET /title. Like the other reads it doesn't take s.mu:
// the conversation has a lock of its own, and the read answers while s.mu
// is held for a slow call into the agent (a restart can take minutes).
func (s *Server) getTitle(ctx context.Context, input *struct{}) (*TitleResponse, error) {
	status := convertStatus(s.conversation.Status())
	task := ""
	messages := s.conversation.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == st.ConversationRoleUser {
			task = shortText(messages[i].Message, 60)
			break
		}
	}
	state := "Ready"
	if status == AgentStatusRunning {
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
	base := strings.TrimRight(os.Getenv("CODER_URL"), "/")
	owner := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_OWNER_NAME"))
	name := strings.TrimSpace(os.Getenv("CODER_WORKSPACE_NAME"))
	if base != "" && owner != "" && name != "" {
		response.Body.URL = base + "/@" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	}
	return &response, nil
}

// getMessages handles GET /messages, without s.mu (see getTitle).
func (s *Server) getMessages(ctx context.Context, input *struct{}) (*MessagesResponse, error) {
	resp := &MessagesResponse{}
	msgs := s.conversation.Messages()
	resp.Body.Messages = make([]Message, len(msgs))
	for i, msg := range msgs {
		resp.Body.Messages[i] = Message{
			Id:      msg.Id,
			Role:    msg.Role,
			Content: msg.Message,
			Time:    msg.Time,
		}
	}

	return resp, nil
}

// getRichMessages handles GET /rich-messages
func (s *Server) getRichMessages(ctx context.Context, input *struct{}) (*RichMessagesResponse, error) {
	resp := &RichMessagesResponse{}
	resp.Body.Messages = s.emitter.RichMessages()
	if resp.Body.Messages == nil {
		resp.Body.Messages = []jsonlwatcher.RichMessage{}
	}
	return resp, nil
}

func (s *Server) getTimeline(ctx context.Context, input *struct{}) (*TimelineResponse, error) {
	events := s.emitter.SessionEvents()
	if events == nil {
		events = []jsonlwatcher.SessionEvent{}
	}
	resp := &TimelineResponse{}
	resp.Body.Events = events
	return resp, nil
}

// createMessage handles POST /message
func (s *Server) createMessage(ctx context.Context, input *MessageRequest) (*MessageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, _ := s.pendingHandoffLocked()

	resp := &MessageResponse{}
	mode := input.Body.DispatchMode
	if mode == "" {
		mode = DispatchQueue
	}
	backend := s.backend()
	if mode != DispatchQueue && (!backend.dispatchModes() || input.Body.Type != MessageTypeUser) {
		return nil, huma.Error400BadRequest("dispatch_mode steer and interrupt need the native transport and a user message")
	}
	switch input.Body.Type {
	case MessageTypeUser:
		if strings.TrimSpace(input.Body.Content) == "" {
			return nil, huma.Error400BadRequest("message must not be empty")
		}
		if err := backend.deliver(input.Body.Content, mode, resp); err != nil {
			return nil, err
		}
		if resp.Body.Queued || resp.Body.Steered {
			// The message doesn't answer what the handoff offers, so it
			// goes on offering it.
			return resp, nil
		}
	case MessageTypeRaw:
		if s.agentio == nil {
			return nil, huma.Error400BadRequest("raw messages need a terminal; the native transport has none")
		}
		if _, err := s.agentio.Write([]byte(input.Body.Content)); err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	}

	resp.Body.Ok = true
	if pending != nil {
		s.handoffSuppressed = pending.ID
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
		o.Description = "Returns a list of messages representing the conversation history with the agent."
	})

	// GET /rich-messages endpoint
	huma.Get(s.api, "/rich-messages", s.getRichMessages, func(o *huma.Operation) {
		o.Description = "Returns a list of rich structured messages. " +
			"Each message contains structured content blocks (text, thinking, tool_use, tool_result), " +
			"model information, and token usage data. With the PTY transport they are parsed from the " +
			"session log of agents that have one (claude, codex and pi); with the native transport they " +
			"come from the agent's protocol."
	})

	huma.Get(s.api, "/timeline", s.getTimeline, func(o *huma.Operation) {
		o.Description = "Returns all normalized events from the current agent session, including text, thinking, tool calls, tool results, and system lifecycle events."
	})

	huma.Delete(s.api, "/messages", s.deleteMessages, func(o *huma.Operation) {
		o.Summary = "Delete all messages and restart"
		o.Description = "Clears all conversation state (messages, rich messages, timeline, errors) and restarts the agent in a new session (PTY or native transport). Returns 400 if restart is not supported (e.g. ACP transport)."
		o.Errors = []int{400}
	})

	// POST /message endpoint
	huma.Post(s.api, "/message", s.createMessage, func(o *huma.Operation) {
		o.Description = "Send a message to the agent. User messages are queued when the agent is busy."
	})
}
