package native

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

// Observer receives the structured state a Conversation produces beyond
// what conv.Emitter carries. Its methods are called with the conversation's
// lock held and must not call back into the conversation. A RichMessage
// and a ConversationState are the observer's own copies, to keep and
// change.
type Observer interface {
	// StateChanged reports the conversation's state whenever a part of it
	// changed: capabilities, login, settings (the agent's and the chosen
	// ones), usage, the limit stop or retry wait that holds the queue. It
	// comes where each change is made, in the order of the conversation's
	// other reports; changes made together come as one.
	StateChanged(agentstate.ConversationState)
	// Started reports that an agent accepts turns: one started, or the
	// conversation started it again by itself after it exited. What it
	// supports came before with StateChanged.
	Started()
	RichMessage(transcript.RichMessage)
	SessionEvents([]transcript.SessionEvent)
	SessionName(string)
	// SessionRef reports the session the next agent resumes: the one an
	// agent opened, or the one a switch opens next. It is reported each
	// time, also when it is the same.
	SessionRef(string)
	RequestUpdated(agentstate.RequestState)
	// AgentExited reports that the agent exited on its own or failed to
	// start, and whether the conversation starts it again. An agent that
	// lost its login before it exited waits for the login instead
	// (ConversationState.LoginRequired).
	AgentExited(AgentExit)
}

type nopObserver struct{}

func (nopObserver) StateChanged(agentstate.ConversationState) {}
func (nopObserver) Started()                                  {}
func (nopObserver) RichMessage(transcript.RichMessage)        {}
func (nopObserver) SessionEvents([]transcript.SessionEvent)   {}
func (nopObserver) SessionName(string)                        {}
func (nopObserver) SessionRef(string)                         {}
func (nopObserver) RequestUpdated(agentstate.RequestState)    {}
func (nopObserver) AgentExited(AgentExit)                     {}

// Config configures a Conversation.
type Config struct {
	// Factory creates the adapter, again on every restart.
	Factory Factory
	// ResumeRef resumes an earlier session on the first start.
	ResumeRef string
	// InitialMessages continues an earlier conversation (restored after
	// AgentAPI restarted). The initial prompt is not sent again.
	InitialMessages []conv.Message
	InitialPrompt   string
	// InitialPromptSent, if set, is called once the initial prompt's turn
	// began, under the conversation's lock.
	InitialPromptSent func()
	Emitter           conv.Emitter
	Observer          Observer
	Logger            *slog.Logger
	Clock             quartz.Clock
	// StartTimeout bounds how long Start waits for the agent to accept
	// turns. Zero means two minutes.
	StartTimeout time.Duration
	// LoginRetryInterval is how often the agent is started again while it
	// waits for a login. Zero means five seconds.
	LoginRetryInterval time.Duration
	// State is the conversation's state before AgentAPI restarted: every
	// agent that starts gets the settings chosen, and a limit stop or retry
	// wait holds the queue again. With retries off (Policy.RetriesOff) a
	// retry wait is dropped.
	State agentstate.StoredState
	// Policy says what the conversation does by itself when a turn or the
	// agent stops.
	Policy
	// PrepareRichMessage, if set, changes every rich message an adapter
	// reports before the conversation takes it, without the conversation's
	// lock: work that can be slow (storing the images of a tool result)
	// holds up nothing else. It gets the conversation's own copy.
	PrepareRichMessage func(*transcript.RichMessage)
}

const (
	defaultStartTimeout       = 2 * time.Minute
	defaultLoginRetryInterval = 5 * time.Second
	// sendTimeout bounds the delivery of a message no caller gives a
	// deadline (the initial prompt).
	sendTimeout = 30 * time.Second
	// restartReset is how long an agent runs before its exit counts as the
	// first of the restart delays again.
	restartReset = 5 * time.Minute
)

