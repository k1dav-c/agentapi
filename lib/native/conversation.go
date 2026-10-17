package native

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	st "github.com/coder/agentapi/lib/screentracker"
)

var _ st.Conversation = (*Conversation)(nil)

// Observer receives the structured state a Conversation produces beyond
// what st.Emitter carries. Its methods are called with the conversation's
// lock held and must not call back into the conversation.
type Observer interface {
	// Started reports that an agent accepts turns, and what it supports.
	Started(Capabilities)
	// LoginRequired reports that the agent waits for the user to log in,
	// or with nil that it no longer does. It is called when that changes.
	LoginRequired(*LoginRequired)
	RichMessage(jsonlwatcher.RichMessage)
	SessionEvents([]jsonlwatcher.SessionEvent)
	SessionName(string)
	SessionRef(string)
	RequestUpdated(RequestState)
}

type nopObserver struct{}

func (nopObserver) Started(Capabilities)                      {}
func (nopObserver) LoginRequired(*LoginRequired)              {}
func (nopObserver) RichMessage(jsonlwatcher.RichMessage)      {}
func (nopObserver) SessionEvents([]jsonlwatcher.SessionEvent) {}
func (nopObserver) SessionName(string)                        {}
func (nopObserver) SessionRef(string)                         {}
func (nopObserver) RequestUpdated(RequestState)               {}

// Config configures a Conversation.
type Config struct {
	// Factory creates the adapter, again on every restart.
	Factory Factory
	// ResumeRef resumes an earlier session on the first start.
	ResumeRef string
	// InitialMessages continues an earlier conversation (restored after
	// AgentAPI restarted). The initial prompt is not sent again.
	InitialMessages []st.ConversationMessage
	InitialPrompt   string
	Emitter         st.Emitter
	Observer        Observer
	Logger          *slog.Logger
	Clock           quartz.Clock
	// StartTimeout bounds how long Start waits for the agent to accept
	// turns. Zero means two minutes.
	StartTimeout time.Duration
	// LoginRetryInterval is how often the agent is started again while it
	// waits for a login. Zero means five seconds.
	LoginRetryInterval time.Duration
}

const (
	defaultStartTimeout       = 2 * time.Minute
	defaultLoginRetryInterval = 5 * time.Second
	// sendTimeout bounds the delivery of a message no caller gives a
	// deadline: the initial prompt, and Send's, which the server calls
	// while it holds its lock.
	sendTimeout = 30 * time.Second
)

// Conversation is an st.Conversation backed by an Adapter.
type Conversation struct {
	cfg Config

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
	// promptSent is set once the initial prompt was sent as a turn, or when
	// the conversation continues an earlier one, which had it. Restart
	// clears it, so the new session gets the prompt too.
	promptSent bool
	sessionRef string

	messages []st.ConversationMessage
	nextID   int
	// agent is the agent message being built, or nil until the agent says
	// something after the last user message.
	agent *agentText
	// rich maps the assistant rich messages to their part of an agent
	// message. A rich message keeps its agent message even if it is still
	// updated after a later user message (a steer), so its text isn't
	// repeated in the next agent message.
	rich map[string]*richPart

	requests []*RequestState
	// responding holds the requests whose answer is being delivered, so a
	// second answer can't be sent at the same time. A close that comes
	// meanwhile is kept there and applies if the delivery fails.
	responding map[string]requestClose

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
}

// NewConversation creates a conversation. Nothing runs until Start.
func NewConversation(cfg Config) *Conversation {
	if cfg.Emitter == nil {
		cfg.Emitter = st.NopEmitter{}
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
	c := &Conversation{
		cfg:        cfg,
		ctx:        context.Background(),
		rich:       make(map[string]*richPart),
		responding: make(map[string]requestClose),
		sessionRef: cfg.ResumeRef,
		exited:     make(chan error, 1),
		messages:   slices.Clone(cfg.InitialMessages),
		promptSent: len(cfg.InitialMessages) > 0,
	}
	for _, m := range c.messages {
		c.nextID = max(c.nextID, m.Id+1)
	}
	return c
}

// Capabilities reports what the agent supports. While no agent runs, it
// reports what the last one supported.
func (c *Conversation) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
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
	return c.started && !c.turnActive
}

// TurnActive reports whether the agent is working on a turn.
func (c *Conversation) TurnActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive
}

// Messages implements st.Conversation.
func (c *Conversation) Messages() []st.ConversationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.messages)
}

// Status implements st.Conversation. A turn waiting only on the user's
// answer to a request counts as stable, like a terminal dialog.
func (c *Conversation) Status() st.ConversationStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Conversation) statusLocked() st.ConversationStatus {
	if !c.started {
		return st.ConversationStatusInitializing
	}
	if !c.turnActive {
		return st.ConversationStatusStable
	}
	for _, r := range c.requests {
		if _, answering := c.responding[r.ID]; r.Status == RequestStatusPending && !answering {
			return st.ConversationStatusStable
		}
	}
	return st.ConversationStatusChanging
}

// Text implements st.Conversation. There is no terminal screen.
func (c *Conversation) Text() string { return "" }

// SaveState implements st.Conversation. State persistence for native
// sessions is handled outside the conversation.
func (c *Conversation) SaveState() error { return nil }

// Reset implements st.Conversation by clearing the messages. Use Restart to
// also replace the agent.
func (c *Conversation) Reset() {
	c.mu.Lock()
	c.resetMessagesLocked()
	c.mu.Unlock()
}

// Send implements st.Conversation by sending the parts' text as a turn.
func (c *Conversation) Send(parts ...st.MessagePart) error {
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
// st.ErrMessageValidationChanging while a turn is running or the agent is
// still starting, so callers can queue the message.
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
		return pendingTurn{}, st.ErrMessageValidationEmpty
	}
	if !c.started || c.turnActive || c.adapter == nil {
		return pendingTurn{}, st.ErrMessageValidationChanging
	}
	c.turnActive = true
	turn := pendingTurn{
		adapter: c.adapter,
		gen:     c.generation,
		input:   c.inputForAgentLocked(input),
		msg:     c.appendUserMessageLocked(text),
	}
	c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
	c.cfg.Emitter.EmitStatus(st.ConversationStatusChanging)
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
		c.removeMessageLocked(turn.msg)
		c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	}
	c.mu.Unlock()
	if errors.Is(err, ErrTurnActive) {
		return st.ErrMessageValidationChanging
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
		return st.ErrMessageValidationEmpty
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
		c.appendUserMessageLocked(text)
		c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
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
