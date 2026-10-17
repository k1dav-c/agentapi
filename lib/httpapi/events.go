package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/jsonlwatcher"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
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
	Seq   uint64 `json:"seq" doc:"Highest sequence number in the replay that follows (the ?since= value when it holds nothing new). The replay is complete once a change with this seq has arrived."`
}

type StatusChangeBody struct {
	Status    AgentStatus    `json:"status" doc:"Backward-compatible agent activity status."`
	Lifecycle LifecycleState `json:"lifecycle" doc:"Detailed process lifecycle state."`
	SessionID string         `json:"session_id" doc:"Identifier for this AgentAPI server session."`
	RunID     uint64         `json:"run_id" doc:"Monotonically increasing run identifier within the session."`
	AgentType mf.AgentType   `json:"agent_type" doc:"Type of the agent being used by the server."`
	Transport Transport      `json:"transport,omitempty" doc:"Backend transport being used ('pty', 'acp' or 'native')."`
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
	jsonlwatcher.RichMessage
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
	richMessages        []jsonlwatcher.RichMessage
	requests            []native.RequestState
	requestSeq          []uint64
	capabilities        *native.Capabilities
	loginRequired       *native.LoginRequired
	settings            *native.Settings
	usage               *native.AgentUsage
	sessionEvents       []jsonlwatcher.SessionEvent
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
	chans               map[int]chan Event
	chanIdx             int
	subscriptionBufSize uint
	screen              string
	terminalPrompt      string
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
	PutRichMessage(pos int, msg jsonlwatcher.RichMessage, seq uint64)
	AppendSessionEvents(events []jsonlwatcher.SessionEvent)
	PutRequest(state native.RequestState, seq uint64)
	DeleteRequest(id string)
	SetSeq(epoch string, seq uint64)
	SetSessionName(name string)
	// SetSessionRef stores what resumes the agent's session.
	SetSessionRef(ref string)
	// SetSettings stores the settings chosen for the session.
	SetSettings(settings native.Settings)
	// Reset removes the conversation; epoch is the new epoch.
	Reset(epoch string)
}

