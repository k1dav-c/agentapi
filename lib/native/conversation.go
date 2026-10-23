package native

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

var _ conv.Conversation = (*Conversation)(nil)

// Observer receives the structured state a Conversation produces beyond
// what conv.Emitter carries. Its methods are called with the conversation's
// lock held and must not call back into the conversation. A RichMessage
// is the observer's own copy, to keep and change.
type Observer interface {
	// Started reports that an agent accepts turns, and what it supports;
	// again when that changes (a new model takes images or doesn't).
	Started(Capabilities)
	// LoginRequired reports that the agent waits for the user to log in,
	// or with nil that it no longer does. It is called when that changes.
	LoginRequired(*LoginRequired)
	RichMessage(transcript.RichMessage)
	SessionEvents([]transcript.SessionEvent)
	SessionName(string)
	SessionRef(string)
	RequestUpdated(RequestState)
	// Settings reports the agent's session settings when they change.
	Settings(Settings)
	// SettingsChosen reports the settings chosen for the session when they
	// change: what to pass as Config.Settings to continue the session.
	SettingsChosen(Settings)
	// Usage reports the agent's usage of its limits when it changes.
	Usage(AgentUsage)
	// LimitStop reports the turn a usage limit stopped when that changes,
	// or with nil that a turn started since or the stop was dismissed.
	LimitStop(*LimitStop)
}

type nopObserver struct{}

func (nopObserver) Started(Capabilities)                    {}
func (nopObserver) LoginRequired(*LoginRequired)            {}
func (nopObserver) RichMessage(transcript.RichMessage)      {}
func (nopObserver) SessionEvents([]transcript.SessionEvent) {}
func (nopObserver) SessionName(string)                      {}
func (nopObserver) SessionRef(string)                       {}
func (nopObserver) RequestUpdated(RequestState)             {}
func (nopObserver) Settings(Settings)                       {}
func (nopObserver) SettingsChosen(Settings)                 {}
func (nopObserver) Usage(AgentUsage)                        {}
func (nopObserver) LimitStop(*LimitStop)                    {}

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
	Emitter         conv.Emitter
	Observer        Observer
	Logger          *slog.Logger
	Clock           quartz.Clock
	// StartTimeout bounds how long Start waits for the agent to accept
	// turns. Zero means two minutes.
	StartTimeout time.Duration
	// LoginRetryInterval is how often the agent is started again while it
	// waits for a login. Zero means five seconds.
	LoginRetryInterval time.Duration
	// Settings were chosen for the session earlier (restored after AgentAPI
	// restarted). Every agent that starts gets them.
	Settings Settings
	// AutoContinue schedules a message that continues a turn a usage limit
	// stopped for when the limit resets, if the agent says when.
	AutoContinue bool
	// LimitStop is a turn a usage limit stopped before AgentAPI restarted.
	LimitStop *LimitStop
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
	// deadline: the initial prompt, and Send's, which the server calls
	// while it holds its lock.
	sendTimeout = 30 * time.Second
)

// Conversation is an conv.Conversation backed by an Adapter.
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
	caps Capabilities
	// generation increases whenever the adapter is replaced; a sink of an
	// older generation is ignored, so a replaced adapter can't change the
	// state.
	generation int
	started    bool
	turnActive bool
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

	exited chan error
	// login is set while the agent waits for the user to log in.
	login *LoginRequired
	// loginWaiting: a start found no login, so the agent is being started
	// again until one works.
	loginWaiting bool
	// loginWake is closed to cut short the wait before the agent is started
	// again for a login: by RetryLogin, to start it now, and when the agent
	// is replaced or closed. It is nil while no wait runs.
	loginWake chan struct{}
	// startErrors holds what the agent reported while starting since it
	// last started, so that while it waits for a login, a start doesn't show
	// again what an earlier one showed.
	startErrors map[string]bool
	// settings are the agent's session settings, kept while no agent runs.
	settings Settings
	// chosen are the settings chosen through ApplySettings (or restored
	// with Config.Settings), which every agent that starts gets; a setting
	// the agent changes by itself is changed here too.
	chosen Settings
	// usage is what the agent last reported of its usage. The rate limits
	// are the account's, so they stay for the next agent; the context
	// belongs to the session.
	usage AgentUsage
	// limitStop is the turn a usage limit stopped, until a turn starts.
	limitStop *LimitStop
	// continueAgain: the turn running is a scheduled continuation, so if
	// the limit stops it again, it is scheduled again.
	continueAgain bool
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
	return &Conversation{
		cfg:        cfg,
		ctx:        context.Background(),
		messages:   newMessageLog(cfg.Clock, cfg.InitialMessages),
		requests:   newRequestBook(cfg.Clock),
		sessionRef: cfg.ResumeRef,
		exited:     make(chan error, 1),
		promptSent: len(cfg.InitialMessages) > 0,
		chosen:     cfg.Settings,
		limitStop:  cfg.LimitStop.clone(),
	}
}

