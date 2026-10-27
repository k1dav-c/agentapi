package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/agentapi/internal/version"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/pricing"
	"github.com/coder/agentapi/lib/transcript"
)

// The events a webhook can be sent besides run.status_changed.
const (
	webhookEventInstanceStarted   = instanceapi.EventInstanceStarted
	webhookEventInstanceHeartbeat = instanceapi.EventInstanceHeartbeat
	webhookEventInstanceStopped   = instanceapi.EventInstanceStopped
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

// WebhookInstanceState is the data of the instance.* events
// (lib/instanceapi).
type WebhookInstanceState = instanceapi.WebhookInstanceState

// WebhookTask is the data of task.started, task.completed and task.failed:
// the task as GET /tasks has it, with the text of the agent's reply.
type WebhookTask struct {
	Task
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	// Reply is the text of the agent's last message of the task.
	Reply string `json:"reply,omitempty"`
	// Cost is what the task's model calls cost at list prices (native
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
	ID        int       `json:"id"`
	Role      conv.Role `json:"role"`
	Content   string    `json:"content"`
	Time      time.Time `json:"time"`
	Truncated bool      `json:"truncated,omitempty"`
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

// maxOpenTools caps the tool calls the observer keeps until their result
// comes: one whose result never comes (an interrupted turn) isn't kept for
// good.
const maxOpenTools = 64

// startWebhook describes this AgentAPI to the webhook and starts sending
// events: the instance's, and while the webhook has a URL the
// conversation's, as the observer turns them into webhook events.
func (s *Server) startWebhook(config ServerConfig) {
	hostname, _ := os.Hostname()
	name := config.Name
	if name == "" && config.CWD != "" {
		name = filepath.Base(config.CWD)
	}
	s.webhook.setInstance(WebhookInstance{
		ID:           net.JoinHostPort(hostname, strconv.Itoa(config.Port)),
		Name:         name,
		Hostname:     hostname,
		Port:         config.Port,
		PID:          os.Getpid(),
		CWD:          config.CWD,
		Version:      version.Version,
		WorkspaceURL: coderWorkspaceURL(),
	})
	s.webhook.snapshot = func() any { return s.webhookInstanceState() }
	since := s.clock.Now()
	observing := &webhookObserving{ctx: s.shutdownCtx, webhook: s.webhook, run: func(ctx context.Context) {
		observer := newWebhookObserver(s.webhook, s.emitter, since)
		if s.sessions != nil {
			observer.taskCost = s.webhookTaskCost
		}
		observer.run(ctx)
	}}
	s.webhook.configured = observing.sync
	observing.sync()
	s.webhook.start(s.shutdownCtx)
}

// webhookInstanceState is the data of the instance.* events.
func (s *Server) webhookInstanceState() WebhookInstanceState {
	status := s.emitter.StatusSnapshot()
	return WebhookInstanceState{
		Status: status.Status, Lifecycle: status.Lifecycle, AgentSessionID: status.AgentSessionID,
		SessionName: status.SessionName, PendingRequests: status.PendingRequests,
		TerminalPrompt: status.TerminalPrompt, LoginRequired: status.LoginRequired,
		LimitStop: status.LimitStop, Usage: status.Usage, Settings: status.Settings,
		// The queue has a lock of its own. s.mu would hold up Stop, whose
		// instance.stopped carries the queue's length, until a call into
		// the agent it is about to cancel ended by itself.
		QueuedMessages: s.queue.Len(),
	}
}

// webhookObserving runs an observer while the webhook has a URL: without
// one nobody hears of the conversation, and an observer would only keep
// track of it.
type webhookObserving struct {
	ctx     context.Context
	webhook *webhookDispatcher
	// run runs an observer until its context ends.
	run func(ctx context.Context)

	mu sync.Mutex
	// stop ends the running observer and waits for it; nil without one.
	stop func()
}

// sync starts or stops the observer after the webhook's URL changed.
func (w *webhookObserving) sync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	wanted := w.webhook.configSnapshot().URL != ""
	switch {
	case wanted && w.stop == nil:
		ctx, cancel := context.WithCancel(w.ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			w.run(ctx)
		}()
		w.stop = func() {
			cancel()
			<-done
		}
	case !wanted && w.stop != nil:
		w.stop()
		w.stop = nil
	}
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
	// taskCost, if set, gives what the session's model calls since a time
	// cost.
	taskCost func(since time.Time) *WebhookCost

	// since: messages and tool calls from before it are history (a
	// restored or resumed conversation), not news.
	since  time.Time
	status StatusChangeBody
	// agent is the agent's latest message, sent as message.created once
	// it is final: when another follows or the turn ends.
	agent *MessageUpdateBody
	// sent is the last message sent (or taken in as history).
	sent mark
	// tools are the calls waiting for their result, toolOrder their IDs
	// as they came (and some that returned, until it is compacted).
	tools     map[string]WebhookToolCall
	toolOrder []string
	// toolDone are the calls that returned: a rich message comes again,
	// whole, as it grows, and tool IDs have no order to keep a mark by.
	toolDone map[string]bool
	// openRequests are the requests seen pending, until they close.
	openRequests map[string]bool
	// prompts counts the terminal dialogs, for their IDs.
	prompts int
	// lastError is the number of the latest error taken in; a new
	// subscription replays the ones before it.
	lastError uint64
	// started and ended are the last tasks announced.
	started, ended mark
	// replies are the texts of the agent's messages, for the replies of
	// the tasks under way.
	replies map[int]string
}

type messageKey struct {
	id   int
	time time.Time
}

// mark is how far through the messages, or the tasks, the observer got.
// Their IDs grow; one is given out again only after the last was taken
// back, and a new conversation starts over, both with later times. So an
// item before the mark with a time not after it, or the mark itself, was
// handled.
type mark messageKey

func (m mark) covers(k messageKey) bool {
	return k == messageKey(m) || (k.id < m.id && !k.time.After(m.time))
}

func (m *mark) advance(k messageKey) {
	if k.id >= m.id || k.time.After(m.time) {
		*m = mark(k)
	}
}

func newWebhookObserver(webhook *webhookDispatcher, emitter *EventEmitter, now time.Time) *webhookObserver {
	return &webhookObserver{
		webhook:      webhook,
		emitter:      emitter,
		since:        now,
		tools:        map[string]WebhookToolCall{},
		toolDone:     map[string]bool{},
		openRequests: map[string]bool{},
		replies:      map[int]string{},
	}
}

// webhookObservedEvents are the events the observer reads: not the screen,
// heartbeats or the summary, which the emitter would work out for it.
func webhookObservedEvents(t EventType) bool {
	return t != EventTypeScreenUpdate && t != EventTypeHeartbeat && t != EventTypeSummaryUpdate
}

// run reads the conversation's events until ctx ends. The state at the
// start is history; a subscription the emitter ended (its buffer filled, or
// the conversation was rebuilt) is taken up again, and what changed
// meanwhile is sent then: the replay holds what was sent already too,
// which the handlers recognize.
func (o *webhookObserver) run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		id, events, replay := o.emitter.SubscribeTo(webhookObservedEvents, 0, "")
		for _, event := range replay {
			if _, ok := event.Payload.(SessionSyncBody); ok {
				// A replay begins with one; only a reset sends one
				// later.
				continue
			}
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
	case SessionSyncBody:
		o.newConversation()
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
	case TaskUpdateBody:
		o.taskUpdated(payload.Task, quiet)
	}
}

