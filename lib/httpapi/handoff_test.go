package httpapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/coder/agentapi/lib/handoff"
	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/quartz"
	"github.com/stretchr/testify/require"
)

type handoffConversation struct {
	status st.ConversationStatus
	sends  int
	parts  [][]st.MessagePart
}

func (c *handoffConversation) Messages() []st.ConversationMessage { return nil }
func (c *handoffConversation) Send(p ...st.MessagePart) error {
	c.sends++
	c.parts = append(c.parts, p)
	return nil
}
func (c *handoffConversation) Start(context.Context)         {}
func (c *handoffConversation) Status() st.ConversationStatus { return c.status }
func (c *handoffConversation) Text() string                  { return "" }
func (c *handoffConversation) SaveState() error              { return nil }
func (c *handoffConversation) Reset()                        {}

type handoffIO struct {
	writes []string
	short  bool
}

func (i *handoffIO) Write(data []byte) (int, error) {
	i.writes = append(i.writes, string(data))
	if i.short {
		return 0, nil
	}
	return len(data), nil
}
func (i *handoffIO) ReadScreen() string { return "" }

func handoffServer() (*Server, *handoffConversation, *handoffIO) {
	c := &handoffConversation{status: st.ConversationStatusStable}
	i := &handoffIO{}
	e := NewEventEmitter(WithSessionID("session"), WithAgentType(mf.AgentTypeClaude))
	e.EmitStatus(st.ConversationStatusStable)
	e.EmitMessages([]st.ConversationMessage{
		{Id: 1, Role: st.ConversationRoleUser, Message: "work"},
		{Id: 2, Role: st.ConversationRoleAgent, Message: "What next?"},
	})
	return &Server{conversation: c, agentio: i, emitter: e, agentType: mf.AgentTypeClaude,
		transport: TransportPTY, clock: quartz.NewReal(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, c, i
}

func TestHandoffReplyDeduplicatesAndInvalidates(t *testing.T) {
	s, c, _ := handoffServer()
	pending, err := s.getPendingHandoff(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, pending.Body.Request)
	request := &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.Body.Request.ID, ID: "discord-1", Content: "continue"}}
	for range 2 {
		result, err := s.replyHandoff(t.Context(), request)
		require.NoError(t, err)
		require.Equal(t, "applied", result.Body.Outcome)
	}
	require.Equal(t, 1, c.sends)
	request.Body.ID = "discord-2"
	result, err := s.replyHandoff(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "superseded", result.Body.Outcome)
	pending, err = s.getPendingHandoff(t.Context(), nil)
	require.NoError(t, err)
	require.Nil(t, pending.Body.Request)
}

func TestHandoffBrowserReplyWins(t *testing.T) {
	s, c, _ := handoffServer()
	pending := s.pendingHandoffLocked()
	require.NotNil(t, pending)
	_, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "from browser"}})
	require.NoError(t, err)
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: "discord-1", Content: "stale"}})
	require.NoError(t, err)
	require.Equal(t, "superseded", result.Body.Outcome)
	require.Equal(t, 1, c.sends)
}

func TestHandoffRestartInvalidatesEvenIdenticalPrompt(t *testing.T) {
	s, c, _ := handoffServer()
	pending := s.pendingHandoffLocked()
	s.emitter.SetLifecycle(LifecycleRestarting)
	require.Nil(t, s.pendingHandoffLocked())
	s.emitter.EmitStatus(st.ConversationStatusStable)
	require.NotEqual(t, pending.ID, s.pendingHandoffLocked().ID)
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: "late", Content: "continue"}})
	require.NoError(t, err)
	require.Equal(t, "superseded", result.Body.Outcome)
	require.Zero(t, c.sends)
}

func TestHandoffTerminalReplyAndQueueGate(t *testing.T) {
	s, c, terminal := handoffServer()
	s.emitter.EmitScreen("Do you want to proceed?\n❯ 1. Yes\n  2. No\nEnter to confirm")
	s.dispatchNextQueuedMessage()
	require.False(t, s.terminalQuestionValid, "empty queues should not scan the terminal")
	s.messageQueue = []QueuedMessage{{ID: 1, Content: "later"}}
	pending := s.pendingHandoffLocked()
	require.Equal(t, "terminal", pending.Kind)
	s.dispatchNextQueuedMessage()
	require.Zero(t, c.sends)
	require.Len(t, s.messageQueue, 1)
	reply := &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: "reply", Content: "approve everything"}}
	result, err := s.replyHandoff(t.Context(), reply)
	require.NoError(t, err)
	require.Equal(t, "invalid", result.Body.Outcome)
	require.Empty(t, terminal.writes)
	reply.Body.Content = "2"
	result, err = s.replyHandoff(t.Context(), reply)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Body.Outcome)
	require.Equal(t, []string{"2"}, terminal.writes, "Claude selects on the digit; a trailing Enter could hit the next prompt")
}

