package acpio

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/quartz"
)

// Compile-time assertion that ACPConversation implements st.Conversation
var _ st.Conversation = (*ACPConversation)(nil)

// ChunkableAgentIO extends AgentIO with chunk callback support for streaming responses.
// This interface is what ACPConversation needs from its AgentIO implementation.
type ChunkableAgentIO interface {
	st.AgentIO
	SetOnChunk(fn func(chunk string))
}

// ACPConversation tracks conversations with ACP-based agents.
// Unlike PTY-based Conversation, ACP has blocking writes where the
// response is complete when Write() returns. Send runs the write in the
// background, so a turn doesn't hold up its caller.
type ACPConversation struct {
	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
	agentIO           ChunkableAgentIO
	messages          []st.ConversationMessage
	nextID            int           // monotonically increasing message ID
	prompting         bool          // true while agent is processing
	chunkReceived     chan struct{} // signals that handleChunk has accumulated a chunk
	streamingResponse strings.Builder
	logger            *slog.Logger
	emitter           st.Emitter
	initialPrompt     []st.MessagePart
	clock             quartz.Clock
}

// NewACPConversation creates a new ACPConversation.
// If emitter is provided, it will receive events when messages/status/screen change.
// If clock is nil, a real clock will be used.
func NewACPConversation(ctx context.Context, agentIO ChunkableAgentIO, logger *slog.Logger, initialPrompt []st.MessagePart, emitter st.Emitter, clock quartz.Clock) *ACPConversation {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = quartz.NewReal()
	}
	if emitter == nil {
		emitter = st.NopEmitter{}
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &ACPConversation{
		ctx:           ctx,
		cancel:        cancel,
		agentIO:       agentIO,
		logger:        logger,
		initialPrompt: initialPrompt,
		emitter:       emitter,
		clock:         clock,
		chunkReceived: make(chan struct{}, 1),
	}
	return c
}

// Messages returns the conversation history.
func (c *ACPConversation) Messages() []st.ConversationMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.messages)
}

// Send starts a turn with the message and returns without waiting for the
// reply, like a native turn: the reply streams into the conversation, and a
// failed prompt is reported with EmitError. It returns a validation error
// if the message is invalid or another message is already being processed.
func (c *ACPConversation) Send(messageParts ...st.MessagePart) error {
	message := ""
	for _, part := range messageParts {
		message += part.String()
	}

	// Validate whitespace BEFORE trimming (match PTY behavior)
	if message != strings.TrimSpace(message) {
		return st.ErrMessageValidationWhitespace
	}

	if message == "" {
		return st.ErrMessageValidationEmpty
	}

	// Check if already prompting and set state atomically
	c.mu.Lock()
	if c.prompting {
		c.mu.Unlock()
		return st.ErrMessageValidationChanging
	}
	c.messages = append(c.messages, st.ConversationMessage{
		Id:      c.nextID,
		Role:    st.ConversationRoleUser,
		Message: message,
		Time:    c.clock.Now(),
	})
	c.nextID++
	// Add placeholder for streaming agent response
	c.messages = append(c.messages, st.ConversationMessage{
		Id:      c.nextID,
		Role:    st.ConversationRoleAgent,
		Message: "",
		Time:    c.clock.Now(),
	})
	c.nextID++
	c.streamingResponse.Reset()
	c.prompting = true
	messages := slices.Clone(c.messages)
	status := c.statusLocked()
	c.mu.Unlock()

	// The user's message shows, and the status turns to "running", before
	// the reply's first chunk.
	c.emitter.EmitMessages(messages)
	c.emitter.EmitStatus(status)

	c.logger.Debug("ACPConversation sending message", "message", message)

	go c.executePrompt(message)
	return nil
}

// Start sets up chunk handling and sends the initial prompt if provided.
func (c *ACPConversation) Start(ctx context.Context) {
	// Wire up the chunk callback for streaming
	c.agentIO.SetOnChunk(c.handleChunk)

	// Send initial prompt if provided
	if len(c.initialPrompt) > 0 {
		err := c.Send(c.initialPrompt...)
		if err == nil {
			return
		}
		c.logger.Error("ACPConversation failed to send initial prompt", "error", err)
		c.emitter.EmitError(fmt.Sprintf("Failed to send the initial prompt: %v", err), st.ErrorLevelError)
	}
	// No prompt under way means we start in stable state
	c.emitter.EmitStatus(c.Status())
}

