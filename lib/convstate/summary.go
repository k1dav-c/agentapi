package convstate

import (
	"reflect"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/instanceapi"
)

// summaryTitleLimit is how many characters of the prompt a title takes.
const summaryTitleLimit = 60

// summaryRequestTitleLimit is how many characters of a request's title a
// summary carries.
const summaryRequestTitleLimit = 120

// SummaryUpdateBody is the SSE payload of the summary after it changed.
type SummaryUpdateBody struct {
	instanceapi.SummaryBody
}

// WithInstance has summaries name the AgentAPI with instance.
func WithInstance(instance func() instanceapi.WebhookInstance) EventEmitterOption {
	return func(e *EventEmitter) { e.instance = instance }
}

// summaryLocked is the conversation's summary. Caller holds e.mu.
func (e *EventEmitter) summaryLocked() instanceapi.SummaryBody {
	status := e.statusChangeBody()
	summary := instanceapi.SummaryBody{
		Status: status.Status, Lifecycle: status.Lifecycle,
		SessionName: status.SessionName, AgentSessionID: status.AgentSessionID,
		PendingRequests: status.PendingRequests, QueuedMessages: len(e.queue),
		Tasks:         len(e.tasks.tasks),
		LoginRequired: status.LoginRequired, LimitStop: status.LimitStop, RetryWait: status.RetryWait, Usage: status.Usage,
	}
	if e.instance != nil {
		summary.Instance = e.instance()
	}
	summary.SeenAt = e.seenAt
	if e.summary.spend != nil {
		spend := e.summary.spend.Clone()
		summary.Spend = &spend
	}
	if e.summary.update != nil {
		update := *e.summary.update
		summary.Update = &update
	}
	summary.ProviderHealth = instanceapi.CloneProviderHealth(e.summary.providerHealth)
	for _, r := range e.requests {
		if r.Status != agentstate.RequestStatusPending {
			continue
		}
		if summary.Request == nil || r.CreatedAt.Before(summary.Request.CreatedAt) {
			summary.Request = &instanceapi.SummaryRequest{ID: r.ID, Kind: r.Kind, Title: shortText(r.Title, summaryRequestTitleLimit), CreatedAt: r.CreatedAt}
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
		summary.Attention = instanceapi.AttentionLogin
	case status.PendingRequests > 0:
		summary.Attention = instanceapi.AttentionRequest
	case status.LimitStop != nil:
		summary.Attention = instanceapi.AttentionLimit
	case status.Lifecycle == instanceapi.LifecycleExited || status.Lifecycle == instanceapi.LifecycleFailed:
		summary.Attention = instanceapi.AttentionExited
	default:
		summary.Attention = instanceapi.AttentionNone
	}
	return summary
}

// Summary returns the conversation's summary.
func (e *EventEmitter) Summary() instanceapi.SummaryBody {
	e.mu.Lock()
	defer e.unlock()
	return e.summaryLocked()
}

// MarkSeen records that a client showed the conversation to the user now,
// and returns the summary.
func (e *EventEmitter) MarkSeen() instanceapi.SummaryBody {
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
func (e *EventEmitter) SetSpend(spend instanceapi.SummarySpend) {
	e.mu.Lock()
	defer e.unlock()
	if e.summary.spend != nil && e.summary.spend.Equal(spend) {
		return
	}
	spend = spend.Clone()
	e.summary.spend = &spend
	e.summary.changed = true
}

// SetUpdate records how far an update through the API is, and sends the
// summary if that changed.
func (e *EventEmitter) SetUpdate(update instanceapi.UpdateStatus) {
	e.mu.Lock()
	defer e.unlock()
	if e.summary.update != nil && *e.summary.update == update {
		return
	}
	e.summary.update = &update
	e.summary.changed = true
}

// SetProviderHealth records how the model providers answered recently,
// and sends the summary if that changed.
func (e *EventEmitter) SetProviderHealth(health []instanceapi.SummaryProviderHealth) {
	e.mu.Lock()
	defer e.unlock()
	if len(health) == 0 {
		health = nil
	}
	if instanceapi.EqualProviderHealth(e.summary.providerHealth, health) {
		return
	}
	e.summary.providerHealth = instanceapi.CloneProviderHealth(health)
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
	sent *instanceapi.SummaryBody
	// spend is what the agent's sessions cost recently (startSpend); nil
	// until it is worked out, and for agents whose logs aren't read.
	spend *instanceapi.SummarySpend
	// update is how far an update through the API is (updater); nil
	// until the server sets it.
	update *instanceapi.UpdateStatus
	// providerHealth is how the providers answered (startProviderHealth);
	// nil without calls.
	providerHealth []instanceapi.SummaryProviderHealth
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
