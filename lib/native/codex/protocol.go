package codex

import "encoding/json"

// Hand-written types for the parts of the `codex app-server` protocol
// (codex-cli 0.160) the adapter uses, following the JSON schema that
// `codex app-server generate-json-schema` prints. Fields the adapter doesn't
// use are left out; unknown fields are ignored when decoding. A nullable
// string is a plain string (null decodes as "") unless null means something
// else.

// rpcMessage is any line on the wire. Server messages carry no "jsonrpc"
// field; notifications carry an extra emittedAtMs.
type rpcMessage struct {
	ID          json.RawMessage `json:"id,omitempty"`
	Method      string          `json:"method,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *rpcError       `json:"error,omitempty"`
	EmittedAtMs int64           `json:"emittedAtMs,omitempty"`
}

// rpcError is a JSON-RPC error. app-server uses -32600 for every error.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

// rpcRequest is a request or notification written to app-server.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse answers a server request.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// --- initialize ---

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type initializeCapabilities struct {
	// ExperimentalAPI opts into experimental methods and fields: the plan
	// collaboration mode.
	ExperimentalAPI           bool     `json:"experimentalApi,omitempty"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods,omitempty"`
}

type initializeParams struct {
	ClientInfo   clientInfo             `json:"clientInfo"`
	Capabilities initializeCapabilities `json:"capabilities"`
}

type initializeResponse struct {
	UserAgent string `json:"userAgent"`
}

// getAccountResponse is the result of account/read.
type getAccountResponse struct {
	Account            json.RawMessage `json:"account"`
	RequiresOpenaiAuth bool            `json:"requiresOpenaiAuth"`
}

// --- threads ---

// threadStartParams is ThreadStartParams. Policies are only sent when the
// user chose them on the command line; otherwise Codex's config decides.
type threadStartParams struct {
	CWD            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

// threadResumeParams is ThreadResumeParams.
type threadResumeParams struct {
	ThreadID       string `json:"threadId"`
	ExcludeTurns   bool   `json:"excludeTurns"`
	CWD            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
}

type thread struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cliVersion"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
}

// threadStartResponse is ThreadStartResponse and ThreadResumeResponse
// (the fields both have).
type threadStartResponse struct {
	Thread thread `json:"thread"`
	Model  string `json:"model"`
	CWD    string `json:"cwd"`
	// ApprovalPolicy is a string, or an object for a granular policy.
	ApprovalPolicy    json.RawMessage `json:"approvalPolicy"`
	ApprovalsReviewer string          `json:"approvalsReviewer"`
	Sandbox           sandboxPolicy   `json:"sandbox"`
	ReasoningEffort   *string         `json:"reasoningEffort"`
	// CollaborationMode is the resumed thread's (experimental).
	CollaborationMode *collaborationMode `json:"collaborationMode"`
}

// sandboxPolicy is SandboxPolicy, by its type: dangerFullAccess,
// workspaceWrite, readOnly or externalSandbox; the other fields keep their
// defaults.
type sandboxPolicy struct {
	Type string `json:"type"`
}

// modelListParams is ModelListParams.
type modelListParams struct {
	Cursor *string `json:"cursor,omitempty"`
}

// modelListResponse is ModelListResponse.
type modelListResponse struct {
	Data []struct {
		ID                        string `json:"id"`
		Model                     string `json:"model"`
		DisplayName               string `json:"displayName"`
		Description               string `json:"description"`
		Hidden                    bool   `json:"hidden"`
		DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
		SupportedReasoningEfforts []struct {
			ReasoningEffort string `json:"reasoningEffort"`
		} `json:"supportedReasoningEfforts"`
	} `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

type threadNameUpdatedNotification struct {
	ThreadID   string  `json:"threadId"`
	ThreadName *string `json:"threadName"`
}

type threadStatus struct {
	Type string `json:"type"`
}

type threadStatusChangedNotification struct {
	ThreadID string       `json:"threadId"`
	Status   threadStatus `json:"status"`
}

// --- turns ---

// userInput is one UserInput item: {type:"text",text} or
// {type:"localImage",path}.
type userInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	// Summary is "detailed" so reasoning summaries stream; model catalogues
	// may default it to "none", also for resumed threads.
	Summary string `json:"summary,omitempty"`
	// Settings changes, for this turn and the ones after it.
	ApprovalPolicy    string         `json:"approvalPolicy,omitempty"`
	SandboxPolicy     *sandboxPolicy `json:"sandboxPolicy,omitempty"`
	ApprovalsReviewer string         `json:"approvalsReviewer,omitempty"`
	Model             string         `json:"model,omitempty"`
	Effort            string         `json:"effort,omitempty"`
	// CollaborationMode switches to or from the plan mode (experimental).
	CollaborationMode *collaborationMode `json:"collaborationMode,omitempty"`
}

type threadCompactStartParams struct {
	ThreadID string `json:"threadId"`
}

type turnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []userInput `json:"input"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails"`
	// CodexErrorInfo is a code ("unauthorized") or an object keyed by the
	// kind of failure, with the upstream httpStatusCode.
	CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
}