// Conversation is the conversation with an agent, through its Adapter.
type Conversation struct {
	cfg Config

	// mu guards the fields below. It is never held while the adapter is
	// called (see Sink).
	mu sync.Mutex
	// ctx is the context Start got; the agent runs in it.
	ctx     context.Context
	adapter Adapter
	// caps is what the last agent that started supports. It is kept while
	// no agent runs (a restart, a wait for a login).
	caps agentstate.Capabilities
	// generation increases whenever the adapter is replaced; a sink of an
	// older generation is ignored, so a replaced adapter can't change the
	// state.
	generation int
	started    bool
	turnActive bool
	// unconfirmedTurn: the turn's message went out to the agent, which
	// didn't confirm it (see deliverTurn). The agent's TurnStarted and
	// TurnCompleted clear it.
	unconfirmedTurn bool
	// compacting is set while the agent compacts its context on request,
	// which no turn may interrupt.
	compacting bool
	// promptSent is set once the initial prompt was sent as a turn, or when
	// the conversation continues an earlier one, which had it. Restart
	// clears it, so the new session gets the prompt too.
	promptSent bool
	sessionRef string
	// forking: sessionRef is the session to fork, until the agent reports
	// the copy's.
	forking bool

	messages messageLog
	requests requestBook

	// login is set while the agent waits for the user to log in.
	login *agentstate.LoginRequired
	// loginWaiting: a start found no login, so the agent is being started
	// again until one works.
	loginWaiting bool
	// relaunchWake is closed to cut short the wait before the agent is
	// started again, for a login or after an exit: by RetryLogin, to start
	// it now, and when the agent is replaced or closed. It is nil while no
	// wait runs.
	relaunchWake chan struct{}
	// restarts counts the starts the conversation made after exits since
	// an agent last ran restartReset.
	restarts int
	// ran: an agent started once, so caps is what the agent supports.
	ran bool
	// startErrors holds what the agent reported while starting since it
	// last started, so that while it waits for a login, a start doesn't show
	// again what an earlier one showed.
	startErrors map[string]bool
	// settings are the agent's session settings, kept while no agent runs.
	settings agentstate.Settings
	// chosen are the settings chosen through ApplySettings (or restored
	// with Config.Settings), which every agent that starts gets; a setting
	// the agent changes by itself is changed here too.
	chosen agentstate.Settings
	// usage is what the agent last reported of its usage. The rate limits
	// are the account's, so they stay for the next agent; the context
	// belongs to the session. usageReported: the agent reported it.
	usage         agentstate.AgentUsage
	usageReported bool
	// hold is what holds the queue (see hold.go): a turn a usage limit
	// stopped or a failed turn that waits to be tried again, until a turn
	// starts.
	hold hold
	// continues is the hold the running turn continues (ContinueIfDue,
	// RetryNow): if the turn stops the same way, the continuation is
	// scheduled again, or the next retry. It is empty for any other turn,
	// and once the turn ended or its agent is gone.
	continues hold
	// reported is the state the observer was told last (see
	// stateChangedLocked).
	reported agentstate.ConversationState
}

