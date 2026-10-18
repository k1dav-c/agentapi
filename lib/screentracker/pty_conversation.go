package screentracker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/quartz"
)

// MessagePartText is text typed into the agent's terminal. A hidden part
// (a bracketed paste marker, say) isn't part of the message's text.
type MessagePartText struct {
	Content string
	Hidden  bool
}

var _ MessagePart = &MessagePartText{}

func (p MessagePartText) Do(writer AgentIO) error {
	_, err := writer.Write([]byte(p.Content))
	return err
}

func (p MessagePartText) String() string {
	if p.Hidden {
		return ""
	}
	return p.Content
}

// PTYConversationConfig is the configuration for a PTYConversation.
type PTYConversationConfig struct {
	AgentType msgfmt.AgentType
	AgentIO   AgentIO
	// Clock provides time operations for the conversation
	Clock quartz.Clock
	// How often to take a snapshot for the stability check
	SnapshotInterval time.Duration
	// How long the screen should not change to be considered stable
	ScreenStabilityLength time.Duration
	// Function to format the messages received from the agent
	// userInput is the last user message
	FormatMessage func(message string, userInput string) string
	// ReadyForInitialPrompt detects whether the agent has initialized and is ready to accept the initial prompt
	ReadyForInitialPrompt func(message string) bool
	// ReadyWhenStable makes a screen that has been stable for
	// ScreenStabilityLength count as ready too, for an agent that may never
	// show what ReadyForInitialPrompt looks for (a terminal program without
	// an input box).
	ReadyWhenStable bool
	// StabilityScreen returns the part of the screen used to decide whether
	// the agent is still working. It lets agents exclude regions that keep
	// changing while idle (e.g. a background agents panel with ticking
	// timers below the input box). Defaults to the whole screen.
	StabilityScreen func(screen string) string
	// FormatToolCall removes the coder report_task tool call from the agent message and also returns the array of removed tool calls
	FormatToolCall func(message string) (string, []string)
	// InitialPrompt is the initial prompt to send to the agent once ready
	InitialPrompt          []MessagePart
	Logger                 *slog.Logger
	StatePersistenceConfig StatePersistenceConfig
}

func (cfg PTYConversationConfig) getStableSnapshotsThreshold() int {
	length := cfg.ScreenStabilityLength.Milliseconds()
	interval := cfg.SnapshotInterval.Milliseconds()
	threshold := int(length / interval)
	if length%interval != 0 {
		threshold++
	}
	return threshold + 1
}

// PTYConversation is a conversation that uses a pseudo-terminal (PTY) for communication.
// It snapshots on output and samples until the screen is stable.
type PTYConversation struct {
	cfg     PTYConversationConfig
	emitter Emitter
	// How many stable snapshots are required to consider the screen stable
	stableSnapshotsThreshold int
	// snapshots counts the snapshots taken since the start or the last
	// reset, up to stableSnapshotsThreshold: fewer means initializing.
	snapshots                   int
	messages                    []ConversationMessage
	screenBeforeLastUserMessage string
	lock                        sync.Mutex

	// stableCount is the number of consecutive snapshots with an identical
	// stability region (see PTYConversationConfig.StabilityScreen),
	// including the most recent one. Maintained incrementally by
	// snapshotLocked so stability checks don't have to compare every
	// snapshot in the buffer on each tick.
	stableCount int
	// lastSnapshotScreen is the stability region of the most recent snapshot.
	lastSnapshotScreen string

	// fmtCache short-circuits updateLastAgentMessageLocked when the screen
	// hasn't changed since the last (expensive) diff+format pass. Any
	// mutation of the other inputs to that computation (messages,
	// screenBeforeLastUserMessage, load-state status) must invalidate it
	// by setting fmtCacheValid to false.
	fmtCacheValid  bool
	fmtCacheScreen string

	// ReadyForInitialPrompt scans the screen with regexps; only re-run it
	// when the screen differs from the one last checked.
	readinessChecked     bool
	readinessCheckScreen string

	// pending is the message waiting to be sent: at most one, since Send
	// refuses another while one waits (the status is changing) and the
	// initial prompt is queued before Send accepts any. Queueing never
	// blocks, so it can happen under c.lock.
	pending *outboundMessage
	// sendGen counts resets: a message queued, or on its way, before a
	// reset doesn't reach the new conversation.
	sendGen uint64
	// cancelSend stops the message on its way, for Reset.
	cancelSend context.CancelFunc
	// sendErr is set once the send loop has stopped; Send fails with it.
	sendErr error
	// sendingMessage is true while the send loop is processing a message.
	// Set under lock in the snapshot loop when signaling, cleared under
	// lock in the send loop after sendMessage returns.
	sendingMessage bool
	// writingMessage is true while writeStabilize is executing.
	// When true, updateLastAgentMessageLocked skips updates to avoid capturing terminal echo.
	writingMessage bool
	// stableSignal is used by the snapshot loop to signal the send loop
	// when the agent is stable and a message is pending.
	stableSignal chan struct{}
	snapshotWake chan struct{}
	// toolCallMessageSet keeps track of the tool calls that have been detected & logged in the current agent message
	toolCallMessageSet map[string]bool
	// dirty tracks whether the conversation state has changed since the last save
	dirty bool
	// userSentMessageAfterLoadState tracks if the user has sent their first message after we load the state
	userSentMessageAfterLoadState bool
	// loadStateStatus tracks the status of loading conversation state from file.
	loadStateStatus LoadStateStatus
	// initialPromptReady is set to true when ReadyForInitialPrompt returns true.
	// Checked inline in the snapshot loop on each tick.
	initialPromptReady bool
	// initialPromptSent is set to true when the initial prompt has been queued.
	initialPromptSent bool
}