type turn struct {
	ID     string     `json:"id"`
	Status string     `json:"status"` // inProgress | completed | interrupted | failed
	Error  *turnError `json:"error"`
}

type turnResponse struct {
	Turn turn `json:"turn"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     turn   `json:"turn"`
}

type errorNotification struct {
	Error     turnError `json:"error"`
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	WillRetry bool      `json:"willRetry"`
}

type tokenUsageBreakdown struct {
	TotalTokens       int64 `json:"totalTokens"`
	InputTokens       int   `json:"inputTokens"`
	CachedInputTokens int   `json:"cachedInputTokens"`
	OutputTokens      int   `json:"outputTokens"`
}

type threadTokenUsageUpdatedNotification struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Last               tokenUsageBreakdown `json:"last"`
		ModelContextWindow int64               `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

type turnPlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type turnPlanUpdatedNotification struct {
	ThreadID    string         `json:"threadId"`
	TurnID      string         `json:"turnId"`
	Explanation string         `json:"explanation"`
	Plan        []turnPlanStep `json:"plan"`
}

type modelReroutedNotification struct {
	ThreadID string `json:"threadId"`
	ToModel  string `json:"toModel"`
}

// --- items ---

// commandAction is Codex's reading of one part of a command.
type commandAction struct {
	Type    string `json:"type"` // read | listFiles | search | unknown
	Command string `json:"command"`
	Path    string `json:"path"`
	Query   string `json:"query"`
}

type fileUpdateChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string `json:"type"` // add | delete | update
		MovePath string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

type mcpToolCallResult struct {
	Content []json.RawMessage `json:"content"`
}

// threadItem is a ThreadItem; Type says which fields are set.
type threadItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// userMessage
	Content json.RawMessage `json:"content"` // UserInput[] (userMessage) or string[] (reasoning)

	// agentMessage, plan
	Text string `json:"text"`

	// reasoning
	Summary []string `json:"summary"`

	// commandExecution
	Command          string `json:"command"`
	CWD              string `json:"cwd"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
	Status           string `json:"status"`

	// fileChange
	Changes []fileUpdateChange `json:"changes"`

	// mcpToolCall, dynamicToolCall
	Server    string             `json:"server"`
	Tool      string             `json:"tool"`
	Namespace string             `json:"namespace"`
	Arguments json.RawMessage    `json:"arguments"`
	Result    *mcpToolCallResult `json:"result"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
	ContentItems []json.RawMessage `json:"contentItems"`
	Success      *bool             `json:"success"`

	// webSearch
	Query  string          `json:"query"`
	Action json.RawMessage `json:"action"`

	// imageView
	Path string `json:"path"`
}

// reasoningContent returns a reasoning item's raw reasoning texts. Content
// is a string array for reasoning items.
func (it *threadItem) reasoningContent() []string {
	var texts []string
	if len(it.Content) > 0 {
		_ = json.Unmarshal(it.Content, &texts)
	}
	return texts
}

// userTexts returns a userMessage's text inputs.
func (it *threadItem) userTexts() []string {
	var inputs []userInput
	if len(it.Content) > 0 {
		_ = json.Unmarshal(it.Content, &inputs)
	}
	var texts []string
	for _, in := range inputs {
		if in.Type == "text" && in.Text != "" {
			texts = append(texts, in.Text)
		}
	}
	return texts
}