// Capabilities reports what the agent supports. While no agent runs, it
// reports what the last one supported.
func (c *Conversation) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Settings reports the agent's session settings. While no agent runs, it
// reports the last ones.
func (c *Conversation) Settings() Settings {
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
func (c *Conversation) Usage() AgentUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func (c *Conversation) setUsageLocked(usage AgentUsage) {
	c.usage = usage
	c.cfg.Observer.Usage(usage)
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
func (c *Conversation) ApplySettings(ctx context.Context, s Settings) error {
	configurable, gen, err := c.configurable()
	if err != nil {
		return err
	}
	applyErr := configurable.ApplySettings(ctx, s)
	settings := configurable.Settings()
	// A new model can change what the agent supports (images).
	caps, hasCaps := Capabilities{}, false
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
	if hasCaps && caps != c.caps {
		c.caps = caps
		c.cfg.Observer.Started(caps)
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

func (c *Conversation) chooseLocked(chosen Settings) {
	if c.chosen != chosen {
		c.chosen = chosen
		c.cfg.Observer.SettingsChosen(chosen)
	}
}

// setSettingsLocked records the agent's settings.
func (c *Conversation) setSettingsLocked(settings Settings) {
	if c.settings != settings {
		c.settings = settings
		c.cfg.Observer.Settings(settings)
	}
}

// agentSettingsLocked records settings the agent changed by itself. A
// chosen setting follows the agent, so the next agent isn't put back.
func (c *Conversation) agentSettingsLocked(settings Settings) {
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
	return c.started && !c.turnActive && !c.compacting
}

// TurnActive reports whether the agent is working on a turn.
func (c *Conversation) TurnActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive
}

// Messages implements conv.Conversation.
func (c *Conversation) Messages() []conv.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.messages.snapshot()
}

// Status implements conv.Conversation. A turn waiting only on the user's
// answer to a request counts as stable, like a terminal dialog.
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

// Reset implements conv.Conversation by clearing the messages. Use Restart to
// also replace the agent.
func (c *Conversation) Reset() {
	c.mu.Lock()
	c.messages.reset()
	c.mu.Unlock()
}

// Send implements conv.Conversation by sending the parts' text as a turn.
func (c *Conversation) Send(parts ...conv.MessagePart) error {
	var sb strings.Builder
	for _, part := range parts {
		sb.WriteString(part.String())
	}
	c.mu.Lock()
	runCtx := c.ctx
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(runCtx, sendTimeout)
	defer cancel()
	return c.StartTurn(ctx, InputFromText(sb.String()))
}

// StartTurn sends a message as a new turn. It returns
// conv.ErrMessageValidationChanging while a turn is running, the agent
// compacts its context or is still starting, so callers can queue the
// message.
func (c *Conversation) StartTurn(ctx context.Context, input Input) error {
	c.mu.Lock()
	turn, err := c.beginTurnLocked(input)
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
}

// beginTurnLocked begins a turn with input as its user message, if the
// agent can take one now.
func (c *Conversation) beginTurnLocked(input Input) (pendingTurn, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return pendingTurn{}, conv.ErrMessageValidationEmpty
	}
	if !c.started || c.turnActive || c.compacting || c.adapter == nil {
		return pendingTurn{}, conv.ErrMessageValidationChanging
	}
	c.turnActive = true
	c.setLimitStopLocked(nil)
	turn := pendingTurn{
		adapter: c.adapter,
		gen:     c.generation,
		input:   c.inputForAgentLocked(input),
		msg:     c.messages.appendUser(text),
	}
	c.cfg.Emitter.EmitMessages(c.messages.snapshot())
	c.cfg.Emitter.EmitStatus(conv.StatusChanging)
	return turn, nil
}

// deliverTurn sends a turn that began to its adapter. If that fails, the
// turn is taken back.
func (c *Conversation) deliverTurn(ctx context.Context, turn pendingTurn) error {
	err := turn.adapter.StartTurn(ctx, turn.input)
	if err == nil {
		return nil
	}
	c.mu.Lock()
	if turn.gen == c.generation {
		c.turnActive = false
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
	adapter, caps, active := c.adapter, c.caps, c.turnActive
	c.mu.Unlock()
	switch {
	case adapter == nil:
		return ErrNotStarted
	case !caps.Interrupt:
		return ErrUnsupported
	case !active:
		return ErrNoActiveTurn
	}
	return adapter.Interrupt(ctx)
}
