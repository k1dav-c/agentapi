package screentracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/util"
	"github.com/coder/quartz"
)

const (
	// writeStabilizeEchoTimeout is the timeout for the echo
	// detection WaitFor loop in writeStabilize Phase 1. The
	// effective ceiling may be slightly longer because the
	// stability check inside the condition runs outside
	// WaitFor's timeout select. Non-echoing agents (e.g. TUI
	// agents using bracketed paste) will hit this timeout,
	// which is non-fatal.
	//
	// TODO: move to PTYConversationConfig if agents need
	// different echo detection windows.
	writeStabilizeEchoTimeout = 2 * time.Second

	// writeStabilizeProcessTimeout is the maximum time to wait
	// for the screen to change after sending a carriage return.
	// This detects whether the agent is actually processing the
	// input.
	writeStabilizeProcessTimeout = 15 * time.Second
)

// A screenSnapshot represents a snapshot of the PTY at a specific time.
type screenSnapshot struct {
	timestamp time.Time
	screen    string
}

type MessagePartText struct {
	Content string
	Alias   string
	Hidden  bool
}

type AgentState struct {
	Version           int                   `json:"version"`
	Messages          []ConversationMessage `json:"messages"`
	InitialPrompt     string                `json:"initial_prompt"`
	InitialPromptSent bool                  `json:"initial_prompt_sent"`
}

// LoadStateStatus represents the state of loading persisted conversation state.
type LoadStateStatus int

const (
	// LoadStatePending indicates state loading has not been attempted yet.
	LoadStatePending LoadStateStatus = iota
	// LoadStateSucceeded indicates state was successfully loaded.
	LoadStateSucceeded
	// LoadStateFailed indicates state loading was attempted but failed.
	LoadStateFailed
)

var _ MessagePart = &MessagePartText{}

func (p MessagePartText) Do(writer AgentIO) error {
	_, err := writer.Write([]byte(p.Content))
	return err
}

func (p MessagePartText) String() string {
	if p.Hidden {
		return ""
	}
	if p.Alias != "" {
		return p.Alias
	}
	return p.Content
}

// outboundMessage wraps a message to be sent with its error channel
type outboundMessage struct {
	parts []MessagePart
	errCh chan error
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
	stableSnapshotsThreshold    int
	snapshotBuffer              *RingBuffer[screenSnapshot]
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

	// outboundQueue holds messages waiting to be sent to the agent.
	// Buffer size is 1. Callers are expected to be serialized (the HTTP
	// layer holds s.mu, and Send blocks until the message is processed),
	// so ordering is preserved.
	outboundQueue chan outboundMessage
	// sendingMessage is true while the send loop is processing a message.
	// Set under lock in the snapshot loop when signaling, cleared under
	// lock in the send loop after sendMessage returns.
	sendingMessage bool
	// writingMessage is true while writeStabilize is executing.
	// When true, updateLastAgentMessageLocked skips updates to avoid capturing terminal echo.
	writingMessage bool
	// stableSignal is used by the snapshot loop to signal the send loop
	// when the agent is stable and there are items in the outbound queue.
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
	// initialPromptSent is set to true when the initial prompt has been enqueued to the outbound queue.
	initialPromptSent bool
}

var _ Conversation = &PTYConversation{}

type noopEmitter struct{}

func (noopEmitter) EmitMessages([]ConversationMessage) {}
func (noopEmitter) EmitStatus(ConversationStatus)      {}
func (noopEmitter) EmitScreen(string)                  {}
func (noopEmitter) EmitError(_ string, _ ErrorLevel)   {}

