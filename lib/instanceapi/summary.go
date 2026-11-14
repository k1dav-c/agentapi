package instanceapi

import (
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/agentstate"
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
// agent is done: stable again, with no request open.
// Messages the user adds while it runs (steering) belong to it.
type Task struct {
	ID int `json:"id" doc:"The ID of the user's message that started the task, as in GET /messages."`
	// Number is the task's place in the conversation, from 1. The
	// emitter sets it on the tasks it hands out; the state database
	// doesn't keep it.
	Number    int        `json:"number,omitempty" doc:"The task's place in the conversation, from 1: the chat's Task N. Set in GET /tasks and task_update; missing in older AgentAPIs."`
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
	Limit *agentstate.LimitStop `json:"limit,omitempty" doc:"The usage limit stop of a limited task."`
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
	// AttentionLimit: a usage limit stopped the agent's turn.
	AttentionLimit Attention = "limit"
	// AttentionExited: the agent exited.
	AttentionExited Attention = "exited"
)

var AttentionValues = []Attention{
	AttentionNone, AttentionLogin, AttentionRequest, AttentionLimit, AttentionExited,
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
	Attention Attention      `json:"attention" doc:"Why the agent waits for the user: login, request (an approval, question or plan), limit (a usage limit stopped its turn), exited, or none."`
	// Title is the session's name, else the start of the latest prompt.
	Title          string `json:"title" doc:"The name the agent gave the session, else the start of the latest task's prompt; empty before the first task."`
	SessionName    string `json:"session_name,omitempty" doc:"The name the agent gave the session, as in GET /status."`
	AgentSessionID string `json:"agent_session_id,omitempty" doc:"The agent's own session, as in GET /status."`
	// PendingRequests and QueuedMessages count what waits.
	PendingRequests int `json:"pending_requests" doc:"Requests (approvals, questions, plans) the agent waits on; see GET /requests."`
	QueuedMessages  int `json:"queued_messages" doc:"Messages waiting in the queue; see GET /queue."`
	// Task is the latest task, nil before the first.
	Task  *Task `json:"task,omitempty" doc:"The latest task, as in GET /tasks; missing before the first."`
	Tasks int   `json:"tasks" doc:"How many tasks the conversation has."`
	// Unread: the latest task ended after a client last showed the
	// conversation (POST /tasks/seen).
	Unread        bool                      `json:"unread" doc:"The latest task ended after a client last showed the conversation to the user (POST /tasks/seen): its result wasn't seen yet."`
	SeenAt        time.Time                 `json:"seen_at" doc:"When a client last showed the conversation to the user (POST /tasks/seen); when AgentAPI started, before any did."`
	LoginRequired *agentstate.LoginRequired `json:"login_required,omitempty" doc:"Set while the agent waits for the user to log in, as in GET /status."`
	LimitStop     *agentstate.LimitStop     `json:"limit_stop,omitempty" doc:"Set while a usage limit holds the agent, as in GET /status."`
	RetryWait     *agentstate.RetryWait     `json:"retry_wait,omitempty" doc:"Set while a failed turn waits to be tried again, as in GET /status. The agent needs nothing of the user meanwhile: attention stays none."`
	Usage         *agentstate.AgentUsage    `json:"usage,omitempty" doc:"The context window and the account's usage limits, as in GET /status."`
	// Request is the oldest pending request, what a client shows of what
	// the agent waits for without opening it.
	Request *SummaryRequest `json:"request,omitempty" doc:"The oldest request the agent waits on (see GET /requests); missing when none waits."`
	// Spend is what the agent's sessions cost recently.
	Spend *SummarySpend `json:"spend,omitempty" doc:"What the agent's sessions in this directory cost over the last 24 hours and 7 days, at list prices, worked out once a minute; missing for agents whose session logs AgentAPI doesn't read and until it is worked out. AgentAPIs with one scope add up the same sessions: count each scope once."`
	// ProviderHealth is how the model providers answered recently.
	ProviderHealth []SummaryProviderHealth `json:"provider_health,omitempty" doc:"How each model provider the agent called over the last hour answered, through AgentAPI's provider proxy, by provider ID; worked out every 30 seconds. Missing without such calls, and from earlier releases."`
	// Update is nil from AgentAPIs that can't update through their API.
	Update *UpdateStatus `json:"update,omitempty" doc:"Whether a client can update this AgentAPI to the latest release through its API (POST /update), and how far an update is. Missing from AgentAPIs that can't update through their API: earlier releases."`
}

// UpdateState is where an update through the API (POST /update) is.
type UpdateState string

const (
	UpdateStateIdle UpdateState = "idle"
	// UpdateStateChecking: it looks up the latest release.
	UpdateStateChecking    UpdateState = "checking"
	UpdateStateDownloading UpdateState = "downloading"
	// UpdateStateWaiting: it waits for the agent to be idle to restart.
	UpdateStateWaiting    UpdateState = "waiting"
	UpdateStateRestarting UpdateState = "restarting"
	UpdateStateFailed     UpdateState = "failed"
)

var UpdateStateValues = []UpdateState{
	UpdateStateIdle, UpdateStateChecking, UpdateStateDownloading,
	UpdateStateWaiting, UpdateStateRestarting, UpdateStateFailed,
}

func (u UpdateState) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "UpdateState", UpdateStateValues)
}