// NewConversation creates a conversation. Nothing runs until Start.
func NewConversation(cfg Config) *Conversation {
	if cfg.Emitter == nil {
		cfg.Emitter = conv.NopEmitter{}
	}
	if cfg.Observer == nil {
		cfg.Observer = nopObserver{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = defaultStartTimeout
	}
	if cfg.LoginRetryInterval == 0 {
		cfg.LoginRetryInterval = defaultLoginRetryInterval
	}
	if cfg.RetriesOff() {
		cfg.State.RetryWait = nil
	}
	cfg.Policy = cfg.withDefaults()
	return &Conversation{
		cfg:        cfg,
		ctx:        context.Background(),
		messages:   newMessageLog(cfg.Clock, cfg.InitialMessages),
		requests:   newRequestBook(cfg.Clock),
		sessionRef: cfg.ResumeRef,
		promptSent: len(cfg.InitialMessages) > 0,
		chosen:     cfg.State.Settings,
		hold:       restoredHold(cfg.State.LimitStop, cfg.State.RetryWait),
	}
}

// Capabilities reports what the agent supports. While no agent runs, it
// reports what the last one supported.
func (c *Conversation) Capabilities() agentstate.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Settings reports the agent's session settings. While no agent runs, it
// reports the last ones.
func (c *Conversation) Settings() agentstate.Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

// Compact has the agent compact its context, while it isn't working on a
// turn, and returns ErrTurnActive while it is (or already compacts). No
// turn starts until it is done: StartTurn returns
// conv.ErrMessageValidationChanging, so a message waits in the queue.
func (c *Conversation) Compact(ctx context.Context) error {
	c.mu.Lock()
	compacter, ok := c.adapter.(Compacter)
	switch {
	case !c.started || c.adapter == nil:
		c.mu.Unlock()
		return ErrNotStarted
	case !ok || !c.caps.Compact:
		c.mu.Unlock()
		return ErrUnsupported
	case c.turnActive || c.compacting:
		c.mu.Unlock()
		return ErrTurnActive
	}
	// No turn starts meanwhile: it is held like one, without the lock.
	c.compacting = true
	gen := c.generation
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()

	err := compacter.Compact(ctx)
	c.mu.Lock()
	if gen == c.generation {
		c.compacting = false
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	}
	c.mu.Unlock()
	return err
}

// Usage reports the agent's usage of its limits, as it last reported it.
func (c *Conversation) Usage() agentstate.AgentUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func (c *Conversation) setUsageLocked(usage agentstate.AgentUsage) {
	c.usage = usage
	c.usageReported = true
	c.stateChangedLocked()
}

// stateLocked is the conversation's state, the observer's own copy.
func (c *Conversation) stateLocked() agentstate.ConversationState {
	state := agentstate.ConversationState{
		LoginRequired: c.login,
		Settings:      c.settings,
		Stored:        agentstate.StoredState{Settings: c.chosen, LimitStop: c.hold.limit, RetryWait: c.hold.retry},
	}
	if c.ran {
		state.Capabilities = &c.caps
	}
	if c.usageReported {
		state.Usage = &c.usage
	}
	return state.Clone()
}

// stateChangedLocked tells the observer the conversation's state, if it
// changed since it was told last. Everything that changes a part of it
// calls it; what changes several parts at once calls it once, after.
func (c *Conversation) stateChangedLocked() {
	state := c.stateLocked()
	if reflect.DeepEqual(state, c.reported) {
		return
	}
	c.reported = state
	c.cfg.Observer.StateChanged(state.Clone())
}

// configurable returns the running agent if its settings can change.
func (c *Conversation) configurable() (Configurable, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started || c.adapter == nil {
		return nil, 0, ErrNotStarted
	}
	configurable, ok := c.adapter.(Configurable)
	if !ok || !c.caps.Settings {
		return nil, 0, ErrUnsupported
	}
	return configurable, c.generation, nil
}

// SettingOptions reports the values the settings can take.
func (c *Conversation) SettingOptions(ctx context.Context) (SettingOptions, error) {
	configurable, gen, err := c.configurable()
	if err != nil {
		return SettingOptions{}, err
	}
	options, err := configurable.SettingOptions(ctx)
	if err != nil {
		return SettingOptions{}, err
	}
	// What the agent learned with them can complete its settings, e.g. the
	// effort a model uses by default.
	settings := configurable.Settings()
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen == c.generation {
		c.setSettingsLocked(settings)
	}
	return options, nil
}

// ApplySettings changes the settings that are set in s. The agents that
// replace this one (a restart, a new login) get them too.
func (c *Conversation) ApplySettings(ctx context.Context, s agentstate.Settings) error {
	configurable, gen, err := c.configurable()
	if err != nil {
		return err
	}
	applyErr := configurable.ApplySettings(ctx, s)
	settings := configurable.Settings()
	// A new model can change what the agent supports (images).
	caps, hasCaps := agentstate.Capabilities{}, false
	if adapter, ok := configurable.(Adapter); ok {
		caps, hasCaps = adapter.Capabilities(), true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation {
		if applyErr != nil {
			return applyErr
		}
		return ErrNotStarted
	}
	if hasCaps {
		c.caps = caps
	}
	// Settings the agent took before one failed show too.
	c.setSettingsLocked(settings)
	if applyErr != nil {
		return applyErr
	}
	chosen := c.chosen.Merge(s)
	if s.Model != "" && s.Effort == "" && chosen.Effort != "" {
		// A model without the effort chosen takes another one, which the
		// next agent gets too.
		chosen.Effort = settings.Effort
	}
	c.chooseLocked(chosen)
	return nil
}

func (c *Conversation) chooseLocked(chosen agentstate.Settings) {
	c.chosen = chosen
	c.stateChangedLocked()
}

// setSettingsLocked records the agent's settings.
func (c *Conversation) setSettingsLocked(settings agentstate.Settings) {
	c.settings = settings
	c.stateChangedLocked()
}

// agentSettingsLocked records settings the agent changed by itself. A
// chosen setting follows the agent, so the next agent isn't put back.
func (c *Conversation) agentSettingsLocked(settings agentstate.Settings) {
	if c.started {
		// An empty mode is one of the agent's own (Claude Code's
		// acceptEdits): the next agent gets the mode chosen before rather
		// than its default, which may let it do more. An empty model or
		// effort is one the agent didn't report.
		chosen := c.chosen
		if chosen.PermissionMode != "" && settings.PermissionMode != "" {
			chosen.PermissionMode = settings.PermissionMode
		}
		if chosen.Model != "" && settings.Model != "" {
			chosen.Model = settings.Model
		}
		if chosen.Effort != "" && settings.Effort != "" {
			chosen.Effort = settings.Effort
		}
		c.chooseLocked(chosen)
	}
	c.setSettingsLocked(settings)
}

// resumeLocked says which session the next agent opens.
func (c *Conversation) resumeLocked() Resume {
	return Resume{Ref: c.sessionRef, Fork: c.forking}
}

// SessionRef is the identifier that resumes the agent's session.
func (c *Conversation) SessionRef() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionRef
}

// Idle reports whether a new turn can start now.
func (c *Conversation) Idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readyLocked()
}

