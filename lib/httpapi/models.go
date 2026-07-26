package httpapi

import (
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
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
	TransportPTY Transport = "pty"
	TransportACP Transport = "acp"
)

var TransportValues = []Transport{
	TransportPTY,
	TransportACP,
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
		Status    AgentStatus  `json:"status" doc:"Current agent status. 'running' means that the agent is processing a message, 'stable' means that the agent is idle and waiting for input."`
		AgentType mf.AgentType `json:"agent_type" doc:"Type of the agent being used by the server."`
		Transport Transport    `json:"transport" doc:"Backend transport being used ('acp' or 'pty')."`
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

type BackgroundTask struct {
	ID         string    `json:"id" doc:"Agent-provided background task or session identifier."`
	Name       string    `json:"name" doc:"Human-readable task name derived from the tool call."`
	Status     string    `json:"status" doc:"Best-known task status: running, completed, failed, or unknown."`
	AgentType  string    `json:"agent_type" doc:"Agent family whose tool metadata produced the task."`
	ToolUseID  string    `json:"tool_use_id" doc:"Structured tool call identifier."`
	OutputPath string    `json:"output_path,omitempty" doc:"Discovered output file path when the agent reported one."`
	StartedAt  time.Time `json:"started_at" doc:"Timestamp of the background tool call."`
	UpdatedAt  time.Time `json:"updated_at" doc:"Timestamp of the latest related tool result."`
	Output     string    `json:"-"`
}

type BackgroundTasksResponse struct {
	Body struct {
		Tasks []BackgroundTask `json:"tasks" nullable:"false" doc:"Background tasks discovered from structured agent tool metadata."`
	}
}

type BackgroundTaskOutputRequest struct {
	ID   string `path:"id" doc:"Background task identifier."`
	Tail int    `query:"tail" minimum:"1" maximum:"1048576" default:"131072" doc:"Maximum number of trailing bytes to return."`
}

type BackgroundTaskOutputResponse struct {
	Body struct {
		TaskID    string `json:"task_id"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		Size      int64  `json:"size"`
		Truncated bool   `json:"truncated"`
	}
}

// MessagesResponse represents the list of messages
type MessagesResponse struct {
	Body struct {
		Messages []Message `json:"messages" nullable:"false" doc:"List of messages"`
	}
}

type MessageRequestBody struct {
	Content string      `json:"content" example:"Hello, agent!" doc:"Message content"`
	Type    MessageType `json:"type" doc:"A 'user' type message will be logged as a user message in the conversation history and submitted to the agent. AgentAPI will wait until the agent starts carrying out the task described in the message before responding. A 'raw' type message will be written directly to the agent's terminal session as keystrokes and will not be saved in the conversation history. 'raw' messages are useful for sending escape sequences to the terminal."`
}

// MessageRequest represents a request to create a new message
type MessageRequest struct {
	Body MessageRequestBody `json:"body" doc:"Message content and type"`
}

// MessageResponse represents a newly created message
type MessageResponse struct {
	Body struct {
		Ok     bool `json:"ok" doc:"Indicates whether the message was accepted."`
		Queued bool `json:"queued" doc:"Indicates whether a user message was queued because the agent was busy."`
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