// UpdateStatus says whether a client can update an AgentAPI through its
// API, and how far an update is.
type UpdateStatus struct {
	Allowed bool `json:"allowed" doc:"Updates through the API are on: --allow-update, AGENTAPI_ALLOW_UPDATE, or allow-update: true in ~/.agentapi/config.yaml. Off by default."`
	// Blocked is why an AgentAPI that may update can't replace its program.
	Blocked string      `json:"blocked,omitempty" doc:"Set when updates are on but this AgentAPI can't replace its program: why, and the command to run once to fix it (the program's directory must be writable by the user AgentAPI runs as)."`
	State   UpdateState `json:"state" doc:"idle, checking (looks up the latest release), downloading, waiting (for the agent to be idle), restarting (into the new version) or failed."`
	Target  string      `json:"target,omitempty" doc:"The version it updates to, once known."`
	Error   string      `json:"error,omitempty" doc:"Why the last update failed (state failed)."`
	// KeepsConversation: the state database carries the conversation over
	// the restart.
	KeepsConversation bool `json:"keeps_conversation" doc:"It runs with --state-db, so the conversation (its queue, open requests and chosen settings included) and the agent's session carry over the restart."`
	// KeepsSession: the agent resumes its session after the restart, with
	// a state database or from the session's log without one.
	KeepsSession bool `json:"keeps_session" doc:"The agent resumes its session after the restart: with --state-db, and without it for Claude Code, Codex and Pi, whose conversation is then read back from the agent's session log while the queue, the open requests and the settings chosen start anew. Earlier releases don't send it; without --state-db they start a new session."`
}

// SummaryRequest is what a summary says of a pending request.
type SummaryRequest struct {
	ID        string                 `json:"id" doc:"The request's ID, as in GET /requests."`
	Kind      agentstate.RequestKind `json:"kind" doc:"approval, question or plan."`
	Title     string                 `json:"title" doc:"One-line summary, such as the command to run."`
	CreatedAt time.Time              `json:"created_at" doc:"When the agent asked."`
}

// SummarySpend is what the agent's sessions in its working directory cost
// recently.
type SummarySpend struct {
	DayUSD  float64 `json:"day_usd" doc:"API-equivalent cost in USD of the agent's sessions in this directory, subagents included, over the last 24 hours (the hours that end within them): the tokens at the model providers' list prices, which a subscription doesn't charge."`
	WeekUSD float64 `json:"week_usd" doc:"The same over the last 7 days."`
	// DayTokens and WeekTokens are the tokens of the same calls; nil from
	// AgentAPIs that don't count them (earlier releases).
	DayTokens  *SummaryTokens `json:"day_tokens,omitempty" doc:"The tokens the agent's sessions in this directory used over the last 24 hours, the calls day_usd adds up, each once; models without a known price included. Missing from earlier releases."`
	WeekTokens *SummaryTokens `json:"week_tokens,omitempty" doc:"The same over the last 7 days."`
	// Scope identifies what the amounts add up: two AgentAPIs of one agent
	// in one directory of one machine read the same session logs.
	Scope string `json:"scope" doc:"Identifies the sessions the amounts add up: those of one agent in one directory, as the session logs of one machine hold them (a hash of the machine's host name and ID, the agent, the directory's real path and where the agent keeps its logs). AgentAPIs that report the same scope report the same sessions; a client that adds up many counts each scope once."`
	// UpdatedAt is when the amounts last changed.
	UpdatedAt time.Time `json:"updated_at" doc:"When the amounts or tokens last changed: of AgentAPIs with one scope, the latest is the most recent."`
	// Hours are the hours behind the amounts, for a client that keeps a
	// longer record (the hub's activity); nil from earlier releases.
	Hours []SummaryHour `json:"hours,omitempty" doc:"What was spent in each hour with calls of the last 25 (UTC, the earliest first), the hour under way included: the hours day_usd adds up and the one before them. An hour's figures grow while it goes on. Sent with the amounts: when they don't change, an hour that left the 25 may stay. Missing from earlier releases."`
	// Providers split the amounts by model provider; nil from earlier
	// releases, and without calls in the last 7 days.
	Providers []SummaryProviderSpend `json:"providers,omitempty" doc:"The amounts and tokens by AgentAPI model provider (default for the agent's own), of the providers with calls in the last 7 days: the most expensive over the week first, then the one with the most tokens. They add up to the totals, but for rounding. Missing from earlier releases and without calls in the last 7 days."`
}

