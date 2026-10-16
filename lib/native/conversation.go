package native

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"
	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/util"
)

var _ st.Conversation = (*Conversation)(nil)

// RequestStatus is where a request is in its life.
type RequestStatus string

const (
	RequestStatusPending  RequestStatus = "pending"
	RequestStatusResolved RequestStatus = "resolved"
	// RequestStatusWithdrawn: the agent stopped waiting before it was
	// answered here.
	RequestStatusWithdrawn RequestStatus = "withdrawn"
	// RequestStatusExpired: the turn ended or the agent was restarted while
	// the request was open.
	RequestStatusExpired RequestStatus = "expired"
)

var RequestStatusValues = []RequestStatus{RequestStatusPending, RequestStatusResolved, RequestStatusWithdrawn, RequestStatusExpired}

func (s RequestStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "RequestStatus", RequestStatusValues)
}

// RequestState is a request together with what became of it.
type RequestState struct {
	Request
	Status   RequestStatus `json:"status" doc:"pending, resolved, withdrawn (the agent stopped waiting) or expired (the turn ended or the agent restarted)."`
	Response *Response     `json:"response,omitempty" doc:"The answer, once resolved."`
	ClosedAt *time.Time    `json:"closed_at,omitempty" doc:"When the request stopped being pending."`
}

// Observer receives the structured state a Conversation produces beyond
// what st.Emitter carries. Its methods are called with the conversation's
// lock held and must not call back into the conversation.
type Observer interface {
	// Started reports that an agent accepts turns, and what it supports.
	Started(Capabilities)
	RichMessage(jsonlwatcher.RichMessage)
	SessionEvents([]jsonlwatcher.SessionEvent)
	SessionName(string)
	SessionRef(string)
	RequestUpdated(RequestState)
}

type nopObserver struct{}

func (nopObserver) Started(Capabilities)                      {}
func (nopObserver) RichMessage(jsonlwatcher.RichMessage)      {}
func (nopObserver) SessionEvents([]jsonlwatcher.SessionEvent) {}
func (nopObserver) SessionName(string)                        {}
func (nopObserver) SessionRef(string)                         {}
func (nopObserver) RequestUpdated(RequestState)               {}

type nopEmitter struct{}

func (nopEmitter) EmitMessages([]st.ConversationMessage) {}
func (nopEmitter) EmitStatus(st.ConversationStatus)      {}
func (nopEmitter) EmitScreen(string)                     {}
func (nopEmitter) EmitError(string, st.ErrorLevel)       {}

// Config configures a Conversation.
type Config struct {
	// Factory creates the adapter, again on every restart.
	Factory Factory
	// ResumeRef resumes an earlier session on the first start.
	ResumeRef     string
	InitialPrompt string
	Emitter       st.Emitter
	Observer      Observer
	Logger        *slog.Logger
	Clock         quartz.Clock
	// StartTimeout bounds how long Start waits for the agent to accept
	// turns. Zero means two minutes.
	StartTimeout time.Duration
}

// maxClosedRequests is how many answered or expired requests are kept for
// clients that reconnect after a request was closed.
const maxClosedRequests = 50

// Conversation is an st.Conversation backed by an Adapter.
type Conversation struct {
	cfg Config

	mu      sync.Mutex
	ctx     context.Context
	adapter Adapter
	// generation increases on every restart; a sink of an older generation
	// is ignored, so a replaced adapter can't change the state.
	generation int
	started    bool
	starting   bool
	turnActive bool
	// promptSent is set once the initial prompt has been delivered.
	promptSent bool
	sessionRef string

	messages []st.ConversationMessage
	nextID   int
	// agentIdx is the index in messages of the agent message being built,
	// or -1 until the agent says something after the last user message.
	agentIdx int
	// segment lists the rich messages whose text makes up the agent message
	// at agentIdx, in the order they appeared; texts holds their text.
	segment []string
	texts   map[string]string

	requests []*RequestState
	// responding marks requests whose answer is being delivered, so a second
	// answer can't be sent at the same time.
	responding map[string]bool

	exited chan error
}

// NewConversation creates a conversation. Nothing runs until Start.
func NewConversation(cfg Config) *Conversation {
	if cfg.Emitter == nil {
		cfg.Emitter = nopEmitter{}
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
		cfg.StartTimeout = 2 * time.Minute
	}
	return &Conversation{
		cfg:        cfg,
		agentIdx:   -1,
		texts:      make(map[string]string),
		responding: make(map[string]bool),
		sessionRef: cfg.ResumeRef,
		exited:     make(chan error, 1),
	}
}