// EmitterState is conversation state restored from a Persister.
type EmitterState struct {
	Epoch         string
	Seq           uint64
	Messages      []st.ConversationMessage
	MessageSeqs   []uint64
	RichMessages  []jsonlwatcher.RichMessage
	RichSeqs      []uint64
	SessionEvents []jsonlwatcher.SessionEvent
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
		sessionEvents:       make([]jsonlwatcher.SessionEvent, 0),
		nextSessionEventID:  1,
		status:              AgentStatusRunning,
		lifecycle:           LifecycleStarting,
		chans:               make(map[int]chan Event),
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

func newEpoch() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// EmitMessages publishes the conversation's messages, which are ordered by
// increasing ID. Changed and new messages are sent as updates. Messages can
// also be removed (the native transport takes back a message the agent
// refused), which updates can't express: clients then get the full state
// under a new epoch. A message inserted between existing ones is not
// supported.
func (e *EventEmitter) EmitMessages(newMessages []st.ConversationMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// cut is where the IDs stop matching: the messages from there on were
	// removed or replaced by others.
	cut := min(len(e.messages), len(newMessages))
	for i := range cut {
		if e.messages[i].Id != newMessages[i].Id {
			cut = i
			break
		}
	}
	removed := cut < len(e.messages)
	if removed && e.persister != nil {
		e.persister.TruncateMessages(e.messages[cut].Id)
	}
	seqs := make([]uint64, len(newMessages))
	copy(seqs, e.messageSeq)
	for i, msg := range newMessages {
		if i < len(e.messages) && e.messages[i] == msg {
			continue
		}
		e.seq++
		seqs[i] = e.seq
		if e.persister != nil {
			e.persister.PutMessage(msg, e.seq)
		}
		if !removed {
			e.notifyChannels(EventTypeMessageUpdate, MessageUpdateBody{
				Id: msg.Id, Role: msg.Role, Message: msg.Message, Time: msg.Time, Seq: e.seq,
			})
		}
	}
	e.messages = newMessages
	e.messageSeq = seqs
	if removed {
		e.resyncLocked()
		return
	}
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
}

// resyncLocked starts a new epoch and sends connected clients the full
// state, for a change they can't apply as an update. Clients that reconnect
// with the old epoch get the full state too. Caller holds e.mu.
func (e *EventEmitter) resyncLocked() {
	e.epoch = newEpoch()
	if e.persister != nil {
		e.persister.SetSeq(e.epoch, e.seq)
	}
	for _, event := range e.currentStateAsEvents(0, "") {
		// Status and screen didn't change, and errors would be shown again.
		if event.Type == EventTypeStatusChange || event.Type == EventTypeScreenUpdate || event.Type == EventTypeError {
			continue
		}
		e.notifyChannels(event.Type, event.Payload)
	}
}

func (e *EventEmitter) EmitStatus(newStatus st.ConversationStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newAgentStatus := convertStatus(newStatus)
	newLifecycle := LifecycleRunning
	if newStatus == st.ConversationStatusInitializing {
		newLifecycle = LifecycleStarting
	} else if newStatus == st.ConversationStatusStable {
		newLifecycle = LifecycleReady
	}
	if e.status == newAgentStatus && e.lifecycle == newLifecycle {
		return
	}

	previousStatus := e.status
	e.handoffRevision++
	if newLifecycle == LifecycleRunning && e.lifecycle == LifecycleReady {
		e.runID++
	}
	e.status = newAgentStatus
	e.lifecycle = newLifecycle
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	if e.onStatusChange != nil && previousStatus != newAgentStatus {
		e.onStatusChange(previousStatus, newAgentStatus)
	}
}

func (e *EventEmitter) SetLifecycle(lifecycle LifecycleState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lifecycle == lifecycle {
		return
	}
	e.lifecycle = lifecycle
	e.handoffRevision++
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetSessionName records the name the agent gave its session.
func (e *EventEmitter) SetSessionName(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessionName == name {
		return
	}
	e.sessionName = name
	if e.persister != nil {
		e.persister.SetSessionName(name)
	}
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

func (e *EventEmitter) StatusSnapshot() StatusChangeBody {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusChangeBody()
}

func (e *EventEmitter) statusChangeBody() StatusChangeBody {
	pending := 0
	for _, r := range e.requests {
		if r.Status == native.RequestStatusPending {
			pending++
		}
	}
	return StatusChangeBody{
		Status: e.status, Lifecycle: e.lifecycle, SessionID: e.sessionID,
		RunID: e.runID, AgentType: e.agentType, Transport: e.transport, Version: version.Version,
		TerminalPrompt: e.terminalPrompt, TerminalColumns: e.terminalColumns,
		SessionName: e.sessionName, PendingRequests: pending, Capabilities: e.capabilities,
		LoginRequired: e.loginRequired, Settings: e.settings, Usage: e.usage,
	}
}

// SetCapabilities records the optional operations the agent supports.
func (e *EventEmitter) SetCapabilities(caps native.Capabilities) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.capabilities != nil && *e.capabilities == caps {
		return
	}
	e.capabilities = &caps
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetSettings records the agent's settings.
func (e *EventEmitter) SetSettings(settings native.Settings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.settings != nil && *e.settings == settings {
		return
	}
	e.settings = &settings
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetUsage records the agent's usage of its limits.
func (e *EventEmitter) SetUsage(usage native.AgentUsage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.usage != nil && reflect.DeepEqual(*e.usage, usage) {
		return
	}
	e.usage = &usage
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetLoginRequired records that the agent waits for a login (nil: it no
// longer does).
func (e *EventEmitter) SetLoginRequired(login *native.LoginRequired) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if login == nil && e.loginRequired == nil {
		return
	}
	if login != nil {
		copied := *login
		login = &copied
	}
	e.loginRequired = login
	e.handoffRevision++
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// EmitRequest stores a structured request's latest state and broadcasts it.
// A change in the number of pending requests is also a status change.
func (e *EventEmitter) EmitRequest(state native.RequestState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.statusChangeBody().PendingRequests
	idx := slices.IndexFunc(e.requests, func(r native.RequestState) bool { return r.ID == state.ID })
	if idx < 0 && state.Status != native.RequestStatusPending {
		// A request closed after a reset belongs to the conversation that
		// was cleared (the restarted agent's requests expire after
		// DELETE /messages cleared them), not to this one.
		return
	}
	if idx < 0 {
		e.requests = append(e.requests, state)
		e.requestSeq = append(e.requestSeq, 0)
		idx = len(e.requests) - 1
	} else {
		e.requests[idx] = state
	}
	e.seq++
	e.requestSeq[idx] = e.seq
	if e.persister != nil {
		e.persister.PutRequest(state, e.seq)
		e.persister.SetSeq(e.epoch, e.seq)
	}
	e.trimRequestsLocked()
	e.handoffRevision++
	e.notifyChannels(EventTypeRequestUpdate, RequestUpdateBody{RequestState: state, Seq: e.seq})
	if e.statusChangeBody().PendingRequests != before {
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}
}

// trimRequestsLocked drops the oldest closed requests beyond the
// native.MaxClosedRequests the conversation keeps too. Caller holds e.mu.
func (e *EventEmitter) trimRequestsLocked() {
	closed := 0
	for _, r := range e.requests {
		if r.Status != native.RequestStatusPending {
			closed++
		}
	}
	for i := 0; closed > native.MaxClosedRequests && i < len(e.requests); {
		if e.requests[i].Status == native.RequestStatusPending {
			i++
			continue
		}
		if e.persister != nil {
			e.persister.DeleteRequest(e.requests[i].ID)
		}
		e.requests = slices.Delete(e.requests, i, i+1)
		e.requestSeq = slices.Delete(e.requestSeq, i, i+1)
		closed--
	}
}

// Requests returns the stored structured requests, oldest first.
func (e *EventEmitter) Requests() []native.RequestState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.requests)
}

// Request returns the stored request with the given ID.
func (e *EventEmitter) Request(id string) (native.RequestState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i := slices.IndexFunc(e.requests, func(r native.RequestState) bool { return r.ID == id }); i >= 0 {
		return e.requests[i], true
	}
	return native.RequestState{}, false
}

func (e *EventEmitter) EmitScreen(newScreen string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.screen == newScreen {
		return
	}

	e.notifyChannels(EventTypeScreenUpdate, ScreenUpdateBody{Screen: strings.TrimRight(newScreen, mf.WhiteSpaceChars)})
	e.screen = newScreen

	// Clients decide whether to offer prompt options from this rather than
	// guessing from message text, where a numbered list in an answer looks
	// just like a selection menu.
	prompt := ""
	if isTerminalQuestionScreen(e.agentType, newScreen) {
		prompt = screenTail(newScreen, terminalQuestionTailLines)
	}
	e.updateTerminalPromptLocked(prompt)
}

// terminalPromptSettle is how long a prompt must stay on screen before it is
// published. Claude Code draws its dialogs before it accepts keys, so an
// option picked the moment the prompt appeared could be ignored.
const terminalPromptSettle = time.Second

// updateTerminalPromptLocked publishes a new prompt once it has settled, and
// clears a prompt that went away or changed immediately. Caller holds e.mu.
func (e *EventEmitter) updateTerminalPromptLocked(prompt string) {
	if prompt == e.pendingPrompt && e.promptTimer != nil {
		return
	}
	if e.promptTimer != nil {
		e.promptTimer.Stop()
		e.promptTimer = nil
	}
	e.pendingPrompt = ""
	if prompt == e.terminalPrompt {
		return
	}
	// Clear a published prompt as soon as it changes: its options would
	// answer a dialog that is no longer showing.
	if e.terminalPrompt != "" {
		e.terminalPrompt = ""
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}
	if prompt == "" {
		return
	}
	e.pendingPrompt = prompt
	e.promptTimer = e.clock.AfterFunc(terminalPromptSettle, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.pendingPrompt != prompt {
			return
		}
		e.pendingPrompt = ""
		e.promptTimer = nil
		e.terminalPrompt = prompt
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}, "EventEmitter", "terminalPrompt")
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
	e.emitRichMessage(msg, false)
}

// EmitRichMessageSnapshot stores msg as the full current state of the
// message, replacing an earlier state with the same (MessageID, Role)
// instead of merging blocks into it. Native adapters send snapshots: their
// text blocks grow, and merging would keep every intermediate version.
func (e *EventEmitter) EmitRichMessageSnapshot(msg jsonlwatcher.RichMessage) {
	e.emitRichMessage(msg, true)
}

func (e *EventEmitter) emitRichMessage(msg jsonlwatcher.RichMessage, replace bool) {
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
	if idx >= 0 && replace {
		e.richMessages[idx] = msg
	} else if idx >= 0 {
		existing := e.richMessages[idx]
		existing.Content = mergeRichContent(existing.Content, msg.Content)
		existing.StopReason = msg.StopReason
		existing.Model = msg.Model
		existing.Usage = msg.Usage
		e.richMessages[idx] = existing
		msg = existing
	} else {
		e.richMessages = append(e.richMessages, msg)
		e.richSeq = append(e.richSeq, 0)
		idx = len(e.richMessages) - 1
	}
	e.seq++
	e.richSeq[idx] = e.seq
	if e.persister != nil {
		e.persister.PutRichMessage(idx, msg, e.seq)
		e.persister.SetSeq(e.epoch, e.seq)
	}
	// Always broadcast the merged snapshot. Broadcasting the incoming Claude
	// delta would make clients replace a complete message with its last block.
	e.notifyChannels(EventTypeRichMessageUpdate, RichMessageUpdateBody{RichMessage: msg, Seq: e.seq})
}

func mergeRichContent(existing, incoming []jsonlwatcher.RichContentBlock) []jsonlwatcher.RichContentBlock {
	merged := slices.Clone(existing)
	seen := make(map[string]struct{}, len(existing))
	for _, block := range existing {
		seen[richContentBlockKey(block)] = struct{}{}
	}
	for _, block := range incoming {
		key := richContentBlockKey(block)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, block)
	}
	return merged
}

func richContentBlockKey(block jsonlwatcher.RichContentBlock) string {
	if block.ToolUseID != "" {
		return block.Type + ":" + block.ToolUseID
	}
	encoded, _ := json.Marshal(block)
	return string(encoded)
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
	if e.persister != nil && len(events) > 0 {
		e.persister.AppendSessionEvents(e.sessionEvents[len(e.sessionEvents)-len(events):])
	}
}

func (e *EventEmitter) SessionEvents() []jsonlwatcher.SessionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sessionEvents)
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
	return e.SubscribeSince(0, "")
}

// SubscribeSince is Subscribe for a client that already has the state up to
// sequence since in epoch: the returned events start with a session_sync and
// hold only later changes, or the full state if the epoch doesn't match.
func (e *EventEmitter) SubscribeSince(since uint64, epoch string) (int, <-chan Event, []Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	stateEvents := e.currentStateAsEvents(since, epoch)

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