func (c *Conversation) readyLocked() bool {
	return c.started && c.adapter != nil && !c.turnActive && !c.compacting
}

// Busy reports whether the agent has work a restart would cut short: a
// turn, a compaction, a request that waits for an answer. An agent that
// isn't running has none.
func (c *Conversation) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive || c.compacting || c.requests.waitingOnUser()
}

// TurnActive reports whether the agent is working on a turn.
func (c *Conversation) TurnActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive
}

// Messages returns the conversation's messages.
func (c *Conversation) Messages() []conv.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.messages.snapshot()
}

// Status reports whether the agent works. A turn waiting only on the
// user's answer to a request counts as stable.
func (c *Conversation) Status() conv.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Conversation) statusLocked() conv.Status {
	if !c.started {
		return conv.StatusInitializing
	}
	if c.compacting {
		return conv.StatusChanging
	}
	if !c.turnActive || c.requests.waitingOnUser() {
		return conv.StatusStable
	}
	return conv.StatusChanging
}

// StartTurn sends a message as a new turn. It returns
// conv.ErrMessageValidationChanging while a turn is running, the agent
// compacts its context or is still starting, so callers can queue the
// message.
func (c *Conversation) StartTurn(ctx context.Context, input Input) error {
	c.mu.Lock()
	turn, err := c.beginTurnLocked(input, hold{})
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return c.deliverTurn(ctx, turn)
}

// pendingTurn is a turn that began here and has yet to reach the adapter.
type pendingTurn struct {
	adapter Adapter
	gen     int
	input   Input
	// msg is the Id of the turn's user message.
	msg int
	// restore is the hold the turn ended, which a turn taken back gives
	// back.
	restore hold
}