// Start launches the agent in the background. Status reports initializing
// until the agent accepts turns.
func (c *Conversation) Start(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	gen := c.generation
	ref := c.sessionRef
	c.starting = true
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	go func() {
		if err := c.launch(ctx, gen, ref); err != nil {
			c.cfg.Logger.Error("Failed to start the agent", "error", err)
			c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to start the agent: %v", err), st.ErrorLevelError)
			c.signalExit(gen, err)
		}
	}()
}

// launch creates and starts an adapter for generation gen.
func (c *Conversation) launch(ctx context.Context, gen int, resumeRef string) error {
	adapter, err := c.cfg.Factory(resumeRef)
	if err != nil {
		return fmt.Errorf("create adapter: %w", err)
	}
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		_ = adapter.Close()
		return nil
	}
	c.adapter = adapter
	c.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, c.cfg.StartTimeout)
	defer cancel()
	if err := adapter.Start(startCtx, &sink{c: c, gen: gen}); err != nil {
		_ = adapter.Close()
		return err
	}
	go func() {
		err := adapter.Wait()
		c.signalExit(gen, err)
	}()

	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return nil
	}
	c.started = true
	c.starting = false
	sendPrompt := c.cfg.InitialPrompt != "" && !c.promptSent
	c.promptSent = true
	c.cfg.Observer.Started(adapter.Capabilities())
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()

	if sendPrompt {
		if err := c.StartTurn(ctx, InputFromText(c.cfg.InitialPrompt)); err != nil {
			c.cfg.Emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), st.ErrorLevelError)
		}
	}
	return nil
}

// signalExit reports that the adapter of generation gen exited. Exits of
// replaced adapters are expected and ignored.
func (c *Conversation) signalExit(gen int, err error) {
	c.mu.Lock()
	if gen != c.generation {
		c.mu.Unlock()
		return
	}
	c.started = false
	c.starting = false
	c.turnActive = false
	for _, state := range c.expireRequestsLocked(ResponseNotResumable) {
		c.cfg.Observer.RequestUpdated(state)
	}
	c.mu.Unlock()
	select {
	case c.exited <- err:
	default:
	}
}

// Exited delivers the error (nil for a clean exit) when the agent exits on
// its own. Exits caused by Restart or Close are not delivered.
func (c *Conversation) Exited() <-chan error { return c.exited }