func TestHandoffUncertainTerminalWriteIsNotRetried(t *testing.T) {
	s, _, terminal := handoffServer()
	s.emitter.EmitScreen("Do you want to proceed?\n❯ 1. Yes\nEnter to confirm")
	terminal.short = true
	reply := &HandoffReplyRequest{Body: handoff.Reply{RequestID: s.pendingHandoffLocked().ID, ID: "reply", Content: "1"}}
	for range 2 {
		result, err := s.replyHandoff(t.Context(), reply)
		require.NoError(t, err)
		require.Equal(t, "uncertain", result.Body.Outcome)
	}
	require.Len(t, terminal.writes, 1)
}

func TestHandoffIgnoresStartupRunningAndQueuedMessages(t *testing.T) {
	s, _, _ := handoffServer()
	s.emitter.Reset()
	require.Nil(t, s.pendingHandoffLocked())
	s, c, _ := handoffServer()
	c.status = st.ConversationStatusChanging
	require.Nil(t, s.pendingHandoffLocked())
	c.status = st.ConversationStatusStable
	s.messageQueue = []QueuedMessage{{ID: 1, Content: "queued"}}
	require.Nil(t, s.pendingHandoffLocked())
}

func TestHandoffMCPRestartInvalidatesRequest(t *testing.T) {
	s, c, _ := handoffServer()
	pending := s.pendingHandoffLocked()
	s.restartAgent = func(context.Context) (int, error) { return 0, nil }
	restarted, err := s.restartQuery(t.Context(), true)
	require.NoError(t, err)
	require.True(t, restarted)
	s.emitter.EmitStatus(st.ConversationStatusStable)
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: "late", Content: "continue"}})
	require.NoError(t, err)
	require.Equal(t, "superseded", result.Body.Outcome)
	require.Zero(t, c.sends)
}

func TestQueueNotBlockedByNumberedListInScrollback(t *testing.T) {
	s, c, _ := handoffServer()
	border := strings.Repeat("─", 40)
	// An earlier agent reply with a numbered list mentioning "option" is
	// still on screen, but Claude's input box shows it's waiting for input.
	s.emitter.EmitScreen(strings.Join([]string{
		"● Pick an option:",
		"  1. Continue with the refactor",
		"  2. Select a different approach",
		"",
		border,
		"❯",
		border,
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
	}, "\n"))
	s.messageQueue = []QueuedMessage{{ID: 1, Content: "next"}}
	s.dispatchNextQueuedMessage()
	require.Equal(t, 1, c.sends)
	require.Empty(t, s.messageQueue)
}

func TestQueueNotBlockedByQuestionFarUpInScrollback(t *testing.T) {
	s, c, _ := handoffServer()
	s.agentType = mf.AgentTypeAider
	lines := []string{"Do you want to proceed?", "❯ 1. Yes", "  2. No", "Enter to confirm"}
	for range terminalQuestionTailLines {
		lines = append(lines, "later output")
	}
	s.emitter.EmitScreen(strings.Join(lines, "\n"))
	s.messageQueue = []QueuedMessage{{ID: 1, Content: "next"}}
	s.dispatchNextQueuedMessage()
	require.Equal(t, 1, c.sends)
}

type notSubmittedConversation struct{ handoffConversation }

func (c *notSubmittedConversation) Send(...st.MessagePart) error {
	c.sends++
	return fmt.Errorf("failed to send message: %w", st.ErrMessageNotSubmitted)
}

func TestQueueDoesNotRetryUnsubmittedMessage(t *testing.T) {
	s, _, _ := handoffServer()
	c := &notSubmittedConversation{handoffConversation{status: st.ConversationStatusStable}}
	s.conversation = c
	s.messageQueue = []QueuedMessage{{ID: 1, Content: "next"}, {ID: 2, Content: "after"}}
	s.dispatchNextQueuedMessage()
	require.Equal(t, 1, c.sends)
	require.Equal(t, []QueuedMessage{{ID: 2, Content: "after"}}, s.messageQueue)
}

func readScreenFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return string(b)
}

func TestPlanFeedbackKey(t *testing.T) {
	key, ok := freeTextReplyKeys(mf.AgentTypeClaude, readScreenFixture(t, "claude_plan_dialog.txt"))
	require.True(t, ok)
	require.Equal(t, "3", key)

	_, ok = freeTextReplyKeys(mf.AgentTypeClaude, readScreenFixture(t, "claude_permission_dialog.txt"))
	require.False(t, ok, "permission prompts have no feedback field")
	_, ok = freeTextReplyKeys(mf.AgentTypeCodex, readScreenFixture(t, "claude_plan_dialog.txt"))
	require.False(t, ok)
	// The dialog text wraps "Would you like to / proceed?" across lines.
	require.True(t, isTerminalQuestion(readScreenFixture(t, "claude_plan_dialog.txt")))
}

func TestCreateMessageDuringPlanDialogSendsFeedback(t *testing.T) {
	s, c, _ := handoffServer()
	s.emitter.EmitScreen(readScreenFixture(t, "claude_plan_dialog.txt"))
	resp, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "use CCC instead"}})
	require.NoError(t, err)
	require.False(t, resp.Body.Queued)
	require.Equal(t, 1, c.sends)
	// The feedback option is selected before the text is pasted, so the
	// carriage return submits feedback instead of approving the plan.
	require.Equal(t, keyPressPart{keys: "3", delay: freeTextFocusDelay}, c.parts[0][0])
	require.Equal(t, "use CCC instead", c.parts[0][2].String())
}