var _ Conversation = &PTYConversation{}

func NewPTY(ctx context.Context, cfg PTYConversationConfig, emitter Emitter) *PTYConversation {
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if emitter == nil {
		emitter = NopEmitter{}
	}
	threshold := cfg.getStableSnapshotsThreshold()
	c := &PTYConversation{
		cfg:                      cfg,
		emitter:                  emitter,
		stableSnapshotsThreshold: threshold,
		messages: []ConversationMessage{
			{
				Message: "",
				Role:    ConversationRoleAgent,
				Time:    cfg.Clock.Now(),
			},
		},
		stableSignal:                  make(chan struct{}, 1),
		snapshotWake:                  make(chan struct{}, 1),
		toolCallMessageSet:            make(map[string]bool),
		dirty:                         false,
		userSentMessageAfterLoadState: false,
		loadStateStatus:               LoadStatePending,
		writingMessage:                false,
	}
	if c.cfg.ReadyForInitialPrompt == nil {
		c.cfg.ReadyForInitialPrompt = func(string) bool { return true }
	}
	if c.cfg.StabilityScreen == nil {
		c.cfg.StabilityScreen = func(screen string) string { return screen }
	}
	return c
}

// Start takes snapshots of the screen as it changes (or every
// SnapshotInterval) and starts the loop that sends messages.
func (c *PTYConversation) Start(ctx context.Context) {
	if source, ok := c.cfg.AgentIO.(interface{ ScreenUpdates() <-chan struct{} }); ok {
		go c.watchScreen(ctx, source, c.snapshot)
	} else {
		// Keep compatibility with AgentIO implementations without notifications.
		c.cfg.Clock.TickerFunc(ctx, c.cfg.SnapshotInterval, c.snapshot, "snapshot")
	}

	go c.sendLoop(ctx)
}

// snapshot takes a snapshot of the screen, acts on what it shows (the
// agent is ready, the saved conversation can be loaded, the initial prompt
// or a message can go out) and emits the conversation's state.
func (c *PTYConversation) snapshot() error {
	c.lock.Lock()
	screen := c.cfg.AgentIO.ReadScreen()
	c.snapshotLocked(screen)
	status := c.statusLocked()
	messages := c.messagesLocked()
	c.checkReadyLocked(screen)
	loadErr := c.loadStateWhenReadyLocked()
	c.queueInitialPromptLocked()
	c.signalSendLocked()
	c.lock.Unlock()

	if loadErr != "" {
		c.emitter.EmitError(loadErr, ErrorLevelWarning)
	}
	c.emitter.EmitStatus(status)
	c.emitter.EmitMessages(messages)
	c.emitter.EmitScreen(screen)
	return nil
}

// checkReadyLocked notes when screen shows the agent ready for the initial
// prompt. It is checked apart from the status, which is "changing" while a
// message waits. Caller holds c.lock.
func (c *PTYConversation) checkReadyLocked(screen string) {
	if c.initialPromptReady {
		return
	}
	if c.cfg.ReadyWhenStable && c.isScreenStableLocked() {
		c.initialPromptReady = true
		// This snapshot's status predates readiness, and a stable screen
		// puts the snapshot loop to sleep: one more snapshot emits it.
		c.wakeSnapshot()
		return
	}
	if c.readinessChecked && screen == c.readinessCheckScreen {
		return
	}
	c.readinessChecked = true
	c.readinessCheckScreen = screen
	if c.cfg.ReadyForInitialPrompt(screen) {
		c.initialPromptReady = true
	}
}