type itemNotification struct {
	Item          threadItem `json:"item"`
	ThreadID      string     `json:"threadId"`
	TurnID        string     `json:"turnId"`
	StartedAtMs   int64      `json:"startedAtMs"`
	CompletedAtMs int64      `json:"completedAtMs"`
}

// deltaNotification covers item/agentMessage/delta, item/plan/delta,
// item/commandExecution/outputDelta and the reasoning deltas.
type deltaNotification struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	SummaryIndex int    `json:"summaryIndex"`
	ContentIndex int    `json:"contentIndex"`
}

// --- server requests ---

type commandExecutionRequestApprovalParams struct {
	TurnID                 string          `json:"turnId"`
	ItemID                 string          `json:"itemId"`
	Command                string          `json:"command"`
	CWD                    string          `json:"cwd"`
	CommandActions         []commandAction `json:"commandActions"`
	Reason                 string          `json:"reason"`
	NetworkApprovalContext *struct {
		Host     string `json:"host"`
		Protocol string `json:"protocol"`
	} `json:"networkApprovalContext"`
	ProposedExecpolicyAmendment     []string          `json:"proposedExecpolicyAmendment"`
	ProposedNetworkPolicyAmendments []json.RawMessage `json:"proposedNetworkPolicyAmendments"`
	// AvailableDecisions is not in the generated schema but is sent.
	AvailableDecisions []json.RawMessage `json:"availableDecisions"`
}

type fileChangeRequestApprovalParams struct {
	TurnID    string `json:"turnId"`
	ItemID    string `json:"itemId"`
	Reason    string `json:"reason"`
	GrantRoot string `json:"grantRoot"`
}

type userInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type userInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
	Options  []userInputOption `json:"options"`
}

type toolRequestUserInputParams struct {
	ThreadID  string              `json:"threadId"`
	TurnID    string              `json:"turnId"`
	ItemID    string              `json:"itemId"`
	Questions []userInputQuestion `json:"questions"`
}

type userInputAnswer struct {
	Answers []string `json:"answers"`
}

type toolRequestUserInputResponse struct {
	Answers map[string]userInputAnswer `json:"answers"`
}

type mcpElicitationProperty struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
	// EnumNames titles Enum in the legacy titled form.
	EnumNames []string            `json:"enumNames"`
	OneOf     []mcpElicitationOpt `json:"oneOf"`
	Items     *struct {
		Enum  []string            `json:"enum"`
		AnyOf []mcpElicitationOpt `json:"anyOf"`
	} `json:"items"`
}

type mcpElicitationOpt struct {
	Const string `json:"const"`
	Title string `json:"title"`
}

type mcpServerElicitationRequestParams struct {
	// TurnID is the turn app-server saw the elicitation in, if any.
	TurnID          string `json:"turnId"`
	ServerName      string `json:"serverName"`
	Mode            string `json:"mode"` // form | openai/form | openaiForm | url
	Message         string `json:"message"`
	URL             string `json:"url"`
	RequestedSchema *struct {
		Properties map[string]mcpElicitationProperty `json:"properties"`
	} `json:"requestedSchema"`
	Meta *struct {
		ApprovalKind    string          `json:"codex_approval_kind"`
		ToolDescription string          `json:"tool_description"`
		ToolParams      json.RawMessage `json:"tool_params"`
	} `json:"_meta"`
}

type mcpServerElicitationResponse struct {
	Action  string `json:"action"` // accept | decline | cancel
	Content any    `json:"content,omitempty"`
}

type permissionsRequestApprovalParams struct {
	TurnID      string          `json:"turnId"`
	ItemID      string          `json:"itemId"`
	Reason      string          `json:"reason"`
	Permissions json.RawMessage `json:"permissions"`
}

type permissionsRequestApprovalResponse struct {
	Permissions json.RawMessage `json:"permissions"`
	Scope       string          `json:"scope"` // turn | session
}

type serverRequestResolvedNotification struct {
	RequestID json.RawMessage `json:"requestId"`
}

type warningNotification struct {
	ThreadID string `json:"threadId"`
	Message  string `json:"message"`
}

type configWarningNotification struct {
	Summary string `json:"summary"`
	Details string `json:"details"`
}

type mcpServerStatusUpdatedNotification struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}