// newConversation forgets the conversation a reset (a restart, a session
// switch) replaced; its last message goes out first, unless it did.
func (o *webhookObserver) newConversation() {
	o.flushAgent()
	o.sent, o.started, o.ended = mark{}, mark{}, mark{}
	clear(o.tools)
	o.toolOrder = nil
	clear(o.toolDone)
	clear(o.openRequests)
	clear(o.replies)
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
	// The agent's message is final once it is done; the task's end, which
	// the emitter sends next, carries it as the reply.
	if (previous.Status == AgentStatusRunning && next.Status == AgentStatusStable) ||
		next.Lifecycle == LifecycleExited || next.Lifecycle == LifecycleFailed {
		o.flushAgent()
	}
}

func (o *webhookObserver) promptID() string {
	return fmt.Sprintf("terminal-%d", o.prompts)
}

func (o *webhookObserver) messageUpdated(msg MessageUpdateBody, quiet bool) {
	key := messageKey{msg.Id, msg.Time}
	if quiet || msg.Time.Before(o.since) {
		o.sent.advance(key)
		return
	}
	if o.sent.covers(key) {
		return
	}
	if msg.Role != conv.RoleUser {
		if o.agent != nil && o.agent.Id != msg.Id {
			o.flushAgent()
		}
		o.agent = &msg
		o.replies[msg.Id] = msg.Message
		return
	}
	o.flushAgent()
	o.send(msg)
}