func TestCreateMessageDuringPermissionPromptIsQueued(t *testing.T) {
	s, c, _ := handoffServer()
	s.emitter.EmitScreen(readScreenFixture(t, "claude_permission_dialog.txt"))
	resp, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "hello"}})
	require.NoError(t, err)
	require.True(t, resp.Body.Queued)
	require.Zero(t, c.sends, "typing into the prompt would approve the highlighted option")
	require.Len(t, s.messageQueue, 1)
}

func TestHandoffFreeTextOnPlanDialogIsFeedback(t *testing.T) {
	s, c, terminal := handoffServer()
	s.emitter.EmitScreen(readScreenFixture(t, "claude_plan_dialog.txt"))
	pending := s.pendingHandoffLocked()
	require.Equal(t, "terminal", pending.Kind)
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: "reply", Content: "use CCC instead"}})
	require.NoError(t, err)
	require.Equal(t, "applied", result.Body.Outcome)
	require.Empty(t, terminal.writes)
	require.Equal(t, 1, c.sends)
	require.Equal(t, keyPressPart{keys: "3", delay: freeTextFocusDelay}, c.parts[0][0])
}

func TestCodexPromptsQueueChatMessages(t *testing.T) {
	for _, fixture := range []string{"codex_plan_prompt.txt", "codex_approval_prompt.txt"} {
		t.Run(fixture, func(t *testing.T) {
			s, c, _ := handoffServer()
			s.agentType = mf.AgentTypeCodex
			screen := readScreenFixture(t, fixture)
			s.emitter.EmitScreen(screen)
			require.True(t, isTerminalQuestionScreen(mf.AgentTypeCodex, screen))
			_, ok := freeTextReplyKeys(mf.AgentTypeCodex, screen)
			require.False(t, ok, "approval and plan prompts have no free-text field")
			resp, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "hello"}})
			require.NoError(t, err)
			require.True(t, resp.Body.Queued)
			require.Zero(t, c.sends)
		})
	}
	// Codex selects on the digit; a trailing Enter would answer the next
	// question with its default.
	reply, ok := terminalReply(mf.AgentTypeCodex, readScreenFixture(t, "codex_plan_prompt.txt"), "3")
	require.True(t, ok)
	require.Equal(t, "3", reply)
	reply, ok = terminalReply(mf.AgentTypeAider, readScreenFixture(t, "codex_plan_prompt.txt"), "3")
	require.True(t, ok)
	require.Equal(t, "3\r", reply)
}

func TestCodexQuestionFreeTextReply(t *testing.T) {
	screen := readScreenFixture(t, "codex_question.txt")
	// The user message just above the dialog must not be mistaken for the
	// composer, or chat messages would be typed into the dialog.
	require.True(t, isTerminalQuestionScreen(mf.AgentTypeCodex, screen))
	keys, ok := freeTextReplyKeys(mf.AgentTypeCodex, screen)
	require.True(t, ok)
	require.Equal(t, "\x1b[B\x1b[B\x1b[B\t", keys, "arrow from option 1 to 4, then open notes")

	keys, ok = freeTextReplyKeys(mf.AgentTypeCodex, readScreenFixture(t, "codex_question_notes.txt"))
	require.True(t, ok)
	require.Empty(t, keys, "notes already open: paste directly")

	s, c, _ := handoffServer()
	s.agentType = mf.AgentTypeCodex
	s.emitter.EmitScreen(screen)
	resp, err := s.createMessage(t.Context(), &MessageRequest{Body: MessageRequestBody{Type: MessageTypeUser, Content: "fetch weather"}})
	require.NoError(t, err)
	require.False(t, resp.Body.Queued)
	require.Equal(t, keyPressPart{keys: "\x1b[B\x1b[B\x1b[B\t", delay: freeTextFocusDelay}, c.parts[0][0])
	require.Equal(t, "fetch weather", c.parts[0][2].String())
}

func TestFormatMessageClearsCodexComposer(t *testing.T) {
	parts := FormatMessage(mf.AgentTypeCodex, "  hello  ")
	require.Len(t, parts, 4)
	// A leftover draft (e.g. a follow-up Codex restored after an interrupt)
	// is cleared first; it is hidden from the recorded user message.
	require.Equal(t, st.MessagePartText{Content: clearCodexComposer, Hidden: true}, parts[0])
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.String())
	}
	require.Equal(t, "hello", text.String())

	// Claude keeps a plain paste.
	require.Len(t, FormatMessage(mf.AgentTypeClaude, "hello"), 3)

	// Replies into a Codex prompt's free-text field never send Backspace.
	reply := formatFreeTextReply(mf.AgentTypeCodex, "\t", "note")
	require.Len(t, reply, 4)
	require.Equal(t, "note", reply[2].String())
}
