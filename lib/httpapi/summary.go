package httpapi

import (
	"context"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/util"
)

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

// summaryTitleLimit is how many characters of the prompt a title takes.
const summaryTitleLimit = 60

// summaryRequestTitleLimit is how many characters of a request's title a
// summary carries.
const summaryRequestTitleLimit = 120

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
	Spend *SummarySpend `json:"spend,omitempty" doc:"What the agent's sessions in this directory cost over the last 24 hours and 7 days, at list prices, worked out once a minute; missing for agents whose session logs AgentAPI doesn't read (PTY, ACP) and until it is worked out."`
}

// SummaryRequest is what a summary says of a pending request.
type SummaryRequest struct {
	ID        string             `json:"id" doc:"The request's ID, as in GET /requests."`
	Kind      native.RequestKind `json:"kind" doc:"approval, question or plan."`
	Title     string             `json:"title" doc:"One-line summary, such as the command to run."`
	CreatedAt time.Time          `json:"created_at" doc:"When the agent asked."`
}

// SummaryUpdateBody is the SSE payload of the summary after it changed.
type SummaryUpdateBody struct {
	SummaryBody
}

// WithInstance has summaries name the AgentAPI with instance.
func WithInstance(instance func() WebhookInstance) EventEmitterOption {
	return func(e *EventEmitter) { e.instance = instance }
}

// summaryLocked is the conversation's summary. Caller holds e.mu.
func (e *EventEmitter) summaryLocked() SummaryBody {
	status := e.statusChangeBody()
	summary := SummaryBody{
		Status: status.Status, Lifecycle: status.Lifecycle,
		SessionName: status.SessionName, AgentSessionID: status.AgentSessionID,
		PendingRequests: status.PendingRequests, QueuedMessages: len(e.queue),
		TerminalPrompt: status.TerminalPrompt, Tasks: len(e.tasks.tasks),
		LoginRequired: status.LoginRequired, LimitStop: status.LimitStop, Usage: status.Usage,
	}
	if e.instance != nil {
		summary.Instance = e.instance()
	}
	summary.SeenAt = e.seenAt
	if e.spend != nil {
		spend := *e.spend
		summary.Spend = &spend
	}
	for _, r := range e.requests {
		if r.Status != native.RequestStatusPending {
			continue
		}
		if summary.Request == nil || r.CreatedAt.Before(summary.Request.CreatedAt) {
			summary.Request = &SummaryRequest{ID: r.ID, Kind: r.Kind, Title: shortText(r.Title, summaryRequestTitleLimit), CreatedAt: r.CreatedAt}
		}
	}
	summary.Title = summary.SessionName
	if n := len(e.tasks.tasks); n > 0 {
		task := e.tasks.tasks[n-1].clone()
		summary.Task = &task
		summary.Unread = task.EndedAt != nil && task.EndedAt.After(e.seenAt)
		if summary.Title == "" {
			summary.Title = shortText(task.Prompt, summaryTitleLimit)
		}
	}
	switch {
	case status.LoginRequired != nil:
		summary.Attention = AttentionLogin
	case status.PendingRequests > 0:
		summary.Attention = AttentionRequest
	case status.TerminalPrompt != "":
		summary.Attention = AttentionDialog
	case status.LimitStop != nil:
		summary.Attention = AttentionLimit
	case status.Lifecycle == LifecycleExited || status.Lifecycle == LifecycleFailed:
		summary.Attention = AttentionExited
	default:
		summary.Attention = AttentionNone
	}
	return summary
}

// Summary returns the conversation's summary.
func (e *EventEmitter) Summary() SummaryBody {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summaryLocked()
}

// MarkSeen records that a client showed the conversation to the user now,
// and returns the summary.
func (e *EventEmitter) MarkSeen() SummaryBody {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seenAt = e.clock.Now()
	if e.persister != nil {
		e.persister.SetSeenAt(e.seenAt)
	}
	e.summaryChangedLocked()
	return e.summaryLocked()
}

// SetSpend records what the agent's sessions cost recently, and sends the
// summary if that changed.
func (e *EventEmitter) SetSpend(spend SummarySpend) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.spend != nil && *e.spend == spend {
		return
	}
	e.spend = &spend
	e.summaryChangedLocked()
}

// SummaryChanged sends the summary if it changed by something outside the
// conversation (the instance's settings).
func (e *EventEmitter) SummaryChanged() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.summaryChangedLocked()
}

// summaryChangedLocked sends the summary to the subscriptions that want it,
// if it changed since it was last sent. Without any, it isn't worked out.
// Caller holds e.mu.
func (e *EventEmitter) summaryChangedLocked() {
	if !e.anyWantsLocked(EventTypeSummaryUpdate) {
		e.summary = nil
		return
	}
	summary := e.summaryLocked()
	if e.summary != nil && reflect.DeepEqual(*e.summary, summary) {
		return
	}
	e.summary = &summary
	e.notifyChannels(EventTypeSummaryUpdate, SummaryUpdateBody{SummaryBody: summary})
}

// anyWantsLocked reports whether a subscription wants events of type t.
// Caller holds e.mu.
func (e *EventEmitter) anyWantsLocked(t EventType) bool {
	for _, sub := range e.chans {
		if sub.wants(t) {
			return true
		}
	}
	return false
}

// SummaryEvents picks the summary alone (GET /events?only=summary).
func SummaryEvents(t EventType) bool { return t == EventTypeSummaryUpdate }

// SummaryResponse is the body of GET /summary.
type SummaryResponse struct {
	Body SummaryBody
}

func (s *Server) getSummary(_ context.Context, _ *struct{}) (*SummaryResponse, error) {
	return &SummaryResponse{Body: s.emitter.Summary()}, nil
}

func (s *Server) markSeen(_ context.Context, _ *struct{}) (*SummaryResponse, error) {
	return &SummaryResponse{Body: s.emitter.MarkSeen()}, nil
}

func (s *Server) registerSummaryRoutes() {
	huma.Post(s.api, "/tasks/seen", s.markSeen, func(o *huma.Operation) {
		o.Summary = "Mark the tasks seen"
		o.Description = "Records that a client showed the conversation to the user now, so the tasks that ended " +
			"until now are read: the summary's unread is false until a task ends again. Every client of this " +
			"AgentAPI shares it: the chat calls it while it shows the conversation, and summary clients get " +
			"summary_update. Returns the summary."
	})
	huma.Get(s.api, "/summary", s.getSummary, func(o *huma.Operation) {
		o.Summary = "Get a summary of the agent"
		o.Description = "Returns what a client that watches many AgentAPIs shows of one in a single response: " +
			"who it is (instance), whether the agent works or waits, why it waits for the user (attention), " +
			"a title, the latest task, what waits (requests, queued messages) and the usage limits. " +
			"GET /events?only=summary sends summary_update whenever it changes, and no other events."
	})
}