func (o *webhookObserver) flushAgent() {
	if o.agent == nil {
		return
	}
	msg := *o.agent
	o.agent = nil
	if o.sent.covers(messageKey{msg.Id, msg.Time}) || msg.Message == "" {
		return
	}
	o.send(msg)
}

func (o *webhookObserver) send(msg MessageUpdateBody) {
	o.sent.advance(messageKey{msg.Id, msg.Time})
	o.webhook.emit(webhookEventMessageCreated, WebhookMessage{
		ID: msg.Id, Role: msg.Role, Content: cut(msg.Message, webhookMessageLimit), Time: msg.Time,
		Truncated: len(msg.Message) > webhookMessageLimit,
	})
}

// taskUpdated sends task.started when a task starts, and task.completed or
// task.failed when it ends. A task from before (history) or seen in the
// first replay isn't news.
func (o *webhookObserver) taskUpdated(task Task, quiet bool) {
	key := messageKey{task.ID, task.StartedAt}
	if quiet || task.StartedAt.Before(o.since) {
		o.started.advance(key)
		if task.Final() {
			o.ended.advance(key)
		}
		return
	}
	if !o.started.covers(key) {
		o.started.advance(key)
		o.webhook.emit(webhookEventTaskStarted, o.webhookTask(task))
	}
	if !task.Final() || o.ended.covers(key) {
		return
	}
	o.ended.advance(key)
	eventType := webhookEventTaskCompleted
	if task.Status != TaskStatusCompleted {
		eventType = webhookEventTaskFailed
	}
	data := o.webhookTask(task)
	if o.taskCost != nil && o.webhook.wants(eventType) {
		data.Cost = o.taskCost(task.StartedAt)
	}
	o.webhook.emit(eventType, data)
	// The replies of earlier tasks aren't needed anymore.
	for id := range o.replies {
		if id < task.ID {
			delete(o.replies, id)
		}
	}
}

// webhookTask is the webhook data of task.
func (o *webhookObserver) webhookTask(task Task) WebhookTask {
	data := WebhookTask{Task: task, SessionName: o.status.SessionName}
	if task.EndedAt != nil {
		data.DurationSeconds = task.EndedAt.Sub(task.StartedAt).Seconds()
	}
	if task.ReplyID != nil {
		data.Reply = cut(o.replies[*task.ReplyID], webhookMessageLimit)
	}
	return data
}

func (o *webhookObserver) richMessageUpdated(msg RichMessageUpdateBody, quiet bool) {
	history := quiet || msg.Timestamp.Before(o.since)
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			o.toolStarted(block)
		case "tool_result":
			if o.toolDone[block.ToolUseID] || toolRunning(block.Status) {
				continue
			}
			o.toolDone[block.ToolUseID] = true
			call := o.tools[block.ToolUseID]
			delete(o.tools, block.ToolUseID)
			if history {
				continue
			}
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

// toolStarted keeps a call until its result comes, for its name and input.
func (o *webhookObserver) toolStarted(block transcript.RichContentBlock) {
	id := block.ToolUseID
	if _, ok := o.tools[id]; ok || o.toolDone[id] {
		return
	}
	o.tools[id] = WebhookToolCall{ToolUseID: id, Name: block.ToolName, Input: toolInput(block.ToolInput)}
	o.toolOrder = append(o.toolOrder, id)
	for len(o.tools) > maxOpenTools {
		delete(o.tools, o.toolOrder[0])
		o.toolOrder = o.toolOrder[1:]
	}
	if len(o.toolOrder) > 2*maxOpenTools {
		o.toolOrder = slices.DeleteFunc(o.toolOrder, func(id string) bool {
			_, open := o.tools[id]
			return !open
		})
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
	wasOpen := o.openRequests[state.ID]
	open := state.Status == native.RequestStatusPending
	if open {
		o.openRequests[state.ID] = true
	} else {
		// A request doesn't open again.
		delete(o.openRequests, state.ID)
	}
	if quiet || open == wasOpen {
		return
	}
	if open {
		o.webhook.emit(webhookEventRequestOpened, state)
	} else {
		o.webhook.emit(webhookEventRequestClosed, state)
	}
}

func (o *webhookObserver) agentError(err ErrorBody, quiet bool) {
	if err.number != 0 && err.number <= o.lastError {
		// Replayed by a new subscription.
		return
	}
	o.lastError = max(o.lastError, err.number)
	if quiet || err.Time.Before(o.since) {
		return
	}
	o.webhook.emit(webhookEventAgentError, err)
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
