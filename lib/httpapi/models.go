package httpapi

import (
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
	"github.com/coder/agentapi/lib/util"
)

type MessageType string

// MessageTypeUser is the one message type; AgentAPI 2 also had raw,
// keystrokes for the agent's terminal.
const MessageTypeUser MessageType = "user"

var MessageTypeValues = []MessageType{
	MessageTypeUser,
}

func (m MessageType) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "MessageType", MessageTypeValues)
}

// AgentType and Transport are part of the instance's contract
// (lib/instanceapi).
type (
	AgentType = instanceapi.AgentType
	Transport = instanceapi.Transport
)

const (
	AgentTypeClaude = instanceapi.AgentTypeClaude
	AgentTypeCodex  = instanceapi.AgentTypeCodex
	AgentTypePi     = instanceapi.AgentTypePi
	AgentTypeCustom = instanceapi.AgentTypeCustom
	TransportNative = instanceapi.TransportNative
)

var TransportValues = instanceapi.TransportValues

// DispatchMode says what to do with a user message while the agent is busy.
type DispatchMode string

const (
	// DispatchQueue sends the message now if the agent is idle and queues
	// it otherwise.
	DispatchQueue DispatchMode = "queue"
	// DispatchSteer adds the message to the turn the agent is working on,
	// or sends it as a new turn if the agent is idle.
	DispatchSteer DispatchMode = "steer"
	// DispatchInterrupt stops the turn the agent is working on and sends the
	// message next.
	DispatchInterrupt DispatchMode = "interrupt"
)

var DispatchModeValues = []DispatchMode{DispatchQueue, DispatchSteer, DispatchInterrupt}

func (d DispatchMode) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "DispatchMode", DispatchModeValues)
}

// Message represents a message
type Message struct {
	Id      int       `json:"id" doc:"Unique identifier for the message. This identifier also represents the order of the message in the conversation history."`
	Content string    `json:"content" example:"Hello world" doc:"Message content. The message is formatted as it appears in the agent's terminal session, meaning that, by default, it consists of lines of text with 80 characters per line."`
	Role    conv.Role `json:"role" doc:"Role of the message author"`
	Time    time.Time `json:"time" doc:"Timestamp of the message"`
}

// StatusResponse represents the server status: what status_change events
// carry, and the epoch.
type StatusResponse struct {
	Body struct {
		StatusChangeBody
		// Epoch lets a client resume /events without a cache.
		Epoch string `json:"epoch" doc:"Identifies the current conversation state; pass it as epoch to /events?sync=1 (see session_sync)."`
	}
}

// TitleResponse describes the current session title and the state used to
// derive it. Connection-only states such as browser offline/reconnecting are
// intentionally not represented because they are client-local.
type TitleResponse struct {
	Body struct {
		Title     string                `json:"title" doc:"Current human-readable session title derived from the latest user task and agent status."`
		Task      string                `json:"task" doc:"Latest user task used to derive the title. Empty before the first task."`
		Status    AgentStatus           `json:"status" doc:"Current agent status."`
		AgentType instanceapi.AgentType `json:"agent_type" doc:"Type of the agent being used by the server."`
	}
}

// WorkspaceResponse exposes the non-secret Coder workspace link injected into
// an AgentAPI process. It is empty outside Coder.
type WorkspaceResponse struct {
	Body struct {
		URL string `json:"url" doc:"Coder workspace URL, when running inside a Coder workspace."`
	}
}

// MessagesResponse represents the list of messages
type MessagesResponse struct {
	Body struct {
		Messages []Message `json:"messages" nullable:"false" doc:"List of messages"`
	}
}