func NewPTY(ctx context.Context, cfg PTYConversationConfig, emitter Emitter) *PTYConversation {
	if cfg.Clock == nil {
		cfg.Clock = quartz.NewReal()
	}
	if emitter == nil {
		emitter = noopEmitter{}
	}
	threshold := cfg.getStableSnapshotsThreshold()
	c := &PTYConversation{
		cfg:                      cfg,
		emitter:                  emitter,
		stableSnapshotsThreshold: threshold,
		snapshotBuffer:           NewRingBuffer[screenSnapshot](threshold),
		messages: []ConversationMessage{
			{
				Message: "",
				Role:    ConversationRoleAgent,
				Time:    cfg.Clock.Now(),
			},
		},
		outboundQueue:                 make(chan outboundMessage, 1),
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

func (c *PTYConversation) Start(ctx context.Context) {
	snapshot := func() error {
		c.lock.Lock()
		screen := c.cfg.AgentIO.ReadScreen()
		c.snapshotLocked(screen)
		status := c.statusLocked()
		messages := c.messagesLocked()

		// Signal send loop if agent is ready and queue has items.
		// We check readiness independently of statusLocked() because
		// statusLocked() returns "changing" when queue has items.
		if !c.initialPromptReady && (!c.readinessChecked || screen != c.readinessCheckScreen) {
			c.readinessChecked = true
			c.readinessCheckScreen = screen
			if c.cfg.ReadyForInitialPrompt(screen) {
				c.initialPromptReady = true
			}
		}

		var loadErr string
		if c.initialPromptReady && c.loadStateStatus == LoadStatePending && c.cfg.StatePersistenceConfig.LoadState {
			if err, shouldEmit := c.loadStateLocked(); err != nil {
				c.loadStateStatus = LoadStateFailed
				if shouldEmit {
					c.cfg.Logger.Error("Failed to load state", "error", err)
					loadErr = fmt.Sprintf("Failed to restore previous session: %v", err)
				}
			} else {
				c.loadStateStatus = LoadStateSucceeded
			}
			// Load-state status and restored messages feed into
			// updateLastAgentMessageLocked; force a fresh pass.
			c.fmtCacheValid = false
		}

		if c.initialPromptReady && len(c.cfg.InitialPrompt) > 0 && !c.initialPromptSent {
			// Safe to send under lock: the queue is guaranteed empty here because
			// statusLocked blocks Send until the snapshot buffer fills, which
			// cannot happen before this first enqueue completes.
			c.outboundQueue <- outboundMessage{parts: c.cfg.InitialPrompt, errCh: nil}
			c.initialPromptSent = true
			c.dirty = true
		}

		if c.initialPromptReady && len(c.outboundQueue) > 0 && c.isScreenStableLocked() {
			select {
			case c.stableSignal <- struct{}{}:
				c.sendingMessage = true
			default:
				// Signal already pending
			}
		}
		c.lock.Unlock()

		if loadErr != "" {
			c.emitter.EmitError(loadErr, ErrorLevelWarning)
		}
		c.emitter.EmitStatus(status)
		c.emitter.EmitMessages(messages)
		c.emitter.EmitScreen(screen)
		return nil
	}
	if source, ok := c.cfg.AgentIO.(interface{ ScreenUpdates() <-chan struct{} }); ok {
		go c.watchScreen(ctx, source, snapshot)
	} else {
		// Keep compatibility with AgentIO implementations without notifications.
		c.cfg.Clock.TickerFunc(ctx, c.cfg.SnapshotInterval, snapshot, "snapshot")
	}

	// Send loop - primary call site for sendLocked() in production
	go func() {
		defer func() {
			// Drain outbound queue so Send() callers don't block forever.
			for {
				select {
				case msg := <-c.outboundQueue:
					if msg.errCh != nil {
						msg.errCh <- ctx.Err()
						close(msg.errCh)
					}
				default:
					return
				}
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stableSignal:
				select {
				case <-ctx.Done():
					return
				case msg := <-c.outboundQueue:
					err := c.sendMessage(ctx, msg.parts...)
					c.lock.Lock()
					c.sendingMessage = false
					c.lock.Unlock()
					c.wakeSnapshot()
					if msg.errCh != nil {
						msg.errCh <- err
						// Close so the Send() caller's <-errCh never blocks
						// if it has already consumed the error value.
						close(msg.errCh)
					}
				default:
					c.cfg.Logger.Error("received stable signal but outbound queue is empty")
				}
			}
		}
	}()
}

func (c *PTYConversation) lastMessage(role ConversationRole) ConversationMessage {
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == role {
			return c.messages[i]
		}
	}
	return ConversationMessage{}
}

// previousTurnAgentMessageLocked returns the last agent message from before
// the last user message, i.e. the finalized message of the previous turn.
// Returns a zero value if there is no such message. Caller MUST hold c.lock.
func (c *PTYConversation) previousTurnAgentMessageLocked() ConversationMessage {
	lastUserIdx := -1
	for i := len(c.messages) - 1; i >= 0; i-- {
		if c.messages[i].Role == ConversationRoleUser {
			lastUserIdx = i
			break
		}
	}
	for i := lastUserIdx - 1; i >= 0; i-- {
		if c.messages[i].Role == ConversationRoleAgent {
			return c.messages[i]
		}
	}
	return ConversationMessage{}
}

// caller MUST hold c.lock
func (c *PTYConversation) updateLastAgentMessageLocked(screen string, timestamp time.Time) {
	if c.writingMessage {
		return
	}
	// The result only depends on the screen and on conversation state that
	// invalidates fmtCache when mutated, so an unchanged screen means the
	// whole diff+format pass would produce the same outcome as last time.
	if c.fmtCacheValid && screen == c.fmtCacheScreen {
		return
	}
	agentMessage := screenDiff(c.screenBeforeLastUserMessage, screen, c.cfg.AgentType)
	lastUserMessage := c.lastMessage(ConversationRoleUser)
	var toolCalls []string
	if c.cfg.FormatMessage != nil {
		agentMessage = c.cfg.FormatMessage(agentMessage, lastUserMessage.Message)
	}
	restoredFromState := false
	if c.loadStateStatus == LoadStateSucceeded && !c.userSentMessageAfterLoadState && len(c.messages) > 0 &&
		c.messages[len(c.messages)-1].Role == ConversationRoleAgent {
		agentMessage = c.messages[len(c.messages)-1].Message
		restoredFromState = true
	}
	if c.cfg.FormatToolCall != nil {
		agentMessage, toolCalls = c.cfg.FormatToolCall(agentMessage)
	}
	// Guard against TUI re-renders leaking the previous turn's output into
	// the current turn's message (see trimPreviousMessageOverlap). Skip for
	// messages restored verbatim from persisted state.
	if !restoredFromState {
		if prev := c.previousTurnAgentMessageLocked(); prev.Message != "" {
			agentMessage = trimPreviousMessageOverlap(prev.Message, agentMessage)
		}
	}
	for _, toolCall := range toolCalls {
		if c.toolCallMessageSet[toolCall] == false {
			c.toolCallMessageSet[toolCall] = true
			c.cfg.Logger.Info("Tool call detected", "toolCall", toolCall)
		}
	}
	// The computation for this screen is complete past this point; record it
	// so identical screens can skip the pass entirely on subsequent ticks.
	c.fmtCacheValid = true
	c.fmtCacheScreen = screen

	shouldCreateNewMessage := len(c.messages) == 0 || c.messages[len(c.messages)-1].Role == ConversationRoleUser
	lastAgentMessage := c.lastMessage(ConversationRoleAgent)
	if lastAgentMessage.Message == agentMessage {
		return
	}
	conversationMessage := ConversationMessage{
		Message: agentMessage,
		Role:    ConversationRoleAgent,
		Time:    timestamp,
	}
	if shouldCreateNewMessage {
		c.messages = append(c.messages, conversationMessage)

		// Cleanup
		c.toolCallMessageSet = make(map[string]bool)

	} else {
		c.messages[len(c.messages)-1] = conversationMessage
	}
	c.messages[len(c.messages)-1].Id = len(c.messages) - 1

	c.dirty = true
}

// caller MUST hold c.lock
func (c *PTYConversation) snapshotLocked(screen string) {
	snapshot := screenSnapshot{
		timestamp: c.cfg.Clock.Now(),
		screen:    screen,
	}
	region := c.cfg.StabilityScreen(screen)
	if c.snapshotBuffer.Len() > 0 && region == c.lastSnapshotScreen {
		c.stableCount++
	} else {
		c.stableCount = 1
	}
	c.lastSnapshotScreen = region
	c.snapshotBuffer.Add(snapshot)
	c.updateLastAgentMessageLocked(screen, snapshot.timestamp)
}

func (c *PTYConversation) Send(messageParts ...MessagePart) error {
	// Validate message content before enqueueing
	message := buildStringFromMessageParts(messageParts)
	if message != msgfmt.TrimWhitespace(message) {
		return ErrMessageValidationWhitespace
	}
	if message == "" {
		return ErrMessageValidationEmpty
	}

	c.lock.Lock()
	if c.statusLocked() != ConversationStatusStable {
		c.lock.Unlock()
		return ErrMessageValidationChanging
	}
	c.lock.Unlock()

	errCh := make(chan error, 1)
	c.outboundQueue <- outboundMessage{parts: messageParts, errCh: errCh}
	c.wakeSnapshot()
	return <-errCh
}

// sendMessage sends a message to the agent. It acquires and releases c.lock
// around the parts that access shared state, but releases it during
// writeStabilize to avoid blocking the snapshot loop.
func (c *PTYConversation) sendMessage(ctx context.Context, messageParts ...MessagePart) error {
	message := buildStringFromMessageParts(messageParts)

	c.lock.Lock()
	screenBeforeMessage := c.cfg.AgentIO.ReadScreen()
	now := c.cfg.Clock.Now()
	c.updateLastAgentMessageLocked(screenBeforeMessage, now)
	c.writingMessage = true
	c.lock.Unlock()

	if err := c.writeStabilize(ctx, messageParts...); err != nil {
		c.lock.Lock()
		defer c.lock.Unlock()
		c.writingMessage = false
		return fmt.Errorf("failed to send message: %w", err)
	}

	c.lock.Lock()
	c.screenBeforeLastUserMessage = screenBeforeMessage
	c.messages = append(c.messages, ConversationMessage{
		Id:      len(c.messages),
		Message: message,
		Role:    ConversationRoleUser,
		Time:    now,
	})
	c.userSentMessageAfterLoadState = true
	c.writingMessage = false
	// screenBeforeLastUserMessage and messages changed; the next
	// updateLastAgentMessageLocked pass must recompute the diff.
	c.fmtCacheValid = false
	c.lock.Unlock()
	return nil
}

// writeStabilize writes messageParts to the PTY and waits for
// the agent to process them. It operates in two phases:
//
// Phase 1 (echo detection): writes the message text and waits
// for the screen to change and stabilize. This detects agents
// that echo typed input. If the screen doesn't change within
// writeStabilizeEchoTimeout, this is non-fatal. Many TUI
// agents buffer bracketed-paste input without rendering it.
//
// Phase 2 (processing detection): writes a carriage return
// and waits for the screen to change, indicating the agent
// started processing. This phase is fatal on timeout: if the
// agent doesn't react to Enter, it's unresponsive.
func (c *PTYConversation) writeStabilize(ctx context.Context, messageParts ...MessagePart) error {
	// Compare stability regions rather than whole screens so output that
	// changes on its own (e.g. timers below the input box) is not mistaken
	// for the agent echoing input or reacting to the carriage return.
	readRegion := func() string {
		return c.cfg.StabilityScreen(c.cfg.AgentIO.ReadScreen())
	}
	screenBeforeMessage := readRegion()
	for _, part := range messageParts {
		if err := part.Do(c.cfg.AgentIO); err != nil {
			return fmt.Errorf("failed to write message part: %w", err)
		}
	}
	// Phase 1: wait for the screen to stabilize after the
	// message is written (echo detection).
	if err := util.WaitFor(ctx, util.WaitTimeout{
		Timeout:     writeStabilizeEchoTimeout,
		MinInterval: 50 * time.Millisecond,
		InitialWait: true,
		Clock:       c.cfg.Clock,
	}, func() (bool, error) {
		screen := readRegion()
		if screen != screenBeforeMessage {
			stabilityTimer := c.cfg.Clock.NewTimer(1 * time.Second)
			select {
			case <-ctx.Done():
				stabilityTimer.Stop()
				return false, ctx.Err()
			case <-stabilityTimer.C:
			}
			stabilityTimer.Stop()
			newScreen := readRegion()
			return newScreen == screen, nil
		}
		return false, nil
	}); err != nil {
		if !errors.Is(err, util.WaitTimedOut) {
			// Context cancellation or condition errors are fatal.
			return fmt.Errorf("failed to wait for screen to stabilize: %w", err)
		}
		// Phase 1 timeout is non-fatal: the agent may not echo
		// input (e.g. TUI agents buffer bracketed-paste content
		// internally). Proceed to Phase 2 to send the carriage
		// return.
		c.cfg.Logger.Info(
			"echo detection timed out, sending carriage return",
			"timeout", writeStabilizeEchoTimeout,
		)
	}

	// Phase 2: wait for the screen to change after the
	// carriage return is written (processing detection).
	screenBeforeCarriageReturn := readRegion()
	lastCarriageReturnTime := time.Time{}
	if err := util.WaitFor(ctx, util.WaitTimeout{
		Timeout:     writeStabilizeProcessTimeout,
		MinInterval: 25 * time.Millisecond,
		Clock:       c.cfg.Clock,
	}, func() (bool, error) {
		// we don't want to spam additional carriage returns because the agent may process them
		// (aider does this), but we do want to retry sending one if nothing's
		// happening for a while
		if c.cfg.Clock.Since(lastCarriageReturnTime) >= 3*time.Second {
			lastCarriageReturnTime = c.cfg.Clock.Now()
			if _, err := c.cfg.AgentIO.Write([]byte("\r")); err != nil {
				return false, fmt.Errorf("failed to write carriage return: %w", err)
			}
		}
		crTimer := c.cfg.Clock.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			crTimer.Stop()
			return false, ctx.Err()
		case <-crTimer.C:
		}
		crTimer.Stop()
		screen := readRegion()

		return screen != screenBeforeCarriageReturn, nil
	}); err != nil {
		if errors.Is(err, util.WaitTimedOut) {
			return fmt.Errorf("failed to wait for processing to start: %w", ErrMessageNotSubmitted)
		}
		return fmt.Errorf("failed to wait for processing to start: %w", err)
	}

	return nil
}