// beginTurnLocked begins a turn with input as its user message, if the
// agent can take one now; it continues the hold continues, if that isn't
// empty (see Conversation.continues). The turn ends the hold.
func (c *Conversation) beginTurnLocked(input Input, continues hold) (pendingTurn, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return pendingTurn{}, conv.ErrMessageValidationEmpty
	}
	if !c.started || c.turnActive || c.compacting || c.adapter == nil {
		return pendingTurn{}, conv.ErrMessageValidationChanging
	}
	c.turnActive = true
	c.unconfirmedTurn = false
	c.continues = continues
	turn := pendingTurn{
		adapter: c.adapter,
		gen:     c.generation,
		input:   c.inputForAgentLocked(input),
		msg:     c.messages.appendUser(text),
		restore: c.hold,
	}
	c.setHoldLocked(hold{})
	c.cfg.Emitter.EmitMessages(c.messages.snapshot())
	c.cfg.Emitter.EmitStatus(conv.StatusChanging)
	return turn, nil
}

// deliverTurn sends a turn that began to its adapter. If that fails, the
// turn is taken back, with the hold it ended, unless the message went out
// unconfirmed: the agent
// may be working on it, so the message stays and the turn stays active
// until the agent's events end it (Interrupt ends it if the agent never
// starts it).
func (c *Conversation) deliverTurn(ctx context.Context, turn pendingTurn) error {
	err := turn.adapter.StartTurn(ctx, turn.input)
	if err == nil {
		return nil
	}
	c.mu.Lock()
	switch {
	case turn.gen != c.generation:
	case errors.Is(err, ErrUnconfirmed):
		// Unless the agent ended the turn meanwhile.
		c.unconfirmedTurn = c.turnActive
	default:
		c.turnActive = false
		c.continues = hold{}
		if c.hold.kind() == HoldNone {
			// Unless the agent ended the turn meanwhile with one of its
			// own.
			c.setHoldLocked(turn.restore)
		}
		c.messages.remove(turn.msg)
		c.cfg.Emitter.EmitMessages(c.messages.snapshot())
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	}
	c.mu.Unlock()
	if errors.Is(err, ErrTurnActive) {
		return conv.ErrMessageValidationChanging
	}
	return err
}

// inputForAgentLocked drops the images from input unless the agent takes
// images; the text still mentions their paths.
func (c *Conversation) inputForAgentLocked(input Input) Input {
	if !c.caps.Images {
		input.Images = nil
	}
	return input
}

// Steer adds a message to the running turn.
func (c *Conversation) Steer(ctx context.Context, input Input) error {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return conv.ErrMessageValidationEmpty
	}
	c.mu.Lock()
	adapter, gen := c.adapter, c.generation
	var err error
	switch {
	case adapter == nil || !c.started:
		err = ErrNotStarted
	case !c.caps.Steer:
		err = ErrUnsupported
	case !c.turnActive:
		err = ErrNoActiveTurn
	}
	input = c.inputForAgentLocked(input)
	c.mu.Unlock()
	if err != nil {
		return err
	}

	if err := adapter.Steer(ctx, input); err != nil {
		return err
	}
	// The message joins the transcript once the agent took it: what the
	// agent says while it is on its way still belongs before it, and a
	// failed delivery leaves nothing to take back.
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen == c.generation {
		c.messages.appendUser(text)
		c.cfg.Emitter.EmitMessages(c.messages.snapshot())
	}
	return nil
}

// Interrupt stops the running turn.
func (c *Conversation) Interrupt(ctx context.Context) error {
	c.mu.Lock()
	adapter, caps, active, gen := c.adapter, c.caps, c.turnActive, c.generation
	c.mu.Unlock()
	switch {
	case adapter == nil:
		return ErrNotStarted
	case !caps.Interrupt:
		return ErrUnsupported
	case !active:
		return ErrNoActiveTurn
	}
	err := adapter.Interrupt(ctx)
	if errors.Is(err, ErrNoActiveTurn) && c.endUnconfirmedTurn(gen) {
		return nil
	}
	return err
}

// endUnconfirmedTurn ends the turn of a message the agent didn't confirm
// (see deliverTurn), if the agent of generation gen has no turn: nothing
// else would end it. It reports whether it did.
func (c *Conversation) endUnconfirmedTurn(gen int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.generation || !c.unconfirmedTurn {
		return false
	}
	c.cfg.Logger.Warn("The agent never started the turn of a message it didn't confirm; ending it here")
	c.turnCompletedLocked(TurnResult{StopReason: StopReasonInterrupted})
	return true
}
