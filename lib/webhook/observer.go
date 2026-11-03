package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
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
	convstate.Task
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

// webhookObserverEvents are the events the observer makes of the
// conversation's; the dispatcher sends run.status_changed and the
// instance.* events itself.
var webhookObserverEvents = []string{
	webhookEventTaskStarted, webhookEventTaskCompleted, webhookEventTaskFailed,
	webhookEventRequestOpened, webhookEventRequestClosed,
	webhookEventLoginRequired, webhookEventLoginCompleted,
	webhookEventLimitStopped, webhookEventLimitCleared,
	webhookEventAgentError,
	webhookEventMessageCreated, webhookEventToolCompleted,
}

const (
	// maxOpenTools caps the tool calls the observer keeps until their
	// result comes: one whose result never comes (an interrupted turn)
	// isn't kept for good.
	maxOpenTools = 64
	// maxToolMessages caps the rich messages whose returned calls the
	// observer remembers: only the latest still grow.
	maxToolMessages = 64
)

// Observe turns the conversation's events, read from source, into webhook
// events (task.*, request.*, message.*, tool.*, login.*, limit.*,
// agent.error) while the webhook sends any of them, until ctx ends. What
// came before since is history and isn't sent. taskCost, if set, gives
// what the session's model calls since a time cost, for task.completed and
// task.failed. Call it once, before Start.
func (d *Dispatcher) Observe(ctx context.Context, source Source, since time.Time, taskCost func(since time.Time) *WebhookCost) {
	observing := &webhookObserving{ctx: ctx, webhook: d, run: func(ctx context.Context) {
		observer := newWebhookObserver(d, source, since)
		observer.taskCost = taskCost
		observer.run(ctx)
	}}
	d.configured = observing.sync
	observing.sync()
}

// webhookObserving runs an observer while the webhook sends events it
// makes (webhookObserverEvents): otherwise nobody hears of the
// conversation, and an observer would only keep track of it. A hub's
// webhook sends instance.* alone.
type webhookObserving struct {
	ctx     context.Context
	webhook *Dispatcher
	// run runs an observer until its context ends.
	run func(ctx context.Context)

	mu sync.Mutex
	// stop ends the running observer and waits for it; nil without one.
	stop func()
}