func (c *PTYConversation) Status() ConversationStatus {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.statusLocked()
}

// isScreenStableLocked returns true if the screen content has been stable
// for the required number of snapshots. Caller MUST hold c.lock.
// Equivalent to "buffer is full and all snapshots are identical", computed
// incrementally via stableCount instead of comparing every snapshot.
func (c *PTYConversation) isScreenStableLocked() bool {
	return c.snapshotBuffer.Len() >= c.stableSnapshotsThreshold &&
		c.stableCount >= c.stableSnapshotsThreshold
}

// caller MUST hold c.lock
func (c *PTYConversation) statusLocked() ConversationStatus {
	// sanity checks
	if c.snapshotBuffer.Capacity() != c.stableSnapshotsThreshold {
		panic(fmt.Sprintf("snapshot buffer capacity %d is not equal to snapshot threshold %d. can't check stability", c.snapshotBuffer.Capacity(), c.stableSnapshotsThreshold))
	}
	if c.stableSnapshotsThreshold == 0 {
		panic("stable snapshots threshold is 0. can't check stability")
	}

	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == ConversationRoleUser {
		// if the last message is a user message then the snapshot loop hasn't
		// been triggered since the last user message, and we should assume
		// the screen is changing
		return ConversationStatusChanging
	}

	if c.snapshotBuffer.Len() != c.stableSnapshotsThreshold {
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
	if len(c.outboundQueue) > 0 || c.sendingMessage {
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

func (c *PTYConversation) Text() string {
	c.lock.Lock()
	defer c.lock.Unlock()

	snapshots := c.snapshotBuffer.GetAll()
	if len(snapshots) == 0 {
		return ""
	}
	return snapshots[len(snapshots)-1].screen
}

func (c *PTYConversation) SaveState() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	stateFile := c.cfg.StatePersistenceConfig.StateFile
	saveState := c.cfg.StatePersistenceConfig.SaveState

	if !saveState {
		c.cfg.Logger.Info("State persistence is disabled")
		return nil
	}

	// Skip if not dirty
	if !c.dirty {
		c.cfg.Logger.Info("Skipping state save: no changes since last save")
		return nil
	}

	conversation := c.messagesLocked()

	// Serialize initial prompt from message parts
	var initialPromptStr string
	if len(c.cfg.InitialPrompt) > 0 {
		initialPromptStr = buildStringFromMessageParts(c.cfg.InitialPrompt)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Use atomic write: write to temp file, then rename to target path
	tempFile := stateFile + ".tmp"
	f, err := os.OpenFile(tempFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create temp state file: %w", err)
	}

	// Clean up temp file on error (before successful rename)
	var renamed bool
	defer func() {
		if !renamed {
			if removeErr := os.Remove(tempFile); removeErr != nil && !os.IsNotExist(removeErr) {
				c.cfg.Logger.Warn("Failed to clean up temp state file", "path", tempFile, "err", removeErr)
			}
		}
	}()

	// Encode directly to file to avoid loading entire JSON into memory
	encoder := json.NewEncoder(f)
	if err := encoder.Encode(AgentState{
		Version:           1,
		Messages:          conversation,
		InitialPrompt:     initialPromptStr,
		InitialPromptSent: c.initialPromptSent,
	}); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to encode state: %w", err)
	}

	// Flush to disk before rename for crash safety
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to sync state file: %w", err)
	}

	// Close file before rename
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFile, stateFile); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	renamed = true

	// Clear dirty flag after successful save
	c.dirty = false

	c.cfg.Logger.Info("State saved successfully", "path", stateFile)

	return nil
}