// Status returns the current conversation status.
func (c *ACPConversation) Status() st.ConversationStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

// statusLocked returns the status without acquiring the lock (caller must hold lock).
func (c *ACPConversation) statusLocked() st.ConversationStatus {
	if c.prompting {
		return st.ConversationStatusChanging // agent is processing
	}
	return st.ConversationStatusStable
}

// Stop cancels any in-progress operations.
func (c *ACPConversation) Stop() {
	c.cancel()
}

// Text returns the current streaming response text.
func (c *ACPConversation) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streamingResponse.String()
}

// handleChunk is called for each streaming chunk from the agent.
func (c *ACPConversation) handleChunk(chunk string) {
	c.mu.Lock()
	// Log and discard chunks that arrive after the prompt has completed or errored.
	// This should not happen under normal operation — if it does, it indicates a
	// bug in the ACP SDK or a race in the connection teardown.
	if !c.prompting {
		c.mu.Unlock()
		c.logger.Error("received chunk while not prompting (late/unexpected chunk discarded)",
			"chunkLen", len(chunk))
		return
	}
	c.streamingResponse.WriteString(chunk)
	// Only update the last message if it's the agent placeholder (defense-in-depth)
	if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == st.ConversationRoleAgent {
		c.messages[len(c.messages)-1].Message = c.streamingResponse.String()
	}
	messages := slices.Clone(c.messages)
	status := c.statusLocked()
	screen := c.streamingResponse.String()
	c.mu.Unlock()

	// Signal that a chunk has been received (non-blocking; a pending signal is sufficient).
	select {
	case c.chunkReceived <- struct{}{}:
	default:
	}

	c.emitter.EmitMessages(messages)
	c.emitter.EmitStatus(status)
	c.emitter.EmitScreen(screen)
}

// executePrompt sends message to the agent as one prompt, waits for the
// turn to end and finalizes the reply. A failed prompt drops the partial
// reply and is reported with EmitError.
func (c *ACPConversation) executePrompt(message string) {
	// Drain any stale signal before sending the prompt.
	select {
	case <-c.chunkReceived:
	default:
	}

	// One prompt per message, with its visible text: the hidden parts are
	// keystrokes for a terminal (bracketed paste, Codex's composer
	// clearing), not text for the agent.
	promptErr := c.ctx.Err()
	if promptErr == nil {
		_, promptErr = c.agentIO.Write([]byte(message))
	}

	// The ACP SDK dispatches SessionUpdate notifications as goroutines, so
	// the chunk may arrive after conn.Prompt() returns. Wait up to 100ms.
	timer := c.clock.NewTimer(100 * time.Millisecond)
	select {
	case <-c.chunkReceived:
	case <-timer.C:
	}
	timer.Stop()

	c.mu.Lock()
	c.prompting = false

	if promptErr != nil {
		c.logger.Error("ACPConversation message failed", "error", promptErr)
		// Remove the agent's streaming message on error (may be empty or partial)
		if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == st.ConversationRoleAgent {
			c.messages = c.messages[:len(c.messages)-1]
		}
	} else {
		// Final response should already be in the last message via streaming
		// but ensure it's finalized.
		response := c.streamingResponse.String()
		if len(c.messages) > 0 && c.messages[len(c.messages)-1].Role == st.ConversationRoleAgent {
			c.messages[len(c.messages)-1].Message = response
		}
		c.logger.Debug("ACPConversation message complete", "responseLen", len(response))
	}

	// Snapshot state and emit once regardless of success/failure.
	messages := slices.Clone(c.messages)
	status := c.statusLocked()
	screen := c.streamingResponse.String()
	c.mu.Unlock()

	c.emitter.EmitMessages(messages)
	c.emitter.EmitStatus(status)
	c.emitter.EmitScreen(screen)

	if promptErr != nil && c.ctx.Err() == nil {
		c.emitter.EmitError(fmt.Sprintf("The agent failed to answer: %v", promptErr), st.ErrorLevelError)
	}
}

func (c *ACPConversation) SaveState() error {
	return fmt.Errorf("ACP mode doesn't support state persistence")
}

// Reset is a no-op for ACP conversations (restart is not supported).
func (c *ACPConversation) Reset() {}