// Restart replaces the agent with a new one in a new session and clears the
// conversation. It returns once the new agent accepts turns.
func (c *Conversation) Restart(ctx context.Context) error {
	c.mu.Lock()
	c.generation++
	gen := c.generation
	old := c.adapter
	c.adapter = nil
	c.started = false
	c.starting = true
	c.turnActive = false
	c.sessionRef = ""
	changed := c.expireRequestsLocked(ResponseNotResumable)
	c.resetMessagesLocked()
	runCtx := c.ctx
	for _, state := range changed {
		c.cfg.Observer.RequestUpdated(state)
	}
	c.cfg.Emitter.EmitMessages(nil)
	c.cfg.Emitter.EmitStatus(st.ConversationStatusInitializing)
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if runCtx == nil {
		runCtx = context.Background()
	}
	errCh := make(chan error, 1)
	go func() { errCh <- c.launch(runCtx, gen, "") }()
	select {
	case err := <-errCh:
		if err != nil {
			c.signalExit(gen, err)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the agent. The exit is not reported on Exited.
func (c *Conversation) Close() error {
	c.mu.Lock()
	c.generation++
	adapter := c.adapter
	c.adapter = nil
	c.started = false
	c.mu.Unlock()
	if adapter == nil {
		return nil
	}
	return adapter.Close()
}

// Capabilities reports what the current adapter supports.
func (c *Conversation) Capabilities() Capabilities {
	c.mu.Lock()
	adapter := c.adapter
	c.mu.Unlock()
	if adapter == nil {
		return Capabilities{}
	}
	return adapter.Capabilities()
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

// Send implements st.Conversation by sending the parts' text as a turn.
func (c *Conversation) Send(parts ...st.MessagePart) error {
	var sb strings.Builder
	for _, part := range parts {
		sb.WriteString(part.String())
	}
	return c.StartTurn(context.Background(), InputFromText(sb.String()))
}

// StartTurn sends a message as a new turn. It returns
// st.ErrMessageValidationChanging while a turn is running or the agent is
// still starting, so callers can queue the message.
func (c *Conversation) StartTurn(ctx context.Context, input Input) error {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return st.ErrMessageValidationEmpty
	}
	c.mu.Lock()
	if !c.started || c.turnActive || c.adapter == nil {
		c.mu.Unlock()
		return st.ErrMessageValidationChanging
	}
	adapter := c.adapter
	gen := c.generation
	c.turnActive = true
	idx := c.appendUserMessageLocked(text)
	c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
	c.cfg.Emitter.EmitStatus(st.ConversationStatusChanging)
	c.mu.Unlock()

	if err := adapter.StartTurn(ctx, input); err != nil {
		c.mu.Lock()
		if gen == c.generation {
			c.turnActive = false
			c.removeMessageLocked(idx)
			c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
			c.cfg.Emitter.EmitStatus(c.statusLocked())
		}
		c.mu.Unlock()
		if errors.Is(err, ErrTurnActive) {
			return st.ErrMessageValidationChanging
		}
		return err
	}
	return nil
}

// Steer adds a message to the running turn.
func (c *Conversation) Steer(ctx context.Context, input Input) error {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return st.ErrMessageValidationEmpty
	}
	c.mu.Lock()
	adapter := c.adapter
	if adapter == nil || !c.started {
		c.mu.Unlock()
		return ErrNotStarted
	}
	if !adapter.Capabilities().Steer {
		c.mu.Unlock()
		return ErrUnsupported
	}
	if !c.turnActive {
		c.mu.Unlock()
		return ErrNoActiveTurn
	}
	gen := c.generation
	idx := c.appendUserMessageLocked(text)
	c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
	c.mu.Unlock()

	if err := adapter.Steer(ctx, input); err != nil {
		c.mu.Lock()
		if gen == c.generation {
			c.removeMessageLocked(idx)
			c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

// Interrupt stops the running turn.
func (c *Conversation) Interrupt(ctx context.Context) error {
	c.mu.Lock()
	adapter := c.adapter
	active := c.turnActive
	c.mu.Unlock()
	if adapter == nil {
		return ErrNotStarted
	}
	if !adapter.Capabilities().Interrupt {
		return ErrUnsupported
	}
	if !active {
		return ErrNoActiveTurn
	}
	return adapter.Interrupt(ctx)
}

// Requests returns the pending requests and the most recently closed ones,
// oldest first.
func (c *Conversation) Requests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RequestState, 0, len(c.requests))
	for _, r := range c.requests {
		out = append(out, *r)
	}
	return out
}

// PendingRequests returns the requests the agent is waiting on.
func (c *Conversation) PendingRequests() []RequestState {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []RequestState
	for _, r := range c.requests {
		if r.Status == RequestStatusPending {
			out = append(out, *r)
		}
	}
	return out
}

// Request returns one request by ID.
func (c *Conversation) Request(id string) (RequestState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.findRequestLocked(id); r != nil {
		return *r, true
	}
	return RequestState{}, false
}

// Respond answers a pending request. It returns ErrRequestNotFound if the
// request is not pending and ErrInvalidResponse if the response doesn't fit.
func (c *Conversation) Respond(ctx context.Context, id string, response Response) (RequestState, error) {
	c.mu.Lock()
	r := c.findRequestLocked(id)
	if r == nil || r.Status != RequestStatusPending || c.responding[id] {
		var state RequestState
		if r != nil {
			state = *r
		}
		c.mu.Unlock()
		return state, ErrRequestNotFound
	}
	if err := validateResponse(r.Request, response); err != nil {
		state := *r
		c.mu.Unlock()
		return state, err
	}
	adapter := c.adapter
	gen := c.generation
	c.responding[id] = true
	c.mu.Unlock()

	var err error
	if adapter == nil {
		err = ErrNotStarted
	} else {
		err = adapter.Respond(ctx, id, response)
	}

	c.mu.Lock()
	delete(c.responding, id)
	r = c.findRequestLocked(id)
	if r == nil || gen != c.generation {
		c.mu.Unlock()
		if err == nil {
			err = ErrRequestNotFound
		}
		return RequestState{}, err
	}
	if err != nil {
		state := *r
		c.mu.Unlock()
		return state, err
	}
	if r.Status == RequestStatusPending {
		now := c.cfg.Clock.Now()
		r.Status = RequestStatusResolved
		resp := response
		r.Response = &resp
		r.ClosedAt = &now
	}
	state := *r
	c.cfg.Observer.RequestUpdated(state)
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()
	return state, nil
}

// validateResponse checks that a response answers the request.
func validateResponse(req Request, resp Response) error {
	switch req.Kind {
	case RequestKindApproval, RequestKindPlan:
		for _, opt := range req.Options {
			if opt.ID == resp.OptionID {
				return nil
			}
		}
		return fmt.Errorf("%w: unknown option %q", ErrInvalidResponse, resp.OptionID)
	case RequestKindQuestion:
		if resp.OptionID != "" {
			for _, opt := range req.Options {
				if opt.ID == resp.OptionID {
					return nil
				}
			}
			return fmt.Errorf("%w: unknown option %q", ErrInvalidResponse, resp.OptionID)
		}
		for _, q := range req.Questions {
			answers := resp.Answers[q.ID]
			if len(answers) == 0 {
				return fmt.Errorf("%w: question %q has no answer", ErrInvalidResponse, q.ID)
			}
			if len(answers) > 1 && !q.MultiSelect {
				return fmt.Errorf("%w: question %q takes one answer", ErrInvalidResponse, q.ID)
			}
			if q.FreeText || len(q.Options) == 0 {
				continue
			}
			for _, a := range answers {
				if !slices.ContainsFunc(q.Options, func(o QuestionOption) bool { return o.Label == a }) {
					return fmt.Errorf("%w: %q is not an option of question %q", ErrInvalidResponse, a, q.ID)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown request kind %q", ErrInvalidResponse, req.Kind)
	}
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
		if r.Status == RequestStatusPending && !c.responding[r.ID] {
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

func (c *Conversation) resetMessagesLocked() {
	c.messages = nil
	c.nextID = 0
	c.agentIdx = -1
	c.segment = nil
	c.texts = make(map[string]string)
}

func (c *Conversation) appendUserMessageLocked(text string) int {
	c.messages = append(c.messages, st.ConversationMessage{
		Id: c.nextID, Message: text, Role: st.ConversationRoleUser, Time: c.cfg.Clock.Now(),
	})
	c.nextID++
	c.agentIdx = -1
	c.segment = nil
	return len(c.messages) - 1
}

// removeMessageLocked removes a user message whose delivery failed. Only the
// last message can be removed without renumbering, which is the only case:
// nothing else is appended while the conversation waits for the adapter.
func (c *Conversation) removeMessageLocked(idx int) {
	if idx != len(c.messages)-1 {
		return
	}
	c.messages = c.messages[:idx]
	c.nextID--
	c.agentIdx = -1
	c.segment = nil
}

func (c *Conversation) findRequestLocked(id string) *RequestState {
	for _, r := range c.requests {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// expireRequestsLocked closes every pending request and returns their new
// states. Questions can still be answered with a message after their turn
// ended; approvals and plans can't.
func (c *Conversation) expireRequestsLocked(capability ResponseCapability) []RequestState {
	var changed []RequestState
	now := c.cfg.Clock.Now()
	for _, r := range c.requests {
		if r.Status != RequestStatusPending {
			continue
		}
		r.Status = RequestStatusExpired
		r.ClosedAt = &now
		r.Capability = capability
		if capability == ResponseMessage && r.Kind != RequestKindQuestion {
			r.Capability = ResponseNotResumable
		}
		changed = append(changed, *r)
	}
	c.trimRequestsLocked()
	return changed
}

func (c *Conversation) trimRequestsLocked() {
	closed := 0
	for _, r := range c.requests {
		if r.Status != RequestStatusPending {
			closed++
		}
	}
	if closed <= maxClosedRequests {
		return
	}
	drop := closed - maxClosedRequests
	kept := c.requests[:0]
	for _, r := range c.requests {
		if drop > 0 && r.Status != RequestStatusPending {
			drop--
			continue
		}
		kept = append(kept, r)
	}
	c.requests = kept
}

// assistantText joins the text blocks of a rich message.
func assistantText(msg jsonlwatcher.RichMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// sink is the Sink handed to the adapter of one generation.
type sink struct {
	c   *Conversation
	gen int
}

var _ Sink = (*sink)(nil)

// lock locks the conversation and reports whether the sink is current. The
// caller must unlock in both cases.
func (s *sink) lock() bool {
	s.c.mu.Lock()
	return s.gen == s.c.generation
}

func (s *sink) TurnStarted() {
	c := s.c
	if !s.lock() {
		c.mu.Unlock()
		return
	}
	c.turnActive = true
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()
}

func (s *sink) TurnCompleted(result TurnResult) {
	c := s.c
	if !s.lock() {
		c.mu.Unlock()
		return
	}
	c.turnActive = false
	c.agentIdx = -1
	c.segment = nil
	changed := c.expireRequestsLocked(ResponseMessage)
	for _, state := range changed {
		c.cfg.Observer.RequestUpdated(state)
	}
	if result.Error != "" {
		c.cfg.Emitter.EmitError(result.Error, st.ErrorLevelError)
	}
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()
}

func (s *sink) RichMessage(msg jsonlwatcher.RichMessage) {
	c := s.c
	if !s.lock() {
		c.mu.Unlock()
		return
	}
	c.cfg.Observer.RichMessage(msg)
	if msg.Role != "assistant" {
		c.mu.Unlock()
		return
	}
	text := assistantText(msg)
	if _, seen := c.texts[msg.MessageID]; !seen && text == "" {
		// Wait for text before tying the message to the agent message, so
		// a message made only of tool calls doesn't start one.
		c.mu.Unlock()
		return
	}
	c.texts[msg.MessageID] = text
	if !slices.Contains(c.segment, msg.MessageID) {
		c.segment = append(c.segment, msg.MessageID)
	}
	var parts []string
	for _, id := range c.segment {
		if t := c.texts[id]; t != "" {
			parts = append(parts, t)
		}
	}
	joined := strings.Join(parts, "\n\n")
	if c.agentIdx < 0 {
		if joined == "" {
			c.mu.Unlock()
			return
		}
		c.messages = append(c.messages, st.ConversationMessage{
			Id: c.nextID, Role: st.ConversationRoleAgent, Time: c.cfg.Clock.Now(),
		})
		c.nextID++
		c.agentIdx = len(c.messages) - 1
	}
	c.messages[c.agentIdx].Message = joined
	c.cfg.Emitter.EmitMessages(slices.Clone(c.messages))
	c.mu.Unlock()
}

func (s *sink) SessionEvents(events []jsonlwatcher.SessionEvent) {
	c := s.c
	if s.lock() {
		c.cfg.Observer.SessionEvents(events)
	}
	c.mu.Unlock()
}

func (s *sink) RequestOpened(req Request) {
	c := s.c
	if !s.lock() {
		c.mu.Unlock()
		return
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = c.cfg.Clock.Now()
	}
	if req.Capability == "" {
		req.Capability = ResponseLive
	}
	state := &RequestState{Request: req, Status: RequestStatusPending}
	if existing := c.findRequestLocked(req.ID); existing != nil {
		*existing = *state
	} else {
		c.requests = append(c.requests, state)
	}
	c.cfg.Observer.RequestUpdated(*state)
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()
}

func (s *sink) RequestClosed(id string) {
	c := s.c
	if !s.lock() {
		c.mu.Unlock()
		return
	}
	r := c.findRequestLocked(id)
	if r == nil || r.Status != RequestStatusPending || c.responding[id] {
		// A request being answered here closes through Respond.
		c.mu.Unlock()
		return
	}
	now := c.cfg.Clock.Now()
	r.Status = RequestStatusWithdrawn
	r.ClosedAt = &now
	c.trimRequestsLocked()
	c.cfg.Observer.RequestUpdated(*r)
	c.cfg.Emitter.EmitStatus(c.statusLocked())
	c.mu.Unlock()
}

func (s *sink) SessionName(name string) {
	c := s.c
	if s.lock() {
		c.cfg.Observer.SessionName(name)
	}
	c.mu.Unlock()
}

func (s *sink) SessionRef(ref string) {
	c := s.c
	if s.lock() {
		c.sessionRef = ref
		c.cfg.Observer.SessionRef(ref)
	}
	c.mu.Unlock()
}

func (s *sink) Error(message string, level st.ErrorLevel) {
	c := s.c
	current := s.lock()
	c.mu.Unlock()
	if current {
		c.cfg.Emitter.EmitError(message, level)
	}
}
