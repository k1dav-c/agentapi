package httpapi

import (
	"context"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
)

// The summary's types are the contract with the clients that watch many
// AgentAPIs (lib/instanceapi); these names keep the OpenAPI schema's.
type (
	Attention      = instanceapi.Attention
	SummaryBody    = instanceapi.SummaryBody
	SummaryRequest = instanceapi.SummaryRequest
	SummarySpend   = instanceapi.SummarySpend
)

const (
	AttentionNone    = instanceapi.AttentionNone
	AttentionLogin   = instanceapi.AttentionLogin
	AttentionRequest = instanceapi.AttentionRequest
	AttentionDialog  = instanceapi.AttentionDialog
	AttentionLimit   = instanceapi.AttentionLimit
	AttentionExited  = instanceapi.AttentionExited
)

var AttentionValues = instanceapi.AttentionValues

// summaryTitleLimit is how many characters of the prompt a title takes.
const summaryTitleLimit = 60

// summaryRequestTitleLimit is how many characters of a request's title a
// summary carries.
const summaryRequestTitleLimit = 120

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
	if e.summary.spend != nil {
		spend := *e.summary.spend
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
		task := e.tasks.tasks[n-1].Clone()
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
	defer e.unlock()
	return e.summaryLocked()
}

// MarkSeen records that a client showed the conversation to the user now,
// and returns the summary.
func (e *EventEmitter) MarkSeen() SummaryBody {
	e.mu.Lock()
	defer e.unlock()
	e.seenAt = e.clock.Now()
	if e.persister != nil {
		e.persister.SetSeenAt(e.seenAt)
	}
	e.summary.changed = true
	return e.summaryLocked()
}

// SetSpend records what the agent's sessions cost recently, and sends the
// summary if that changed.
func (e *EventEmitter) SetSpend(spend SummarySpend) {
	e.mu.Lock()
	defer e.unlock()
	if e.summary.spend != nil && *e.summary.spend == spend {
		return
	}
	e.summary.spend = &spend
	e.summary.changed = true
}

// SummaryChanged sends the summary if it changed by something outside the
// conversation (the instance's settings).
func (e *EventEmitter) SummaryChanged() {
	e.mu.Lock()
	defer e.unlock()
	e.summary.changed = true
}

// summaryFeed sends the summary to the subscriptions that want it: once
// for all the changes an emitter method made, when it releases the lock
// (unlock), and only if it differs from the one sent last. e.mu guards it.
type summaryFeed struct {
	// changed: the state changed since the summary was last sent.
	changed bool
	// sent is the summary sent last, nil while no subscription wants it.
	sent *SummaryBody
	// spend is what the agent's sessions cost recently (startSpend); nil
	// until it is worked out, and for agents whose logs aren't read.
	spend *SummarySpend
}

// sendSummaryLocked sends the summary if the state changed since it was
// last sent and it differs. Without a subscription that wants it, it isn't
// worked out. Caller holds e.mu.
func (e *EventEmitter) sendSummaryLocked() {
	if !e.summary.changed {
		return
	}
	e.summary.changed = false
	if !e.anyWantsLocked(EventTypeSummaryUpdate) {
		e.summary.sent = nil
		return
	}
	summary := e.summaryLocked()
	if e.summary.sent != nil && reflect.DeepEqual(*e.summary.sent, summary) {
		return
	}
	e.summary.sent = &summary
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
