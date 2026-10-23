package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/pricing"
	st "github.com/coder/agentapi/lib/screentracker"
)

// The events a webhook can be sent besides run.status_changed.
const (
	webhookEventInstanceStarted   = "instance.started"
	webhookEventInstanceHeartbeat = "instance.heartbeat"
	webhookEventInstanceStopped   = "instance.stopped"
	webhookEventTaskStarted       = "task.started"
	webhookEventTaskCompleted     = "task.completed"
	webhookEventTaskFailed        = "task.failed"
	webhookEventRequestOpened     = "request.opened"
	webhookEventRequestClosed     = "request.closed"
	webhookEventLoginRequired     = "login.required"
	webhookEventLoginCompleted    = "login.completed"
	webhookEventLimitStopped      = "limit.stopped"
	webhookEventLimitCleared      = "limit.cleared"
	webhookEventAgentError        = "agent.error"
	webhookEventMessageCreated    = "message.created"
	webhookEventToolCompleted     = "tool.completed"
)

// How much of a message, and of a tool call's input and result, an event
// carries.
const (
	webhookMessageLimit = 64 << 10
	webhookToolLimit    = 4 << 10
	webhookPromptLimit  = 4 << 10
)

// WebhookInstanceState is the data of instance.started, instance.heartbeat
// and instance.stopped: what the agent is doing.
type WebhookInstanceState struct {
	Status          AgentStatus           `json:"status"`
	Lifecycle       LifecycleState        `json:"lifecycle"`
	SessionID       string                `json:"session_id"`
	SessionName     string                `json:"session_name,omitempty"`
	PendingRequests int                   `json:"pending_requests"`
	QueuedMessages  int                   `json:"queued_messages"`
	TerminalPrompt  string                `json:"terminal_prompt,omitempty"`
	LoginRequired   *native.LoginRequired `json:"login_required,omitempty"`
	LimitStop       *native.LimitStop     `json:"limit_stop,omitempty"`
	Usage           *native.AgentUsage    `json:"usage,omitempty"`
	Settings        *native.Settings      `json:"settings,omitempty"`
}