// loadStateLocked loads the state, this method assumes that caller holds the Lock.
// Returns (error, shouldEmit) where shouldEmit indicates if the error should be emitted to the user.
func (c *PTYConversation) loadStateLocked() (error, bool) {
	stateFile := c.cfg.StatePersistenceConfig.StateFile
	loadState := c.cfg.StatePersistenceConfig.LoadState

	if !loadState || c.loadStateStatus != LoadStatePending {
		return nil, false
	}

	// Check if file exists
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		c.cfg.Logger.Info("No previous state to load (file does not exist)", "path", stateFile)
		return fmt.Errorf("No previous state to load (file does not exist)"), false
	}

	// Open state file
	f, err := os.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err), true
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			c.cfg.Logger.Warn("Failed to close state file", "path", stateFile, "err", closeErr)
		}
	}()

	var agentState AgentState
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&agentState); err != nil {
		return fmt.Errorf("failed to unmarshal state (corrupted or invalid JSON): %w", err), true
	}

	// Validate version
	if agentState.Version != 1 {
		return fmt.Errorf("unsupported state file version %d (expected 1)", agentState.Version), true
	}

	// Handle initial prompt restoration:
	// - If a new initial prompt was provided via flags, check if it differs from the saved one.
	//   If different, mark as not sent (will be sent). If same, preserve sent status.
	// - If no new prompt provided, restore the saved prompt and its sent status.
	c.initialPromptSent = agentState.InitialPromptSent
	if len(c.cfg.InitialPrompt) > 0 {
		isDifferent := buildStringFromMessageParts(c.cfg.InitialPrompt) != agentState.InitialPrompt
		if isDifferent {
			c.initialPromptSent = false
		}
		// If same prompt, keep agentState.InitialPromptSent
	} else if agentState.InitialPrompt != "" {
		c.cfg.InitialPrompt = []MessagePart{MessagePartText{
			Content: agentState.InitialPrompt,
			Alias:   "",
			Hidden:  false,
		}}
	}

	c.messages = agentState.Messages

	c.dirty = false

	c.cfg.Logger.Info("Successfully loaded state", "path", stateFile, "messages", len(c.messages))
	return nil, false
}

