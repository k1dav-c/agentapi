package instanceapi

import (
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/util"
)

// AgentStatus is whether the agent works or waits.
type AgentStatus string

const (
	AgentStatusRunning AgentStatus = "running"
	AgentStatusStable  AgentStatus = "stable"
)

var AgentStatusValues = []AgentStatus{
	AgentStatusStable,
	AgentStatusRunning,
}

func (a AgentStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "AgentStatus", AgentStatusValues)
}

// LifecycleState is where the agent's process is in its life.
type LifecycleState string

const (
	LifecycleStarting   LifecycleState = "starting"
	LifecycleReady      LifecycleState = "ready"
	LifecycleRunning    LifecycleState = "running"
	LifecycleRestarting LifecycleState = "restarting"
	LifecycleExited     LifecycleState = "exited"
	LifecycleFailed     LifecycleState = "failed"
)

var LifecycleStateValues = []LifecycleState{
	LifecycleStarting, LifecycleReady, LifecycleRunning,
	LifecycleRestarting, LifecycleExited, LifecycleFailed,
}

func (l LifecycleState) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "LifecycleState", LifecycleStateValues)
}

// TaskStatus is where a task is in its life.
type TaskStatus string

const (
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusFailed: the agent reported an error during the task, or
	// exited.
	TaskStatusFailed TaskStatus = "failed"
	// TaskStatusLimited: a usage limit stopped the task.
	TaskStatusLimited TaskStatus = "limited"
)

var TaskStatusValues = []TaskStatus{TaskStatusRunning, TaskStatusCompleted, TaskStatusFailed, TaskStatusLimited}

func (s TaskStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "TaskStatus", TaskStatusValues)
}

// Task is a message of the user's and the agent's turn on it, until the
// agent is done: stable again, with no request or terminal dialog open.
// Messages the user adds while it runs (steering) belong to it.
type Task struct {
	ID        int        `json:"id" doc:"The ID of the user's message that started the task, as in GET /messages."`
	Status    TaskStatus `json:"status" doc:"running, completed, failed (the agent reported an error or exited) or limited (a usage limit stopped it)."`
	Prompt    string     `json:"prompt" doc:"The start of the user's message (up to 1 KiB); GET /messages has all of it."`
	StartedAt time.Time  `json:"started_at" doc:"When the user's message was sent."`
	EndedAt   *time.Time `json:"ended_at,omitempty" doc:"When the agent was done; missing while it runs."`
	// ReplyID is the agent's last message of the task.
	ReplyID *int `json:"reply_id,omitempty" doc:"The ID of the agent's last message of the task, as in GET /messages."`
	// Steered are the user's messages added while it ran.
	Steered []int `json:"steered,omitempty" doc:"The IDs of the user's messages added to the task while it ran (steering)."`
	// Errors the agent reported during the task.
	Errors []string `json:"errors,omitempty" doc:"Errors the agent reported during the task."`
	// Limit is the usage limit that stopped a limited task.
	Limit *native.LimitStop `json:"limit,omitempty" doc:"The usage limit stop of a limited task."`
}

// Final reports whether the task ended.
func (t Task) Final() bool { return t.Status != TaskStatusRunning }

// Clone returns a copy of t that shares none of its lists.
func (t Task) Clone() Task {
	t.Errors = slices.Clone(t.Errors)
	t.Steered = slices.Clone(t.Steered)
	return t
}

// Attention is why the agent waits for the user, if it does.
type Attention string

const (
	AttentionNone Attention = "none"
	// AttentionLogin: the agent can't work until the user logs in.
	AttentionLogin Attention = "login"
	// AttentionRequest: an approval, question or plan waits for an answer.
	AttentionRequest Attention = "request"
	// AttentionDialog: the agent's terminal shows a dialog to answer.
	AttentionDialog Attention = "dialog"
	// AttentionLimit: a usage limit stopped the agent's turn.
	AttentionLimit Attention = "limit"
	// AttentionExited: the agent exited.
	AttentionExited Attention = "exited"
)

var AttentionValues = []Attention{
	AttentionNone, AttentionLogin, AttentionRequest, AttentionDialog, AttentionLimit, AttentionExited,
}