// WebhookTask is the data of task.started, task.completed and task.failed:
// a message of the user and the agent's turn on it.
type WebhookTask struct {
	// ID is the ID of the user's message, as in GET /messages.
	ID              int        `json:"id"`
	Prompt          string     `json:"prompt"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds float64    `json:"duration_seconds,omitempty"`
	// Outcome is completed, failed (an agent error, or the agent exited) or
	// limited (a usage limit stopped it); empty while it runs.
	Outcome string `json:"outcome,omitempty"`
	// Reply is the agent's last message of the turn.
	Reply  string            `json:"reply,omitempty"`
	Errors []string          `json:"errors,omitempty"`
	Limit  *native.LimitStop `json:"limit,omitempty"`
	// Cost is what the turn's model calls cost at list prices (native
	// Claude Code, Codex and Pi).
	Cost        *WebhookCost `json:"cost,omitempty"`
	SessionName string       `json:"session_name,omitempty"`
}

// WebhookCost is the API-equivalent cost of some model calls.
type WebhookCost struct {
	USD    float64        `json:"usd"`
	Tokens pricing.Tokens `json:"tokens"`
	Calls  int            `json:"calls"`
}

// WebhookMessage is the data of message.created.
type WebhookMessage struct {
	ID        int                 `json:"id"`
	Role      st.ConversationRole `json:"role"`
	Content   string              `json:"content"`
	Time      time.Time           `json:"time"`
	Truncated bool                `json:"truncated,omitempty"`
}

// WebhookToolCall is the data of tool.completed.
type WebhookToolCall struct {
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	// Input is the call's input as JSON, or a string when it was cut.
	Input     any       `json:"input,omitempty"`
	Result    string    `json:"result"`
	IsError   bool      `json:"is_error"`
	Time      time.Time `json:"time"`
	Truncated bool      `json:"truncated,omitempty"`
}

// WebhookTerminalPrompt is the data of request.opened and request.closed
// for a dialog the agent shows in its terminal (PTY transport): answer it
// with POST /message, type raw.
type WebhookTerminalPrompt struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Prompt string `json:"prompt,omitempty"`
}

// startWebhook describes this AgentAPI to the webhook and starts sending
// events: the conversation's, as the observer turns them into webhook
// events, and the instance's.
func (s *Server) startWebhook(config ServerConfig) {
	hostname, _ := os.Hostname()
	s.webhook.setInstance(WebhookInstance{
		ID:           net.JoinHostPort(hostname, strconv.Itoa(config.Port)),
		Hostname:     hostname,
		Port:         config.Port,
		PID:          os.Getpid(),
		CWD:          config.CWD,
		Version:      version.Version,
		WorkspaceURL: coderWorkspaceURL(),
	})
	observer := newWebhookObserver(s.webhook, s.emitter, s.clock.Now())
	observer.clock = s.clock
	observer.queued = func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.queue.Len()
	}
	if s.sessions != nil {
		observer.taskCost = s.webhookTaskCost
	}
	s.webhook.snapshot = func() any { return observer.state() }
	go observer.run(s.shutdownCtx)
	s.webhook.start(s.shutdownCtx)
}

// webhookTaskCost is what the open session's model calls since a time
// cost, or nil when none are known.
func (s *Server) webhookTaskCost(since time.Time) *WebhookCost {
	id := s.native.SessionRef()
	if id == "" {
		return nil
	}
	costs, err := s.sessions.Costs()
	if err != nil {
		s.logger.Warn("Failed to read what the task cost", "error", err)
		return nil
	}
	var cost WebhookCost
	for _, call := range costs.Sessions[id] {
		if call.Time.Before(since) {
			continue
		}
		cost.USD += call.USD
		cost.Tokens = cost.Tokens.Add(call.Tokens)
		cost.Calls++
	}
	if cost.Calls == 0 {
		return nil
	}
	return &cost
}

// webhookObserver turns the conversation's events into webhook events. It
// reads the emitter like an /events client.
type webhookObserver struct {
	webhook *webhookDispatcher
	emitter *EventEmitter
	clock   quartz.Clock
	// taskCost, if set, gives what the session's model calls since a time
	// cost.
	taskCost func(since time.Time) *WebhookCost
	// queued counts the messages waiting in the queue, for the heartbeat.
	queued func() int

	// since: messages and tool calls from before it are history (a
	// restored or resumed conversation), not news.
	since  time.Time
	status StatusChangeBody
	// agent is the agent's latest message, sent as message.created once
	// it is final: when another follows or the turn ends.
	agent *MessageUpdateBody
	// sent are the messages already sent, by ID and time (IDs start over
	// in a new conversation).
	sent     map[messageKey]bool
	tools    map[string]WebhookToolCall
	toolDone map[string]bool
	requests map[string]native.RequestStatus
	// prompts counts the terminal dialogs, for their IDs.
	prompts int
	task    *WebhookTask
	// taskRan: the agent worked on the task, so its being stable again
	// ends it.
	taskRan bool
}

type messageKey struct {
	id   int
	time time.Time
}

func newWebhookObserver(webhook *webhookDispatcher, emitter *EventEmitter, now time.Time) *webhookObserver {
	return &webhookObserver{
		webhook:  webhook,
		emitter:  emitter,
		since:    now,
		sent:     map[messageKey]bool{},
		tools:    map[string]WebhookToolCall{},
		toolDone: map[string]bool{},
		requests: map[string]native.RequestStatus{},
	}
}

func webhookObservedEvents(t EventType) bool {
	return t != EventTypeScreenUpdate && t != EventTypeHeartbeat
}

// run reads the conversation's events until ctx ends. The state at the
// start is history; a subscription the emitter dropped (its buffer filled)
// is taken up again, and what changed meanwhile is sent then.
func (o *webhookObserver) run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		id, events, replay := o.emitter.SubscribeTo(webhookObservedEvents, 0, "")
		for _, event := range replay {
			o.handle(event, first)
		}
		first = false
	read:
		for {
			select {
			case <-ctx.Done():
				o.emitter.Unsubscribe(id)
				return
			case event, ok := <-events:
				if !ok {
					break read
				}
				o.handle(event, false)
			}
		}
	}
}

// handle takes one event; quiet takes in the state without sending
// anything.
func (o *webhookObserver) handle(event Event, quiet bool) {
	switch payload := event.Payload.(type) {
	case StatusChangeBody:
		o.statusChanged(payload, quiet)
	case MessageUpdateBody:
		o.messageUpdated(payload, quiet)
	case RichMessageUpdateBody:
		o.richMessageUpdated(payload, quiet)
	case RequestUpdateBody:
		o.requestUpdated(payload.RequestState, quiet)
	case ErrorBody:
		o.agentError(payload, quiet)
	}
}

func (o *webhookObserver) statusChanged(next StatusChangeBody, quiet bool) {
	previous := o.status
	o.status = next
	if quiet {
		return
	}
	if (previous.LoginRequired == nil) != (next.LoginRequired == nil) {
		if next.LoginRequired != nil {
			o.webhook.emit(webhookEventLoginRequired, next.LoginRequired)
		} else {
			o.webhook.emit(webhookEventLoginCompleted, previous.LoginRequired)
		}
	}
	if (previous.LimitStop == nil) != (next.LimitStop == nil) {
		if next.LimitStop != nil {
			o.webhook.emit(webhookEventLimitStopped, next.LimitStop)
		} else {
			o.webhook.emit(webhookEventLimitCleared, previous.LimitStop)
		}
	}
	if previous.TerminalPrompt != next.TerminalPrompt {
		if previous.TerminalPrompt != "" {
			o.webhook.emit(webhookEventRequestClosed, WebhookTerminalPrompt{
				ID: o.promptID(), Kind: "terminal_prompt", Status: string(native.RequestStatusResolved),
			})
		}
		if next.TerminalPrompt != "" {
			o.prompts++
			o.webhook.emit(webhookEventRequestOpened, WebhookTerminalPrompt{
				ID: o.promptID(), Kind: "terminal_prompt", Status: string(native.RequestStatusPending), Prompt: next.TerminalPrompt,
			})
		}
	}
	if next.Status == AgentStatusRunning {
		o.taskRan = true
	}
	// A turn waiting on a request or a terminal dialog isn't over, though
	// the agent is idle meanwhile.
	if next.Status == AgentStatusStable && o.taskRan && next.PendingRequests == 0 && next.TerminalPrompt == "" {
		o.flushAgent()
		o.endTask()
	}
	if next.Lifecycle == LifecycleExited || next.Lifecycle == LifecycleFailed {
		if o.task != nil {
			o.task.Errors = append(o.task.Errors, fmt.Sprintf("the agent %s", next.Lifecycle))
		}
		o.flushAgent()
		o.endTask()
	}
}

func (o *webhookObserver) promptID() string {
	return fmt.Sprintf("terminal-%d", o.prompts)
}

func (o *webhookObserver) messageUpdated(msg MessageUpdateBody, quiet bool) {
	key := messageKey{msg.Id, msg.Time}
	if quiet || msg.Time.Before(o.since) {
		o.sent[key] = true
		return
	}
	if o.sent[key] {
		return
	}
	if msg.Role != st.ConversationRoleUser {
		if o.agent != nil && o.agent.Id != msg.Id {
			o.flushAgent()
		}
		o.agent = &msg
		return
	}
	o.flushAgent()
	o.send(msg)
	if o.task == nil {
		o.task = &WebhookTask{
			ID: msg.Id, Prompt: cut(msg.Message, webhookPromptLimit), StartedAt: msg.Time,
			SessionName: o.status.SessionName,
		}
		o.taskRan = o.status.Status == AgentStatusRunning
		o.webhook.emit(webhookEventTaskStarted, *o.task)
	}
}

func (o *webhookObserver) flushAgent() {
	if o.agent == nil {
		return
	}
	msg := *o.agent
	o.agent = nil
	if o.sent[messageKey{msg.Id, msg.Time}] || msg.Message == "" {
		return
	}
	o.send(msg)
	if o.task != nil {
		o.task.Reply = cut(msg.Message, webhookMessageLimit)
	}
}

func (o *webhookObserver) send(msg MessageUpdateBody) {
	o.sent[messageKey{msg.Id, msg.Time}] = true
	o.webhook.emit(webhookEventMessageCreated, WebhookMessage{
		ID: msg.Id, Role: msg.Role, Content: cut(msg.Message, webhookMessageLimit), Time: msg.Time,
		Truncated: len(msg.Message) > webhookMessageLimit,
	})
}

// endTask sends task.completed or task.failed for the task under way.
func (o *webhookObserver) endTask() {
	task := o.task
	if task == nil {
		return
	}
	o.task = nil
	ended := o.now()
	task.EndedAt = &ended
	task.DurationSeconds = ended.Sub(task.StartedAt).Seconds()
	task.SessionName = o.status.SessionName
	eventType := webhookEventTaskCompleted
	switch {
	case o.status.LimitStop != nil && !o.status.LimitStop.StoppedAt.Before(task.StartedAt):
		task.Outcome = "limited"
		task.Limit = o.status.LimitStop
		eventType = webhookEventTaskFailed
	case len(task.Errors) > 0:
		task.Outcome = "failed"
		eventType = webhookEventTaskFailed
	default:
		task.Outcome = "completed"
	}
	if o.taskCost != nil && o.webhook.wants(eventType) {
		task.Cost = o.taskCost(task.StartedAt)
	}
	o.webhook.emit(eventType, *task)
}

func (o *webhookObserver) now() time.Time {
	if o.clock != nil {
		return o.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func (o *webhookObserver) richMessageUpdated(msg RichMessageUpdateBody, quiet bool) {
	history := quiet || msg.Timestamp.Before(o.since)
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			if _, ok := o.tools[block.ToolUseID]; !ok {
				o.tools[block.ToolUseID] = WebhookToolCall{ToolUseID: block.ToolUseID, Name: block.ToolName, Input: toolInput(block.ToolInput)}
			}
		case "tool_result":
			if o.toolDone[block.ToolUseID] || toolRunning(block.Status) {
				continue
			}
			o.toolDone[block.ToolUseID] = true
			if history {
				continue
			}
			call := o.tools[block.ToolUseID]
			call.ToolUseID = block.ToolUseID
			if call.Name == "" {
				call.Name = block.ToolName
			}
			call.Result = cut(block.Text, webhookToolLimit)
			call.IsError = block.IsError != nil && *block.IsError
			call.Time = msg.Timestamp
			_, inputCut := call.Input.(string)
			call.Truncated = inputCut || len(block.Text) > webhookToolLimit
			o.webhook.emit(webhookEventToolCompleted, call)
		}
	}
}

func toolRunning(status string) bool {
	switch status {
	case "running", "in_progress", "inProgress", "pending":
		return true
	}
	return false
}

// toolInput is a tool call's input as JSON, or cut to a string when it is
// too long.
func toolInput(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > webhookToolLimit {
		return cut(string(raw), webhookToolLimit)
	}
	return raw
}

func (o *webhookObserver) requestUpdated(state native.RequestState, quiet bool) {
	previous, known := o.requests[state.ID]
	o.requests[state.ID] = state.Status
	if quiet || (known && previous == state.Status) {
		return
	}
	switch {
	case state.Status == native.RequestStatusPending:
		o.webhook.emit(webhookEventRequestOpened, state)
	case known && previous == native.RequestStatusPending:
		o.webhook.emit(webhookEventRequestClosed, state)
	}
}

func (o *webhookObserver) agentError(err ErrorBody, quiet bool) {
	if quiet || err.Time.Before(o.since) {
		return
	}
	if o.task != nil && err.Level == st.ErrorLevelError {
		o.task.Errors = append(o.task.Errors, err.Message)
	}
	o.webhook.emit(webhookEventAgentError, err)
}

// state is the data of the instance.* events.
func (o *webhookObserver) state() WebhookInstanceState {
	status := o.emitter.StatusSnapshot()
	state := WebhookInstanceState{
		Status: status.Status, Lifecycle: status.Lifecycle, SessionID: status.SessionID,
		SessionName: status.SessionName, PendingRequests: status.PendingRequests,
		TerminalPrompt: status.TerminalPrompt, LoginRequired: status.LoginRequired,
		LimitStop: status.LimitStop, Usage: status.Usage, Settings: status.Settings,
	}
	if o.queued != nil {
		state.QueuedMessages = o.queued()
	}
	return state
}

// cut returns s cut to at most n bytes, on a character boundary, with an
// ellipsis when it was cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
