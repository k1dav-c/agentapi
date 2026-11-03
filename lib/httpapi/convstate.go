package httpapi

import "github.com/coder/agentapi/lib/convstate"

// The conversation state lives in lib/convstate. These names keep the
// OpenAPI schema's, which come from the Go types' names, and the API of
// this package's callers.
type (
	EventEmitter          = convstate.EventEmitter
	Event                 = convstate.Event
	EventType             = convstate.EventType
	Persister             = convstate.Persister
	EmitterState          = convstate.EmitterState
	MessageUpdateBody     = convstate.MessageUpdateBody
	SessionSyncBody       = convstate.SessionSyncBody
	StatusChangeBody      = convstate.StatusChangeBody
	ErrorBody             = convstate.ErrorBody
	RichMessageUpdateBody = convstate.RichMessageUpdateBody
	RequestUpdateBody     = convstate.RequestUpdateBody
	HeartbeatBody         = convstate.HeartbeatBody
	TaskUpdateBody        = convstate.TaskUpdateBody
	SummaryUpdateBody     = convstate.SummaryUpdateBody
	QueuedMessage         = convstate.QueuedMessage
	QueueUpdateBody       = convstate.QueueUpdateBody

	AgentStatus    = convstate.AgentStatus
	LifecycleState = convstate.LifecycleState
	Task           = convstate.Task
	TaskStatus     = convstate.TaskStatus
	Attention      = convstate.Attention
	SummaryBody    = convstate.SummaryBody
	SummaryRequest = convstate.SummaryRequest
	SummarySpend   = convstate.SummarySpend
)

const (
	EventTypeMessageUpdate     = convstate.EventTypeMessageUpdate
	EventTypeStatusChange      = convstate.EventTypeStatusChange
	EventTypeError             = convstate.EventTypeError
	EventTypeRichMessageUpdate = convstate.EventTypeRichMessageUpdate
	EventTypeHeartbeat         = convstate.EventTypeHeartbeat
	EventTypeSessionSync       = convstate.EventTypeSessionSync
	EventTypeRequestUpdate     = convstate.EventTypeRequestUpdate
	EventTypeTaskUpdate        = convstate.EventTypeTaskUpdate
	EventTypeQueueUpdate       = convstate.EventTypeQueueUpdate
	EventTypeSummaryUpdate     = convstate.EventTypeSummaryUpdate

	AgentStatusRunning = convstate.AgentStatusRunning
	AgentStatusStable  = convstate.AgentStatusStable

	LifecycleStarting   = convstate.LifecycleStarting
	LifecycleReady      = convstate.LifecycleReady
	LifecycleRunning    = convstate.LifecycleRunning
	LifecycleRestarting = convstate.LifecycleRestarting
	LifecycleExited     = convstate.LifecycleExited
	LifecycleFailed     = convstate.LifecycleFailed

	TaskStatusRunning   = convstate.TaskStatusRunning
	TaskStatusCompleted = convstate.TaskStatusCompleted
	TaskStatusFailed    = convstate.TaskStatusFailed
	TaskStatusLimited   = convstate.TaskStatusLimited

	AttentionNone    = convstate.AttentionNone
	AttentionLogin   = convstate.AttentionLogin
	AttentionRequest = convstate.AttentionRequest
	AttentionDialog  = convstate.AttentionDialog
	AttentionLimit   = convstate.AttentionLimit
	AttentionExited  = convstate.AttentionExited
)

var (
	AgentStatusValues    = convstate.AgentStatusValues
	LifecycleStateValues = convstate.LifecycleStateValues
	TaskStatusValues     = convstate.TaskStatusValues
	AttentionValues      = convstate.AttentionValues
)
