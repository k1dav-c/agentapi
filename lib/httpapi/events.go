package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/danielgtaylor/huma/v2"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/transcript"
	"github.com/coder/agentapi/lib/util"
)

type EventType string

const (
	EventTypeMessageUpdate     EventType = "message_update"
	EventTypeStatusChange      EventType = "status_change"
	EventTypeScreenUpdate      EventType = "screen_update"
	EventTypeError             EventType = "agent_error"
	EventTypeRichMessageUpdate EventType = "rich_message_update"
	EventTypeHeartbeat         EventType = "heartbeat"
	EventTypeSessionSync       EventType = "session_sync"
	EventTypeRequestUpdate     EventType = "request_update"
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

type MessageUpdateBody struct {
	Id      int                 `json:"id" doc:"Unique identifier for the message. This identifier also represents the order of the message in the conversation history."`
	Role    st.ConversationRole `json:"role" doc:"Role of the message author"`
	Message string              `json:"message" doc:"Message content. The message is formatted as it appears in the agent's terminal session, meaning that, by default, it consists of lines of text with 80 characters per line."`
	Time    time.Time           `json:"time" doc:"Timestamp of the message"`
	Seq     uint64              `json:"seq" doc:"Sequence number of this change. Pass the highest one seen as ?since= when reconnecting to /events to receive only later changes."`
}

// SessionSyncBody is the first event on /events. It tells the client whether
// the replay that follows is the full state (discard anything cached) or
// only the changes after the ?since= sequence it asked for. It is sent
// again, with a new epoch, when the conversation is reset or changes in a
// way updates can't express (a message was removed); the full state
// follows it then.
type SessionSyncBody struct {
	Epoch string `json:"epoch" doc:"Identifies this conversation state. It changes when the conversation is reset or rebuilt, and when the server restarts (with --state-db, only after an unclean exit); a cache from another epoch must be discarded."`
	Full  bool   `json:"full" doc:"True when the replay that follows is the full state rather than the changes after ?since=."`
	Seq   uint64 `json:"seq" doc:"Highest sequence number in the replay that follows (the ?since= value when it holds nothing new). The replay is grouped by kind, not in seq order: it is complete at its last event, the first to carry an SSE id, and only then can a client resume from this seq."`
}

type StatusChangeBody struct {
	Status    AgentStatus    `json:"status" doc:"Backward-compatible agent activity status."`
	Lifecycle LifecycleState `json:"lifecycle" doc:"Detailed process lifecycle state."`
	SessionID string         `json:"session_id" doc:"Identifier for this AgentAPI server session."`
	RunID     uint64         `json:"run_id" doc:"Monotonically increasing run identifier within the session."`
	AgentType mf.AgentType   `json:"agent_type" doc:"Type of the agent being used by the server."`
	Transport Transport      `json:"transport" doc:"Backend transport being used ('pty', 'acp' or 'native')."`
	Version   string         `json:"agentapi_version" doc:"Version of the AgentAPI server."`
	// TerminalPrompt is the bottom of the screen while the agent shows an
	// interactive prompt (selection list, confirmation) instead of its input
	// box, and empty otherwise.
	TerminalPrompt string `json:"terminal_prompt" doc:"Bottom of the terminal while the agent is showing an interactive prompt (selection list, confirmation dialog) that must be answered; empty otherwise."`
	// SessionName is what the agent calls the session, for telling
	// sessions apart.
	SessionName string `json:"session_name,omitempty" doc:"Name the agent gave the session: Claude Code's title (or its /rename), Codex's thread name or Pi's /name. Empty until the agent names it."`
	// TerminalColumns lets a terminal mirror wrap lines where the agent's
	// terminal does.
	TerminalColumns uint16 `json:"terminal_columns,omitempty" doc:"Width of the emulated terminal the agent runs in, in columns (PTY transport only)."`
	// PendingRequests counts the structured requests (native transport)
	// the agent is waiting on.
	PendingRequests int `json:"pending_requests,omitempty" doc:"Number of requests (approvals, questions, plans) the agent is waiting on. Native transport only; see GET /requests."`
	// Capabilities is set for the native transport.
	Capabilities *native.Capabilities `json:"capabilities,omitempty" doc:"Optional operations the agent supports. Native transport only."`
	// LoginRequired is set while the agent can't work until the user logs
	// in (native transport).
	LoginRequired *native.LoginRequired `json:"login_required,omitempty" doc:"Set while the agent waits for the user to log in: the command to run in a terminal on the machine AgentAPI runs on. Native transport only."`
	// Settings is set for agents whose settings can change (native
	// transport).
	Settings *native.Settings `json:"settings,omitempty" doc:"The agent's permission mode, model and reasoning effort, for agents with the settings capability; see GET /settings. Native transport only."`
	// Usage is set once the agent reports it (native transport).
	Usage *native.AgentUsage `json:"usage,omitempty" doc:"How full the agent's context window is and the account's rate limits, as the agent last reported them. Native transport only."`
	// LimitStop is set while a turn a usage limit stopped holds the queue.
	LimitStop *native.LimitStop `json:"limit_stop,omitempty" doc:"Set when the agent's last turn stopped because the account reached a usage limit, until a turn starts or the stop is dismissed (DELETE /limit). Native transport only."`
	// Terminal says whether GET /terminal has a shell.
	Terminal bool `json:"terminal,omitempty" doc:"A shell terminal in the agent's working directory is available at GET /terminal. Native transport only."`
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
type RichMessageUpdateBody struct {
	transcript.RichMessage
	Seq uint64 `json:"seq" doc:"Sequence number of this change; see message_update."`
}

// RequestUpdateBody is the SSE payload for a structured request that was
// opened or changed state.
type RequestUpdateBody struct {
	native.RequestState
	Seq uint64 `json:"seq" doc:"Sequence number of this change; see message_update."`
}

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
	mu       sync.Mutex
	messages []st.ConversationMessage
	// epoch identifies the current conversation state; seq numbers every
	// message, rich message and request change so a reconnecting client can
	// ask for only what it hasn't seen. messageSeq, richSeq and requestSeq
	// hold the seq of each entry's latest change.
	epoch               string
	seq                 uint64
	messageSeq          []uint64
	richSeq             []uint64
	richMessages        []transcript.RichMessage
	requests            []native.RequestState
	requestSeq          []uint64
	capabilities        *native.Capabilities
	loginRequired       *native.LoginRequired
	settings            *native.Settings
	usage               *native.AgentUsage
	limitStop           *native.LimitStop
	terminal            bool
	sessionEvents       []transcript.SessionEvent
	nextSessionEventID  int
	status              AgentStatus
	lifecycle           LifecycleState
	sessionID           string
	runID               uint64
	handoffRevision     uint64
	agentType           mf.AgentType
	transport           Transport
	terminalColumns     uint16
	sessionName         string
	chans               map[int]subscriber
	chanIdx             int
	subscriptionBufSize uint
	screen              string
	// screenQuestion: the screen shows a dialog of the agent's terminal
	// that waits for an answer (isTerminalQuestionScreen), worked out once
	// per screen.
	screenQuestion bool
	terminalPrompt string
	// pendingPrompt is a prompt seen on screen that hasn't been shown long
	// enough to be published; promptTimer publishes it.
	pendingPrompt  string
	promptTimer    *quartz.Timer
	errors         []ErrorBody
	clock          quartz.Clock
	onStatusChange func(previous, current AgentStatus)
	// persister, if set, stores every change of the conversation state.
	persister Persister
}

// Persister stores conversation state changes so they survive a restart.
// The emitter calls it with its lock held, so its methods must not block
// (statedb.DB queues the writes).
type Persister interface {
	PutMessage(msg st.ConversationMessage, seq uint64)
	// TruncateMessages removes the messages with an ID of at least fromID.
	TruncateMessages(fromID int)
	PutRichMessage(pos int, msg transcript.RichMessage, seq uint64)
	AppendSessionEvents(events []transcript.SessionEvent)
	PutRequest(state native.RequestState, seq uint64)
	DeleteRequest(id string)
	SetSeq(epoch string, seq uint64)
	SetSessionName(name string)
	// SetSessionRef stores what resumes the agent's session.
	SetSessionRef(ref string)
	// SetSettings stores the settings chosen for the session.
	SetSettings(settings native.Settings)
	// SetLimitStop stores the turn a usage limit stopped; nil removes it.
	SetLimitStop(ls *native.LimitStop)
	// Reset removes the conversation; epoch is the new epoch.
	Reset(epoch string)
}

// EmitterState is conversation state restored from a Persister.
type EmitterState struct {
	Epoch         string
	Seq           uint64
	Messages      []st.ConversationMessage
	MessageSeqs   []uint64
	RichMessages  []transcript.RichMessage
	RichSeqs      []uint64
	SessionEvents []transcript.SessionEvent
	Requests      []native.RequestState
	RequestSeqs   []uint64
	SessionName   string
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

// WithTransport sets the transport reported in status updates.
func WithTransport(transport Transport) EventEmitterOption {
	return func(e *EventEmitter) { e.transport = transport }
}

// WithTerminal reports in status updates that GET /terminal has a shell.
func WithTerminal() EventEmitterOption {
	return func(e *EventEmitter) { e.terminal = true }
}

// WithTerminalColumns sets the width of the agent's terminal reported in
// status updates.
func WithTerminalColumns(columns uint16) EventEmitterOption {
	return func(e *EventEmitter) { e.terminalColumns = columns }
}

func WithSessionID(sessionID string) EventEmitterOption {
	return func(e *EventEmitter) { e.sessionID = sessionID }
}

func WithClock(clock quartz.Clock) EventEmitterOption {
	return func(e *EventEmitter) {
		e.clock = clock
	}
}

// WithPersister stores every change of the conversation state with p.
func WithPersister(p Persister) EventEmitterOption {
	return func(e *EventEmitter) { e.persister = p }
}

// Restore replaces the conversation state with state, without storing it
// again. Call it before anyone subscribes.
func (e *EventEmitter) Restore(state EmitterState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if state.Epoch != "" {
		e.epoch = state.Epoch
	}
	e.seq = state.Seq
	e.messages = slices.Clone(state.Messages)
	e.messageSeq = slices.Clone(state.MessageSeqs)
	e.richMessages = slices.Clone(state.RichMessages)
	e.richSeq = slices.Clone(state.RichSeqs)
	e.sessionEvents = slices.Clone(state.SessionEvents)
	e.nextSessionEventID = 1
	for _, ev := range e.sessionEvents {
		e.nextSessionEventID = max(e.nextSessionEventID, ev.EventID+1)
	}
	e.requests = slices.Clone(state.Requests)
	e.requestSeq = slices.Clone(state.RequestSeqs)
	e.sessionName = state.SessionName
}

// Epoch identifies the current conversation state; see SessionSyncBody.
func (e *EventEmitter) Epoch() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.epoch
}

func WithStatusChangeHandler(handler func(previous, current AgentStatus)) EventEmitterOption {
	return func(e *EventEmitter) {
		e.onStatusChange = handler
	}
}

func NewEventEmitter(opts ...EventEmitterOption) *EventEmitter {
	e := &EventEmitter{
		messages:            make([]st.ConversationMessage, 0),
		sessionEvents:       make([]transcript.SessionEvent, 0),
		nextSessionEventID:  1,
		status:              AgentStatusRunning,
		lifecycle:           LifecycleStarting,
		chans:               make(map[int]subscriber),
		subscriptionBufSize: defaultSubscriptionBufSize,
		epoch:               newEpoch(),
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.clock == nil {
		e.clock = quartz.NewReal()
	}
	return e
}

// subscriber is a subscription's channel and the events it gets.
type subscriber struct {
	ch    chan Event
	wants func(EventType) bool
}

// AllEvents, ConversationEvents and ScreenEvents pick the events a
// subscription gets: all, all but the screen (GET /events), or only the
// screen (GET /internal/screen).
func AllEvents(EventType) bool            { return true }
func ConversationEvents(t EventType) bool { return t != EventTypeScreenUpdate }
func ScreenEvents(t EventType) bool       { return t == EventTypeScreenUpdate }

// Assumes the caller holds the lock.
func (e *EventEmitter) notifyChannels(eventType EventType, payload any) {
	event := Event{Type: eventType, Payload: payload}
	for chanId, sub := range e.chans {
		if !sub.wants(eventType) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			// If the channel is full, close it.
			// Listeners must actively drain the channel.
			e.unsubscribeInner(chanId)
		}
	}
}

func newEpoch() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Reset clears all conversation state — messages, rich messages, requests,
// timeline, errors, and screen. Used when restarting with a clean slate.
func (e *EventEmitter) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handoffRevision++
	e.messages = nil
	e.messageSeq = nil
	e.richMessages = nil
	e.richSeq = nil
	e.requests = nil
	e.requestSeq = nil
	// A new epoch makes reconnecting clients drop their cache; connected
	// clients are told now so they clear the old conversation.
	e.epoch = newEpoch()
	if e.persister != nil {
		e.persister.Reset(e.epoch)
	}
	e.notifyChannels(EventTypeSessionSync, SessionSyncBody{Epoch: e.epoch, Full: true})
	e.sessionEvents = nil
	e.nextSessionEventID = 1
	e.errors = nil
	e.screen = ""
	e.terminalPrompt = ""
	e.sessionName = ""
	e.pendingPrompt = ""
	if e.promptTimer != nil {
		e.promptTimer.Stop()
		e.promptTimer = nil
	}
}

