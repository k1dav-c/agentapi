package httpapi

import (
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/util"
	"github.com/danielgtaylor/huma/v2"
)

type MessageType string

const (
	MessageTypeUser MessageType = "user"
	MessageTypeRaw  MessageType = "raw"
)

var MessageTypeValues = []MessageType{
	MessageTypeUser,
	MessageTypeRaw,
}

func (m MessageType) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "MessageType", MessageTypeValues)
}

type Transport string

const (
	TransportPTY    Transport = "pty"
	TransportACP    Transport = "acp"
	TransportNative Transport = "native"
)

var TransportValues = []Transport{
	TransportPTY,
	TransportACP,
	TransportNative,
}

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

func (tr Transport) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "Transport", TransportValues)
}

// Message represents a message
type Message struct {
	Id      int                 `json:"id" doc:"Unique identifier for the message. This identifier also represents the order of the message in the conversation history."`
	Content string              `json:"content" example:"Hello world" doc:"Message content. The message is formatted as it appears in the agent's terminal session, meaning that, by default, it consists of lines of text with 80 characters per line."`
	Role    st.ConversationRole `json:"role" doc:"Role of the message author"`
	Time    time.Time           `json:"time" doc:"Timestamp of the message"`
}

// StatusResponse represents the server status
type StatusResponse struct {
	Body struct {
		Status    AgentStatus    `json:"status" doc:"Current backward-compatible activity status."`
		Lifecycle LifecycleState `json:"lifecycle" doc:"Detailed process lifecycle state."`
		SessionID string         `json:"session_id" doc:"Identifier for this AgentAPI server session."`
		RunID     uint64         `json:"run_id" doc:"Monotonically increasing run identifier within the session."`
		AgentType mf.AgentType   `json:"agent_type" doc:"Type of the agent being used by the server."`
		Transport Transport      `json:"transport" doc:"Backend transport being used ('pty', 'acp' or 'native')."`
		Version   string         `json:"agentapi_version" doc:"Version of the AgentAPI server."`
		// See StatusChangeBody.TerminalPrompt.
		TerminalPrompt string `json:"terminal_prompt" doc:"Bottom of the terminal while the agent is showing an interactive prompt (selection list, confirmation dialog) that must be answered; empty otherwise."`
		// See StatusChangeBody.SessionName.
		SessionName string `json:"session_name,omitempty" doc:"Name the agent gave the session: Claude Code's title (or its /rename), Codex's thread name or Pi's /name. Empty until the agent names it."`
		// See StatusChangeBody.TerminalColumns.
		TerminalColumns uint16 `json:"terminal_columns,omitempty" doc:"Width of the emulated terminal the agent runs in, in columns (PTY transport only)."`
		// See StatusChangeBody.PendingRequests.
		PendingRequests int `json:"pending_requests,omitempty" doc:"Number of requests (approvals, questions, plans) the agent is waiting on. Native transport only; see GET /requests."`
		// See StatusChangeBody.Capabilities.
		Capabilities *native.Capabilities `json:"capabilities,omitempty" doc:"Optional operations the agent supports. Native transport only."`
		// Epoch lets a client resume /events without a cache.
		Epoch string `json:"epoch" doc:"Identifies the current conversation state; pass it as epoch to /events?sync=1 (see session_sync)."`
	}
}

// TitleResponse describes the current session title and the state used to
// derive it. Connection-only states such as browser offline/reconnecting are
// intentionally not represented because they are client-local.
type TitleResponse struct {
	Body struct {
		Title     string       `json:"title" doc:"Current human-readable session title derived from the latest user task and agent status."`
		Task      string       `json:"task" doc:"Latest user task used to derive the title. Empty before the first task."`
		Status    AgentStatus  `json:"status" doc:"Current agent status."`
		AgentType mf.AgentType `json:"agent_type" doc:"Type of the agent being used by the server."`
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
	DispatchMode DispatchMode `json:"dispatch_mode,omitempty" doc:"What to do with a user message while the agent is busy. 'queue' (default) sends it once the agent is idle. 'steer' adds it to the turn the agent is working on; 'interrupt' stops that turn and sends the message next. 'steer' and 'interrupt' need the native transport and an agent with the matching capability (see GET /status); otherwise they return 400."`
	Type         MessageType  `json:"type" doc:"A 'user' type message will be logged as a user message in the conversation history and submitted to the agent. AgentAPI will wait until the agent starts carrying out the task described in the message before responding. A 'raw' type message will be written directly to the agent's terminal session as keystrokes and will not be saved in the conversation history. 'raw' messages are useful for sending escape sequences to the terminal."`
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

type QueuedMessage struct {
	ID      int       `json:"id" doc:"Unique identifier for the queued message."`
	Content string    `json:"content" doc:"Message content."`
	Time    time.Time `json:"time" doc:"Timestamp when the message was queued."`
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
		Requests []native.RequestState `json:"requests" nullable:"false" doc:"Pending requests and the most recently closed ones, oldest first."`
	}
}

type RequestPath struct {
	ID string `path:"id" doc:"Request identifier."`
}

type RequestResponse struct {
	Body native.RequestState
}

type RespondRequest struct {
	ID   string `path:"id" doc:"Request identifier."`
	Body native.Response
}

type InterruptResponse struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

type QueueMutationResponse struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// RichMessagesResponse represents the list of rich structured messages
type RichMessagesResponse struct {
	Body struct {
		Messages []jsonlwatcher.RichMessage `json:"messages" nullable:"false" doc:"List of rich messages with structured content blocks, model info, and usage"`
	}
}

type TimelineResponse struct {
	Body struct {
		Events []jsonlwatcher.SessionEvent `json:"events" nullable:"false" doc:"Timeline events including text, thinking, tool calls, tool results, and system lifecycle events."`
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