// sync starts or stops the observer after the webhook's configuration
// changed: its URL or its events.
func (w *webhookObserving) sync() {
	w.mu.Lock()
	defer w.mu.Unlock()
	wanted := slices.ContainsFunc(webhookObserverEvents, w.webhook.wants)
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

// Source is what the observer reads of the conversation: the events of a
// convstate.EventEmitter, which it subscribes to like an /events client.
type Source interface {
	SubscribeTo(wants func(convstate.EventType) bool, since uint64, epoch string) (int, <-chan convstate.Event, []convstate.Event)
	Unsubscribe(id int)
}

// webhookObserver turns the conversation's events into webhook events. It
// reads the emitter like an /events client.
type webhookObserver struct {
	webhook *Dispatcher
	emitter Source
	// taskCost, if set, gives what the session's model calls since a time
	// cost.
	taskCost func(since time.Time) *WebhookCost

	// since: messages and tool calls from before it are history (a
	// restored or resumed conversation), not news.
	since time.Time
	// epoch and seq are how far through the emitter's changes the observer
	// read: a subscription taken up again starts from there.
	epoch  string
	seq    uint64
	status convstate.StatusChangeBody
	// agent is the agent's latest message, sent as message.created once
	// it is final: when another follows or the turn ends.
	agent *convstate.MessageUpdateBody
	// sent is the last message sent (or taken in as history).
	sent mark
	// tools are the calls waiting for their result, toolOrder their IDs
	// as they came (and some that returned, until it is compacted).
	tools     map[string]WebhookToolCall
	toolOrder []string
	// toolDone are the calls that returned, and toolMessages the rich
	// messages that hold their results, the latest last, with those calls:
	// a rich message comes again, whole, as it grows, and tool IDs have no
	// order to keep a mark by. Only the latest maxToolMessages are kept;
	// an older message doesn't grow, and a replay holds only what changed.
	toolDone     map[string]bool
	toolMessages []toolMessage
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

// richKey identifies a rich message, which comes again as it grows.
type richKey struct{ id, role string }

// toolMessage is a rich message that holds results of tool calls, and the
// calls whose result it brought.
type toolMessage struct {
	key   richKey
	calls []string
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

func newWebhookObserver(webhook *Dispatcher, emitter Source, now time.Time) *webhookObserver {
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
func webhookObservedEvents(t convstate.EventType) bool {
	return t != convstate.EventTypeScreenUpdate && t != convstate.EventTypeHeartbeat && t != convstate.EventTypeSummaryUpdate
}

// run reads the conversation's events until ctx ends. The state at the
// start is history. A subscription the emitter ended (its buffer filled, or
// the conversation was rebuilt) is taken up again from where the observer
// got, and what changed meanwhile is sent then.
func (o *webhookObserver) run(ctx context.Context) {
	first := true
	for ctx.Err() == nil {
		id, events, replay := o.subscribe()
		o.replay(replay, first)
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
				o.take(event)
			}
		}
	}
}

// subscribe subscribes to the emitter from where the observer got: the
// replay holds the changes since, unless the conversation got a new epoch.
func (o *webhookObserver) subscribe() (int, <-chan convstate.Event, []convstate.Event) {
	return o.emitter.SubscribeTo(webhookObservedEvents, o.seq, o.epoch)
}

// replay takes the events a subscription begins with; quiet takes in the
// state without sending anything. After a new epoch (a message was taken
// back) the replay holds the whole conversation, whose changes up to the
// seq the observer got to it read already.
func (o *webhookObserver) replay(events []convstate.Event, quiet bool) {
	last := o.seq
	for _, event := range events {
		if sync, ok := event.Payload.(convstate.SessionSyncBody); ok {
			// A replay begins with one; only a reset sends one later.
			o.epoch = sync.Epoch
			o.seq = max(o.seq, sync.Seq)
			continue
		}
		if seq := event.Seq(); seq != 0 && seq <= last {
			continue
		}
		o.handle(event, quiet)
	}
}

// take takes an event that came after the replay.
func (o *webhookObserver) take(event convstate.Event) {
	if sync, ok := event.Payload.(convstate.SessionSyncBody); ok {
		o.epoch = sync.Epoch
	}
	o.seq = max(o.seq, event.Seq())
	o.handle(event, false)
}

// handle takes one event; quiet takes in the state without sending
// anything.
func (o *webhookObserver) handle(event convstate.Event, quiet bool) {
	switch payload := event.Payload.(type) {
	case convstate.SessionSyncBody:
		o.newConversation()
	case convstate.StatusChangeBody:
		o.statusChanged(payload, quiet)
	case convstate.MessageUpdateBody:
		o.messageUpdated(payload, quiet)
	case convstate.RichMessageUpdateBody:
		o.richMessageUpdated(payload, quiet)
	case convstate.RequestUpdateBody:
		o.requestUpdated(payload.RequestState, quiet)
	case convstate.ErrorBody:
		o.agentError(payload, quiet)
	case convstate.TaskUpdateBody:
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
	o.toolMessages = nil
	clear(o.openRequests)
	clear(o.replies)
}

func (o *webhookObserver) statusChanged(next convstate.StatusChangeBody, quiet bool) {
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
	if (previous.Status == convstate.AgentStatusRunning && next.Status == convstate.AgentStatusStable) ||
		next.Lifecycle == convstate.LifecycleExited || next.Lifecycle == convstate.LifecycleFailed {
		o.flushAgent()
	}
}

func (o *webhookObserver) promptID() string {
	return fmt.Sprintf("terminal-%d", o.prompts)
}

func (o *webhookObserver) messageUpdated(msg convstate.MessageUpdateBody, quiet bool) {
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

func (o *webhookObserver) send(msg convstate.MessageUpdateBody) {
	o.sent.advance(messageKey{msg.Id, msg.Time})
	o.webhook.emit(webhookEventMessageCreated, WebhookMessage{
		ID: msg.Id, Role: msg.Role, Content: cut(msg.Message, webhookMessageLimit), Time: msg.Time,
		Truncated: len(msg.Message) > webhookMessageLimit,
	})
}

// taskUpdated sends task.started when a task starts, and task.completed or
// task.failed when it ends. A task from before (history) or seen in the
// first replay isn't news.
func (o *webhookObserver) taskUpdated(task convstate.Task, quiet bool) {
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
	if task.Status != convstate.TaskStatusCompleted {
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
func (o *webhookObserver) webhookTask(task convstate.Task) WebhookTask {
	data := WebhookTask{Task: task, SessionName: o.status.SessionName}
	if task.EndedAt != nil {
		data.DurationSeconds = task.EndedAt.Sub(task.StartedAt).Seconds()
	}
	if task.ReplyID != nil {
		data.Reply = cut(o.replies[*task.ReplyID], webhookMessageLimit)
	}
	return data
}

func (o *webhookObserver) richMessageUpdated(msg convstate.RichMessageUpdateBody, quiet bool) {
	history := quiet || msg.Timestamp.Before(o.since)
	results := false
	var returned []string
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			o.toolStarted(block)
		case "tool_result":
			results = true
			if o.toolDone[block.ToolUseID] || toolRunning(block.Status) {
				continue
			}
			o.toolDone[block.ToolUseID] = true
			returned = append(returned, block.ToolUseID)
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
	if results {
		o.keepToolMessage(richKey{msg.MessageID, msg.Role}, returned)
	}
}

// keepToolMessage takes the rich message key, which holds results, as the
// latest, with the calls whose result it brought now. The earliest beyond
// maxToolMessages is forgotten, with its calls.
func (o *webhookObserver) keepToolMessage(key richKey, returned []string) {
	m := toolMessage{key: key}
	if i := slices.IndexFunc(o.toolMessages, func(m toolMessage) bool { return m.key == key }); i >= 0 {
		m = o.toolMessages[i]
		o.toolMessages = slices.Delete(o.toolMessages, i, i+1)
	}
	m.calls = append(m.calls, returned...)
	o.toolMessages = append(o.toolMessages, m)
	if len(o.toolMessages) > maxToolMessages {
		for _, id := range o.toolMessages[0].calls {
			delete(o.toolDone, id)
		}
		o.toolMessages = slices.Delete(o.toolMessages, 0, 1)
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

func (o *webhookObserver) agentError(err convstate.ErrorBody, quiet bool) {
	if err.Number() != 0 && err.Number() <= o.lastError {
		// Replayed by a new subscription.
		return
	}
	o.lastError = max(o.lastError, err.Number())
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