// SummaryProviderSpend is what the agent's sessions in its working
// directory spent recently on one model provider.
type SummaryProviderSpend struct {
	Provider   string        `json:"provider" doc:"The AgentAPI model provider, by its ID; default for the agent's own. Claude Code's calls go to the provider AgentAPI recorded their session on (its provider ledger); Codex's log names it."`
	DayUSD     float64       `json:"day_usd" doc:"API-equivalent cost in USD of the provider's calls over the last 24 hours, as day_usd: at the list prices of the models called."`
	WeekUSD    float64       `json:"week_usd" doc:"The same over the last 7 days."`
	DayTokens  SummaryTokens `json:"day_tokens" doc:"The tokens of the provider's calls over the last 24 hours."`
	WeekTokens SummaryTokens `json:"week_tokens" doc:"The same over the last 7 days."`
}

// SummaryProviderHealth is how a model provider answered the agent's
// calls over the last hour.
type SummaryProviderHealth struct {
	Provider string `json:"provider" doc:"The AgentAPI model provider, by its ID."`
	agentstate.ProviderHealth
}

// CloneProviderHealth returns a copy of health that shares nothing with
// it.
func CloneProviderHealth(health []SummaryProviderHealth) []SummaryProviderHealth {
	if health == nil {
		return nil
	}
	out := make([]SummaryProviderHealth, len(health))
	for i, h := range health {
		out[i] = SummaryProviderHealth{Provider: h.Provider, ProviderHealth: h.ProviderHealth.Clone()}
	}
	return out
}

// EqualProviderHealth reports whether a and b say the same.
func EqualProviderHealth(a, b []SummaryProviderHealth) bool {
	return slices.EqualFunc(a, b, func(x, y SummaryProviderHealth) bool {
		return x.Provider == y.Provider && x.ProviderHealth.Equal(y.ProviderHealth)
	})
}

// SummaryHour is what the agent's sessions in a directory spent in one
// hour.
type SummaryHour struct {
	Hour   time.Time     `json:"hour" doc:"The start of the hour, in UTC."`
	USD    float64       `json:"usd" doc:"API-equivalent cost in USD of the hour's calls, at list prices."`
	Tokens SummaryTokens `json:"tokens" doc:"The tokens of the hour's calls, models without a known price included."`
}

// Clone returns a copy of s that shares none of its tokens or hours.
func (s SummarySpend) Clone() SummarySpend {
	s.DayTokens, s.WeekTokens = cloneTokens(s.DayTokens), cloneTokens(s.WeekTokens)
	s.Hours = slices.Clone(s.Hours)
	s.Providers = slices.Clone(s.Providers)
	return s
}

// Equal reports whether s and o say the same.
func (s SummarySpend) Equal(o SummarySpend) bool {
	return s.DayUSD == o.DayUSD && s.WeekUSD == o.WeekUSD && s.Scope == o.Scope && s.UpdatedAt.Equal(o.UpdatedAt) &&
		s.SameTokens(o) && slices.EqualFunc(s.Hours, o.Hours, func(a, b SummaryHour) bool {
		return a.Hour.Equal(b.Hour) && a.USD == b.USD && a.Tokens == b.Tokens
	}) && s.SameProviders(o)
}

// SameProviders reports whether s and o split the amounts and tokens by
// provider the same.
func (s SummarySpend) SameProviders(o SummarySpend) bool {
	return slices.Equal(s.Providers, o.Providers)
}

// SameTokens reports whether s and o count the same tokens.
func (s SummarySpend) SameTokens(o SummarySpend) bool {
	return equalTokens(s.DayTokens, o.DayTokens) && equalTokens(s.WeekTokens, o.WeekTokens)
}

func cloneTokens(t *SummaryTokens) *SummaryTokens {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func equalTokens(a, b *SummaryTokens) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// SummaryTokens are the tokens of the model calls a spend adds up.
type SummaryTokens struct {
	Input  int `json:"input" doc:"Uncached input tokens."`
	Output int `json:"output" doc:"Output tokens, thinking included."`
	// CacheWrite counts both of Anthropic's caches.
	CacheWrite int `json:"cache_write" doc:"Input tokens written to the prompt cache, the five-minute and one-hour caches together."`
	CacheRead  int `json:"cache_read" doc:"Input tokens read from the prompt cache."`
	Total      int `json:"total" doc:"The sum of input, output, cache_write and cache_read."`
}