// loadStateWhenReadyLocked loads the saved conversation once the agent is
// ready, and returns the error to show if that failed. Caller holds
// c.lock.
func (c *PTYConversation) loadStateWhenReadyLocked() string {
	if !c.initialPromptReady || c.loadStateStatus != LoadStatePending || !c.cfg.StatePersistenceConfig.LoadState {
		return ""
	}
	// Load-state status and restored messages feed into
	// updateLastAgentMessageLocked; force a fresh pass.
	c.fmtCacheValid = false
	err, shouldEmit := c.loadStateLocked()
	if err == nil {
		c.loadStateStatus = LoadStateSucceeded
		return ""
	}
	c.loadStateStatus = LoadStateFailed
	if !shouldEmit {
		return ""
	}
	c.cfg.Logger.Error("Failed to load state", "error", err)
	return fmt.Sprintf("Failed to restore previous session: %v", err)
}

// queueInitialPromptLocked queues the initial prompt once the agent is
// ready. Caller holds c.lock.
func (c *PTYConversation) queueInitialPromptLocked() {
	if !c.initialPromptReady || len(c.cfg.InitialPrompt) == 0 || c.initialPromptSent || c.pending != nil {
		return
	}
	c.pending = &outboundMessage{parts: c.cfg.InitialPrompt, initial: true, gen: c.sendGen}
	c.initialPromptSent = true
	c.dirty = true
}

// signalSendLocked tells the send loop to send the message that waits,
// once the agent is ready and its screen stable. Caller holds c.lock.
func (c *PTYConversation) signalSendLocked() {
	if !c.initialPromptReady || c.pending == nil || c.sendingMessage || !c.isScreenStableLocked() {
		return
	}
	select {
	case c.stableSignal <- struct{}{}:
		c.sendingMessage = true
	default:
		// Signal already pending
	}
}

func (c *PTYConversation) Status() ConversationStatus {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.statusLocked()
}

// caller MUST hold c.lock
func (c *PTYConversation) statusLocked() ConversationStatus {
	// sanity checks
	if c.stableSnapshotsThreshold == 0 {
		panic("stable snapshots threshold is 0. can't check stability")
	}

	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == ConversationRoleUser {
		// if the last message is a user message then the snapshot loop hasn't
		// been triggered since the last user message, and we should assume
		// the screen is changing
		return ConversationStatusChanging
	}

	if c.snapshots < c.stableSnapshotsThreshold {
		return ConversationStatusInitializing
	}

	if !c.isScreenStableLocked() {
		return ConversationStatusChanging
	}

	// The send loop gates stableSignal on initialPromptReady.
	// Report "changing" until readiness is detected so that Send()
	// rejects with ErrMessageValidationChanging instead of blocking
	// indefinitely on a stableSignal that will never fire.
	if !c.initialPromptReady {
		return ConversationStatusChanging
	}

	// Handle initial prompt readiness: report "changing" until the queue is drained
	// to avoid the status flipping "changing" -> "stable" -> "changing"
	if c.pending != nil || c.sendingMessage {
		return ConversationStatusChanging
	}

	return ConversationStatusStable
}

func (c *PTYConversation) Messages() []ConversationMessage {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.messagesLocked()
}

// messagesLocked returns a copy of messages. Caller MUST hold c.lock.
func (c *PTYConversation) messagesLocked() []ConversationMessage {
	result := make([]ConversationMessage, len(c.messages))
	copy(result, c.messages)
	return result
}

// Reset clears all conversation state back to the initial empty state.
func (c *PTYConversation) Reset() {
	defer c.wakeSnapshot()
	c.lock.Lock()
	defer c.lock.Unlock()

	// The message waiting, or on its way, belonged to the old conversation.
	c.sendGen++
	if c.cancelSend != nil {
		c.cancelSend()
	}
	if msg := c.pending; msg != nil {
		c.pending = nil
		msg.finish(ErrConversationReset)
	}
	c.messages = []ConversationMessage{
		{
			Message: "",
			Role:    ConversationRoleAgent,
			Time:    c.cfg.Clock.Now(),
		},
	}
	c.screenBeforeLastUserMessage = ""
	c.snapshots = 0
	c.stableCount = 0
	c.lastSnapshotScreen = ""
	c.fmtCacheValid = false
	c.toolCallMessageSet = make(map[string]bool)
	c.dirty = false
	c.userSentMessageAfterLoadState = false
	c.initialPromptReady = false
	c.initialPromptSent = false
	c.readinessChecked = false
	c.readinessCheckScreen = ""
}