// Reset clears all conversation state back to the initial empty state.
func (c *PTYConversation) Reset() {
	defer c.wakeSnapshot()
	c.lock.Lock()
	defer c.lock.Unlock()

	c.messages = []ConversationMessage{
		{
			Message: "",
			Role:    ConversationRoleAgent,
			Time:    c.cfg.Clock.Now(),
		},
	}
	c.screenBeforeLastUserMessage = ""
	c.snapshotBuffer = NewRingBuffer[screenSnapshot](c.stableSnapshotsThreshold)
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

func (c *PTYConversation) wakeSnapshot() {
	select {
	case c.snapshotWake <- struct{}{}:
	default:
	}
}

// watchScreen uses an adaptive interval: the base SnapshotInterval (25ms)
// while the screen is settling toward stability, and a longer activeInterval
// (6× base) while the agent is actively streaming output. Once fully settled,
// it sleeps without a timer until PTY output, a queued send, or Reset wakes
// it. Buffering notifications coalesces bursts and keeps rendering bounded.
func (c *PTYConversation) watchScreen(ctx context.Context, source interface{ ScreenUpdates() <-chan struct{} }, snapshot func() error) {
	settleInterval := c.cfg.SnapshotInterval           // 25ms — for stability detection
	activeInterval := c.cfg.SnapshotInterval * 6        // 150ms — during streaming
	interval := settleInterval
	var prevScreen string

	for {
		timer := c.cfg.Clock.NewTimer(interval, "snapshot")
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Consume output already covered by the next snapshot. Output arriving
		// during or after the read remains pending, so no final update is lost.
		select {
		case <-source.ScreenUpdates():
		default:
		}
		select {
		case <-c.snapshotWake:
		default:
		}
		if err := snapshot(); err != nil {
			return
		}
		c.lock.Lock()
		curScreen := c.lastSnapshotScreen
		settled := c.isScreenStableLocked() && !c.sendingMessage && !c.writingMessage && len(c.outboundQueue) == 0
		c.lock.Unlock()

		if settled {
			prevScreen = curScreen
			// Park until something happens.
			select {
			case <-ctx.Done():
				return
			case <-source.ScreenUpdates():
			case <-c.snapshotWake:
			}
			interval = settleInterval // just woke — sample fast
		} else if curScreen != prevScreen {
			// Screen is actively changing — no point sampling at full rate.
			interval = activeInterval
		} else {
			// Screen stopped changing but stableCount hasn't reached
			// threshold yet — keep the fast cadence for quick detection.
			interval = settleInterval
		}
		prevScreen = curScreen
	}
}