// currentStateAsEvents returns the events that rebuild the current state for
// a new subscriber. With a matching epoch, only messages, rich messages and
// requests changed after since are included; otherwise everything is.
// Caller holds e.mu.
func (e *EventEmitter) currentStateAsEvents(since uint64, epoch string) []Event {
	// since 0 asks for everything: the client must replace its state rather
	// than merge into what it holds.
	full := epoch != e.epoch || since == 0 || since > e.seq
	if full {
		since = 0
	}
	events := make([]Event, 0, len(e.messages)+3)
	// The session sync goes first; its Seq is filled in below with the
	// highest seq in the replay, so the client knows when the replay is
	// complete and can apply it at once.
	events = append(events, Event{Type: EventTypeSessionSync})
	replaySeq := since
	for i, msg := range e.messages {
		if e.messageSeq[i] <= since {
			continue
		}
		replaySeq = max(replaySeq, e.messageSeq[i])
		events = append(events, Event{
			Type:    EventTypeMessageUpdate,
			Payload: MessageUpdateBody{Id: msg.Id, Role: msg.Role, Message: msg.Message, Time: msg.Time, Seq: e.messageSeq[i]},
		})
	}
	events = append(events, Event{
		Type:    EventTypeStatusChange,
		Payload: e.statusChangeBody(),
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

	// Include rich message events for late subscriber replay
	for i, msg := range e.richMessages {
		if e.richSeq[i] <= since {
			continue
		}
		replaySeq = max(replaySeq, e.richSeq[i])
		events = append(events, Event{
			Type:    EventTypeRichMessageUpdate,
			Payload: RichMessageUpdateBody{RichMessage: msg, Seq: e.richSeq[i]},
		})
	}

	for i, req := range e.requests {
		if e.requestSeq[i] <= since {
			continue
		}
		replaySeq = max(replaySeq, e.requestSeq[i])
		events = append(events, Event{
			Type:    EventTypeRequestUpdate,
			Payload: RequestUpdateBody{RequestState: req, Seq: e.requestSeq[i]},
		})
	}

	events[0].Payload = SessionSyncBody{Epoch: e.epoch, Full: full, Seq: replaySeq}
	return events
}

// Subscribe returns:
// - a subscription ID that can be used to unsubscribe.
// - a channel for receiving events.
// - a list of events that allow to recreate the state of the conversation right before the subscription was created.
func (e *EventEmitter) Subscribe() (int, <-chan Event, []Event) {
	return e.SubscribeTo(AllEvents, 0, "")
}

// SubscribeSince is Subscribe for a client that already has the state up to
// sequence since in epoch: the returned events start with a session_sync and
// hold only later changes, or the full state if the epoch doesn't match.
func (e *EventEmitter) SubscribeSince(since uint64, epoch string) (int, <-chan Event, []Event) {
	return e.SubscribeTo(AllEvents, since, epoch)
}

// SubscribeTo is SubscribeSince for the events wants accepts: neither the
// channel nor the replay holds others.
func (e *EventEmitter) SubscribeTo(wants func(EventType) bool, since uint64, epoch string) (int, <-chan Event, []Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	stateEvents := slices.DeleteFunc(e.currentStateAsEvents(since, epoch), func(event Event) bool { return !wants(event.Type) })

	// Once a channel becomes full, it will be closed.
	ch := make(chan Event, e.subscriptionBufSize)
	e.chans[e.chanIdx] = subscriber{ch: ch, wants: wants}
	e.chanIdx++
	return e.chanIdx - 1, ch, stateEvents
}

// Assumes the caller holds the lock.
func (e *EventEmitter) unsubscribeInner(chanId int) {
	sub, ok := e.chans[chanId]
	if !ok {
		return
	}
	close(sub.ch)
	delete(e.chans, chanId)
}

func (e *EventEmitter) Unsubscribe(chanId int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unsubscribeInner(chanId)
}