func (a Attention) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "Attention", AttentionValues)
}

// SummaryBody is what a client that watches many AgentAPIs shows of one:
// who it is, whether it works or waits for the user, and its latest task.
type SummaryBody struct {
	Instance WebhookInstance `json:"instance" doc:"Who this AgentAPI is: its ID (--instance-id), name (--name), server ID, host, port, directory, agent and URL, as in webhook events."`
	Status   AgentStatus     `json:"status" doc:"Whether the agent works (running) or waits (stable)."`
	// Lifecycle tells a failed or exited agent from one that waits.
	Lifecycle LifecycleState `json:"lifecycle" doc:"The agent process's lifecycle state."`
	Attention Attention      `json:"attention" doc:"Why the agent waits for the user: login, request (an approval, question or plan), dialog (in its terminal), limit (a usage limit stopped its turn), exited, or none."`
	// Title is the session's name, else the start of the latest prompt.
	Title          string `json:"title" doc:"The name the agent gave the session, else the start of the latest task's prompt; empty before the first task."`
	SessionName    string `json:"session_name,omitempty" doc:"The name the agent gave the session, as in GET /status."`
	AgentSessionID string `json:"agent_session_id,omitempty" doc:"The agent's own session, as in GET /status."`
	// PendingRequests and QueuedMessages count what waits.
	PendingRequests int    `json:"pending_requests" doc:"Requests (approvals, questions, plans) the agent waits on; see GET /requests."`
	QueuedMessages  int    `json:"queued_messages" doc:"Messages waiting in the queue; see GET /queue."`
	TerminalPrompt  string `json:"terminal_prompt,omitempty" doc:"The bottom of the agent's terminal while it shows a dialog to answer, as in GET /status."`
	// Task is the latest task, nil before the first.
	Task  *Task `json:"task,omitempty" doc:"The latest task, as in GET /tasks; missing before the first."`
	Tasks int   `json:"tasks" doc:"How many tasks the conversation has."`
	// Unread: the latest task ended after a client last showed the
	// conversation (POST /tasks/seen).
	Unread        bool                  `json:"unread" doc:"The latest task ended after a client last showed the conversation to the user (POST /tasks/seen): its result wasn't seen yet."`
	SeenAt        time.Time             `json:"seen_at" doc:"When a client last showed the conversation to the user (POST /tasks/seen); when AgentAPI started, before any did."`
	LoginRequired *native.LoginRequired `json:"login_required,omitempty" doc:"Set while the agent waits for the user to log in, as in GET /status."`
	LimitStop     *native.LimitStop     `json:"limit_stop,omitempty" doc:"Set while a usage limit holds the agent, as in GET /status."`
	Usage         *native.AgentUsage    `json:"usage,omitempty" doc:"The context window and the account's usage limits, as in GET /status."`
	// Request is the oldest pending request, what a client shows of what
	// the agent waits for without opening it.
	Request *SummaryRequest `json:"request,omitempty" doc:"The oldest request the agent waits on (see GET /requests); missing when none waits."`
	// Spend is what the agent's sessions cost recently.
	Spend *SummarySpend `json:"spend,omitempty" doc:"What the agent's sessions in this directory cost over the last 24 hours and 7 days, at list prices, worked out once a minute; missing for agents whose session logs AgentAPI doesn't read (PTY) and until it is worked out."`
}

// SummaryRequest is what a summary says of a pending request.
type SummaryRequest struct {
	ID        string             `json:"id" doc:"The request's ID, as in GET /requests."`
	Kind      native.RequestKind `json:"kind" doc:"approval, question or plan."`
	Title     string             `json:"title" doc:"One-line summary, such as the command to run."`
	CreatedAt time.Time          `json:"created_at" doc:"When the agent asked."`
}

// SummarySpend is what the agent's sessions in its working directory cost
// recently.
type SummarySpend struct {
	DayUSD  float64 `json:"day_usd" doc:"API-equivalent cost in USD of the agent's sessions in this directory, subagents included, over the last 24 hours (the hours that end within them): the tokens at the model providers' list prices, which a subscription doesn't charge."`
	WeekUSD float64 `json:"week_usd" doc:"The same over the last 7 days."`
}
