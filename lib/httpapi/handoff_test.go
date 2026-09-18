package httpapi

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/coder/agentapi/lib/handoff"
	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/stretchr/testify/require"
)

type handoffConversation struct {
	status st.ConversationStatus
	sends  int
}

func (c *handoffConversation) Messages() []st.ConversationMessage { return nil }
func (c *handoffConversation) Send(...st.MessagePart) error       { c.sends++; return nil }
func (c *handoffConversation) Start(context.Context)              {}
func (c *handoffConversation) Status() st.ConversationStatus      { return c.status }
func (c *handoffConversation) Text() string                       { return "" }
func (c *handoffConversation) SaveState() error                   { return nil }
func (c *handoffConversation) Reset()                             {}

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
		transport: TransportPTY, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, c, i
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
	require.Equal(t, []string{"2\r"}, terminal.writes)
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
