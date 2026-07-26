package httpapi

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/util"
	"github.com/danielgtaylor/huma/v2"
)

type EventType string

const (
	EventTypeMessageUpdate     EventType = "message_update"
	EventTypeStatusChange      EventType = "status_change"
	EventTypeScreenUpdate      EventType = "screen_update"
	EventTypeError             EventType = "agent_error"
	EventTypeRichMessageUpdate EventType = "rich_message_update"
	EventTypeHeartbeat         EventType = "heartbeat"
)

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

type MessageUpdateBody struct {
	Id      int                 `json:"id" doc:"Unique identifier for the message. This identifier also represents the order of the message in the conversation history."`
	Role    st.ConversationRole `json:"role" doc:"Role of the message author"`
	Message string              `json:"message" doc:"Message content. The message is formatted as it appears in the agent's terminal session, meaning that, by default, it consists of lines of text with 80 characters per line."`
	Time    time.Time           `json:"time" doc:"Timestamp of the message"`
}

type StatusChangeBody struct {
	Status    AgentStatus  `json:"status" doc:"Agent status"`
	AgentType mf.AgentType `json:"agent_type" doc:"Type of the agent being used by the server."`
}

type ScreenUpdateBody struct {
	Screen string `json:"screen"`
}

type ErrorBody struct {
	Message string        `json:"message" doc:"Error message"`
	Level   st.ErrorLevel `json:"level" doc:"Error level"`
	Time    time.Time     `json:"time" doc:"Timestamp when the error occurred"`
}

// RichMessageUpdateBody is the SSE payload for rich message updates.
type RichMessageUpdateBody = jsonlwatcher.RichMessage

// HeartbeatBody is a periodic SSE keep-alive. It lets clients detect
// connections that died without a FIN (e.g. after system sleep), which
// otherwise never produce an error on the client side.
type HeartbeatBody struct {
	Time time.Time `json:"time" doc:"Server time when the heartbeat was sent"`
}

type Event struct {
	Type    EventType
	Payload any
}

type EventEmitter struct {
	mu                  sync.Mutex
	messages            []st.ConversationMessage
	richMessages        []jsonlwatcher.RichMessage
	sessionEvents       []jsonlwatcher.SessionEvent
	nextSessionEventID  int
	status              AgentStatus
	agentType           mf.AgentType
	chans               map[int]chan Event
	chanIdx             int
	subscriptionBufSize uint
	screen              string
	errors              []ErrorBody
	clock               quartz.Clock
}

func convertStatus(status st.ConversationStatus) AgentStatus {
	switch status {
	case st.ConversationStatusInitializing:
		return AgentStatusRunning
	case st.ConversationStatusStable:
		return AgentStatusStable
	case st.ConversationStatusChanging:
		return AgentStatusRunning
	default:
		panic(fmt.Sprintf("unknown conversation status: %s", status))
	}
}

const defaultSubscriptionBufSize uint = 1024

// maxStoredErrors caps the number of errors retained for late subscribers.
const maxStoredErrors = 100

type EventEmitterOption func(*EventEmitter)

func WithSubscriptionBufSize(size uint) EventEmitterOption {
	return func(e *EventEmitter) {
		if size == 0 {
			e.subscriptionBufSize = defaultSubscriptionBufSize
		} else {
			e.subscriptionBufSize = size
		}
	}
}

func WithAgentType(agentType mf.AgentType) EventEmitterOption {
	return func(e *EventEmitter) {
		e.agentType = agentType
	}
}

func WithClock(clock quartz.Clock) EventEmitterOption {
	return func(e *EventEmitter) {
		e.clock = clock
	}
}