type MessageRequestBody struct {
	Content      string       `json:"content" example:"Hello, agent!" doc:"Message content"`
	DispatchMode DispatchMode `json:"dispatch_mode,omitempty" doc:"What to do with a user message while the agent is busy. 'queue' (default) sends it once the agent is idle. 'steer' adds it to the turn the agent is working on; 'interrupt' stops that turn and sends the message next. Without a running turn, both send the message as usual. 'steer' and 'interrupt' need, while a turn runs, an agent with the matching capability (see GET /status); otherwise they return 400."`
	Type         MessageType  `json:"type" doc:"Always 'user': the message is logged as a user message in the conversation history and submitted to the agent. AgentAPI 2 also took 'raw', keystrokes for the agent's terminal."`
}

// MessageRequest represents a request to create a new message
type MessageRequest struct {
	Body MessageRequestBody `json:"body" doc:"Message content and type"`
}

// MessageResponse represents a newly created message
type MessageResponse struct {
	Body struct {
		Ok      bool `json:"ok" doc:"Indicates whether the message was accepted."`
		Queued  bool `json:"queued" doc:"Indicates whether a user message was queued because the agent was busy."`
		Steered bool `json:"steered,omitempty" doc:"Indicates whether the message was added to the turn the agent was working on."`
	}
}

type QueueResponse struct {
	Body struct {
		Messages []QueuedMessage `json:"messages" nullable:"false" doc:"Messages waiting to be sent to the agent."`
	}
}

type UpdateQueuedMessageRequest struct {
	ID   int `path:"id" doc:"Queued message identifier."`
	Body struct {
		Content string `json:"content" minLength:"1" doc:"Updated message content."`
	}
}

type DeleteQueuedMessageRequest struct {
	ID int `path:"id" doc:"Queued message identifier."`
}

type SteerQueuedMessageRequest struct {
	ID int `path:"id" doc:"Queued message identifier."`
}

type ReorderQueueRequest struct {
	Body struct {
		IDs []int `json:"ids" nullable:"false" doc:"Identifiers of all queued messages in the new order."`
	}
}

type RequestsResponse struct {
	Body struct {
		Requests []agentstate.RequestState `json:"requests" nullable:"false" doc:"Pending requests and the most recently closed ones, oldest first."`
	}
}

type RequestPath struct {
	ID string `path:"id" doc:"Request identifier."`
}

type RequestResponse struct {
	Body agentstate.RequestState
}

type RespondRequest struct {
	ID   string `path:"id" doc:"Request identifier."`
	Body agentstate.Response
}

// SettingsResponse is the agent's settings and the values they can take.
type SettingsResponse struct {
	Body struct {
		Settings agentstate.Settings   `json:"settings" doc:"Current settings; a setting the agent doesn't report is missing."`
		Options  native.SettingOptions `json:"options" doc:"Values the settings can take."`
	}
}

type UpdateSettingsRequest struct {
	Body agentstate.Settings
}

type InterruptResponse struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// okResponse is an InterruptResponse that says the request succeeded.
func okResponse() *InterruptResponse {
	resp := &InterruptResponse{}
	resp.Body.Ok = true
	return resp
}

type QueueMutationResponse struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// RichMessagesResponse represents the list of rich structured messages
type RichMessagesResponse struct {
	Body struct {
		Messages []transcript.RichMessage `json:"messages" nullable:"false" doc:"List of rich messages with structured content blocks, model info, and usage"`
	}
}

// TimelineRequest is GET /timeline's query.
type TimelineRequest struct {
	Subagents bool `query:"subagents" doc:"Also return the events of the subagents the agent ran in the session (native Claude Code and Codex)."`
}

type TimelineResponse struct {
	Body struct {
		Events []transcript.SessionEvent `json:"events" nullable:"false" doc:"Timeline events including text, thinking, tool calls, tool results, and system lifecycle events."`
	}
}

type UploadResponse struct {
	Body struct {
		Ok       bool   `json:"ok" doc:"Indicates whether the files were uploaded successfully."`
		FilePath string `json:"filePath" doc:"Path of the file"`
	}
}

type UploadRequest struct {
	File huma.FormFile `form:"file" required:"true" doc:"file that needs to be uploaded"`
}
