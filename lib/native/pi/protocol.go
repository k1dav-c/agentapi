package pi

import "encoding/json"

// Pi's RPC records (`pi --mode rpc`, Pi 1.0). Optional fields are omitted
// by Pi, never null.

// envelope is the part every stdout record has.
type envelope struct {
	Type string `json:"type"`
}

// response answers a command with the same id.
type response struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// promptResult is the data of a prompt response.
type promptResult struct {
	// Disposition is "started" (a run started), "queued" (added to the
	// running run) or "handled" (an extension command ran; no run).
	Disposition string `json:"disposition"`
}

// sessionState is the data of a get_state response.
type sessionState struct {
	SessionID   string   `json:"sessionId"`
	SessionFile string   `json:"sessionFile"`
	SessionName string   `json:"sessionName"`
	IsStreaming bool     `json:"isStreaming"`
	Model       *piModel `json:"model"`
	// ThinkingLevel is one of thinkingLevels.
	ThinkingLevel string `json:"thinkingLevel"`
}

// messageEvent is message_start, message_update and message_end.
type messageEvent struct {
	Message               json.RawMessage `json:"message"`
	AssistantMessageEvent *streamEvent    `json:"assistantMessageEvent"`
}

// streamEvent is the assistantMessageEvent of a message_update.
type streamEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
	Content      string `json:"content"`
	ID           string `json:"id"`
	ToolName     string `json:"toolName"`
	ToolCall     *struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"toolCall"`
}

// agentMessage holds the fields of a Pi message the adapter looks at. The
// whole message goes to jsonlwatcher's Pi parser.
type agentMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Timestamp    int64           `json:"timestamp"`
	ToolCallID   string          `json:"toolCallId"`
}

// contentBlock is a block of a message's or tool result's content.
type contentBlock struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// toolEvent is tool_execution_start, tool_execution_update and
// tool_execution_end.
type toolEvent struct {
	ToolCallID    string          `json:"toolCallId"`
	ToolName      string          `json:"toolName"`
	Args          json.RawMessage `json:"args"`
	PartialResult *toolResult     `json:"partialResult"`
	Result        *toolResult     `json:"result"`
	IsError       bool            `json:"isError"`
}

type toolResult struct {
	Content json.RawMessage `json:"content"`
	IsError bool            `json:"isError"`
}

// uiRequest is an extension_ui_request.
type uiRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Title  string `json:"title"`
	// Message is the text of confirm and notify.
	Message     string   `json:"message"`
	Options     []string `json:"options"`
	Placeholder string   `json:"placeholder"`
	Prefill     string   `json:"prefill"`
	// Timeout in milliseconds after which Pi answers the dialog with its
	// default itself; 0 means no timeout.
	Timeout    int64  `json:"timeout"`
	NotifyType string `json:"notifyType"`
	StatusKey  string `json:"statusKey"`
	StatusText string `json:"statusText"`
	WidgetKey  string `json:"widgetKey"`
}

// uiResponse is an extension_ui_response.
type uiResponse struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

// imageContent is an image attached to a prompt.
type imageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// command is a command that gets a response with the same id.
type command interface {
	// withID returns the command with its id set.
	withID(id string) any
}

// promptCommand is prompt; StreamingBehavior "steer" adds the message to
// the running run, or starts one when Pi is idle.
type promptCommand struct {
	ID                string         `json:"id"`
	Type              string         `json:"type"`
	Message           string         `json:"message"`
	Images            []imageContent `json:"images,omitempty"`
	StreamingBehavior string         `json:"streamingBehavior,omitempty"`
}

func (c promptCommand) withID(id string) any {
	c.ID = id
	return c
}

// simpleCommand is a command without arguments (get_state, abort).
type simpleCommand struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (c simpleCommand) withID(id string) any {
	c.ID = id
	return c
}

// sessionInfoChanged is session_info_changed.
type sessionInfoChanged struct {
	Name string `json:"name"`
}

// autoRetryEnd is auto_retry_end.
type autoRetryEnd struct {
	Success    bool   `json:"success"`
	FinalError string `json:"finalError"`
}

// compactionEnd is compaction_end.
type compactionEnd struct {
	Aborted      bool   `json:"aborted"`
	ErrorMessage string `json:"errorMessage"`
}

// extensionError is extension_error.
type extensionError struct {
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
}