func NewEventEmitter(opts ...EventEmitterOption) *EventEmitter {
	e := &EventEmitter{
		messages:            make([]st.ConversationMessage, 0),
		sessionEvents:       make([]jsonlwatcher.SessionEvent, 0),
		nextSessionEventID:  1,
		status:              AgentStatusRunning,
		chans:               make(map[int]chan Event),
		subscriptionBufSize: defaultSubscriptionBufSize,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.clock == nil {
		e.clock = quartz.NewReal()
	}
	return e
}

// Assumes the caller holds the lock.
func (e *EventEmitter) notifyChannels(eventType EventType, payload any) {
	chanIds := make([]int, 0, len(e.chans))
	for chanId := range e.chans {
		chanIds = append(chanIds, chanId)
	}
	for _, chanId := range chanIds {
		ch := e.chans[chanId]
		event := Event{
			Type:    eventType,
			Payload: payload,
		}

		select {
		case ch <- event:
		default:
			// If the channel is full, close it.
			// Listeners must actively drain the channel.
			e.unsubscribeInner(chanId)
		}
	}
}

// EmitMessages assumes that only the last message can change or new messages can be added.
// If a new message is injected between existing messages (identified by Id), the behavior is undefined.
func (e *EventEmitter) EmitMessages(newMessages []st.ConversationMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()

	maxLength := max(len(e.messages), len(newMessages))
	for i := range maxLength {
		var oldMsg st.ConversationMessage
		var newMsg st.ConversationMessage
		if i < len(e.messages) {
			oldMsg = e.messages[i]
		}
		if i < len(newMessages) {
			newMsg = newMessages[i]
		}
		if oldMsg != newMsg {
			if i >= len(newMessages) {
				continue
			}
			e.notifyChannels(EventTypeMessageUpdate, MessageUpdateBody{
				Id:      newMessages[i].Id,
				Role:    newMessages[i].Role,
				Message: newMessages[i].Message,
				Time:    newMessages[i].Time,
			})
		}
	}

	e.messages = newMessages
}

func (e *EventEmitter) EmitStatus(newStatus st.ConversationStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newAgentStatus := convertStatus(newStatus)
	if e.status == newAgentStatus {
		return
	}

	e.notifyChannels(EventTypeStatusChange, StatusChangeBody{Status: newAgentStatus, AgentType: e.agentType})
	e.status = newAgentStatus
}

func (e *EventEmitter) EmitScreen(newScreen string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.screen == newScreen {
		return
	}

	e.notifyChannels(EventTypeScreenUpdate, ScreenUpdateBody{Screen: strings.TrimRight(newScreen, mf.WhiteSpaceChars)})
	e.screen = newScreen
}

func (e *EventEmitter) EmitError(message string, level st.ErrorLevel) {
	e.mu.Lock()
	defer e.mu.Unlock()

	errorBody := ErrorBody{
		Message: message,
		Level:   level,
		Time:    e.clock.Now(),
	}

	// Store the error so new subscribers can receive recent errors.
	e.errors = append(e.errors, errorBody)
	if len(e.errors) > maxStoredErrors {
		e.errors = e.errors[len(e.errors)-maxStoredErrors:]
	}

	e.notifyChannels(EventTypeError, errorBody)
}

// EmitRichMessage emits a rich structured message from the JSONL watcher.
// It stores the message for late subscriber replay. Messages are upserted
// by (MessageID, Role) because parsers re-emit a message as its content
// accumulates (e.g. Codex turn updates).
func (e *EventEmitter) EmitRichMessage(msg jsonlwatcher.RichMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()

	idx := -1
	// Search from the end: updates target recent messages.
	for i := len(e.richMessages) - 1; i >= 0; i-- {
		if e.richMessages[i].MessageID == msg.MessageID && e.richMessages[i].Role == msg.Role {
			idx = i
			break
		}
	}
	if idx >= 0 {
		e.richMessages[idx] = msg
	} else {
		e.richMessages = append(e.richMessages, msg)
	}
	e.notifyChannels(EventTypeRichMessageUpdate, RichMessageUpdateBody(msg))
}

// RichMessages returns a snapshot of all rich messages received so far.
// The emitter is the single store for rich messages: the JSONL watcher
// parses and emits, the emitter deduplicates, replays, and serves reads.
func (e *EventEmitter) RichMessages() []jsonlwatcher.RichMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.richMessages)
}

// EmitSessionEvents stores normalized events in the same order as the source
// JSONL records and assigns stable identifiers for the lifetime of this run.
func (e *EventEmitter) EmitSessionEvents(events []jsonlwatcher.SessionEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, event := range events {
		event.EventID = e.nextSessionEventID
		e.nextSessionEventID++
		e.sessionEvents = append(e.sessionEvents, event)
	}
}

func (e *EventEmitter) SessionEvents() []jsonlwatcher.SessionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sessionEvents)
}

// Assumes the caller holds the lock.
func (e *EventEmitter) currentStateAsEvents() []Event {
	events := make([]Event, 0, len(e.messages)+2)
	for _, msg := range e.messages {
		events = append(events, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: msg.Id, Role: msg.Role, Message: msg.Message, Time: msg.Time},
		})
	}
	events = append(events, Event{
		Type:    EventTypeStatusChange,
		Payload: StatusChangeBody{Status: e.status, AgentType: e.agentType},
	})
	events = append(events, Event{
		Type:    EventTypeScreenUpdate,
		Payload: ScreenUpdateBody{Screen: strings.TrimRight(e.screen, mf.WhiteSpaceChars)},
	})

	// Include all error events
	for _, err := range e.errors {
		events = append(events, Event{
			Type:    EventTypeError,
			Payload: err,
		})
	}

	// Include all rich message events for late subscriber replay
	for _, msg := range e.richMessages {
		events = append(events, Event{
			Type:    EventTypeRichMessageUpdate,
			Payload: RichMessageUpdateBody(msg),
		})
	}

	return events
}

// Subscribe returns:
// - a subscription ID that can be used to unsubscribe.
// - a channel for receiving events.
// - a list of events that allow to recreate the state of the conversation right before the subscription was created.
func (e *EventEmitter) Subscribe() (int, <-chan Event, []Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	stateEvents := e.currentStateAsEvents()

	// Once a channel becomes full, it will be closed.
	ch := make(chan Event, e.subscriptionBufSize)
	e.chans[e.chanIdx] = ch
	e.chanIdx++
	return e.chanIdx - 1, ch, stateEvents
}

// Assumes the caller holds the lock.
func (e *EventEmitter) unsubscribeInner(chanId int) {
	ch, ok := e.chans[chanId]
	if !ok {
		return
	}
	close(ch)
	delete(e.chans, chanId)
}

func (e *EventEmitter) Unsubscribe(chanId int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unsubscribeInner(chanId)
}
