package discordbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/coder/agentapi/lib/handoff"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func workflowEnvironment() *testsuite.TestWorkflowEnvironment {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	a := &Activities{}
	env.RegisterActivity(a.DiscordNotify)
	env.RegisterActivity(a.DiscordFeedback)
	env.RegisterActivity(a.AgentReply)
	env.RegisterActivity(a.AgentPending)
	return env
}

func TestHumanReplySignalResumesAgent(t *testing.T) {
	env := workflowEnvironment()
	request := handoff.Request{ID: strings.Repeat("a", 64), Content: "What next?", Kind: "message"}
	reply := handoff.Reply{RequestID: request.ID, ID: "discord-1", Content: "continue"}
	env.OnActivity("DiscordNotify", mock.Anything, request).Return("message-1", nil).Once()
	env.OnActivity("AgentReply", mock.Anything, reply).Return(handoff.Result{Outcome: "applied"}, nil).Once()
	env.OnActivity("DiscordFeedback", mock.Anything, FeedbackInput{"message-1", "applied"}).Return(nil).Once()
	// Signals may arrive while notification delivery is still in progress.
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(ReplySignal, reply) }, 0)
	env.ExecuteWorkflow(HumanReplyWorkflow, WorkflowInput{Request: request})
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHumanReplyBrowserResolutionClosesWait(t *testing.T) {
	env := workflowEnvironment()
	request := handoff.Request{ID: "request"}
	env.OnActivity("AgentPending", mock.Anything, request.ID).Return(false, nil).Once()
	env.OnActivity("DiscordFeedback", mock.Anything, FeedbackInput{"existing-message", "superseded"}).Return(nil).Once()
	// A resumed workflow retains its existing Discord message.
	env.ExecuteWorkflow(HumanReplyWorkflow, WorkflowInput{Request: request, MessageID: "existing-message"})
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHumanReplyInvalidAnswerKeepsWaiting(t *testing.T) {
	env := workflowEnvironment()
	request := handoff.Request{ID: "request", Kind: "terminal"}
	invalid := handoff.Reply{RequestID: request.ID, ID: "one", Content: "yes"}
	valid := handoff.Reply{RequestID: request.ID, ID: "two", Content: "1"}
	env.OnActivity("AgentReply", mock.Anything, invalid).Return(handoff.Result{Outcome: "invalid"}, nil).Once()
	env.OnActivity("AgentReply", mock.Anything, valid).Return(handoff.Result{Outcome: "applied"}, nil).Once()
	env.OnActivity("DiscordFeedback", mock.Anything, FeedbackInput{"message", "invalid"}).Return(nil).Once()
	env.OnActivity("DiscordFeedback", mock.Anything, FeedbackInput{"message", "applied"}).Return(nil).Once()
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(ReplySignal, invalid) }, time.Second)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(ReplySignal, valid) }, 2*time.Second)
	env.ExecuteWorkflow(HumanReplyWorkflow, WorkflowInput{Request: request, MessageID: "message"})
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestAgentReplyActivityUsesAuthenticatedHandoffProtocol(t *testing.T) {
	reply := handoff.Reply{RequestID: "request", ID: "discord-message", Content: "continue"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/handoff/reply", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		var received handoff.Reply
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		require.Equal(t, reply, received)
		_, _ = w.Write([]byte(`{"outcome":"applied"}`))
	}))
	defer server.Close()
	a := &Activities{AgentURL: server.URL, AgentToken: "test-token", HTTP: server.Client()}
	result, err := a.AgentReply(context.Background(), reply)
	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
}

func TestWorkflowReferenceRequiresOurBotAndValidID(t *testing.T) {
	id := workflowPrefix + strings.Repeat("a", 64)
	message := &discordgo.Message{Author: &discordgo.User{ID: "our-bot"}, Embeds: []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: id}}}}
	require.Equal(t, id, workflowIDFromMessage(message, "our-bot"))
	require.Empty(t, workflowIDFromMessage(message, "other-bot"))
	message.Embeds[0].Footer.Text = workflowPrefix + strings.Repeat("z", 64)
	require.Empty(t, workflowIDFromMessage(message, "our-bot"))
	require.Empty(t, workflowIDFromMessage(nil, "our-bot"))
}

func TestNotificationRetainsQuestionAtEndOfLongOutput(t *testing.T) {
	text := strings.Repeat("😀", 4000) + "\n1. Continue\n2. Cancel"
	excerpt := notificationExcerpt(text)
	require.LessOrEqual(t, len([]rune(excerpt)), 1700)
	require.True(t, strings.HasSuffix(excerpt, "1. Continue\n2. Cancel"))
}

func TestBridgeRequiresExplicitReplyUsersAndDedicatedQueue(t *testing.T) {
	c := Config{AgentURL: "http://localhost:3284", BotToken: "token", ChannelID: "channel", TemporalAddress: "localhost:7233", Namespace: "default"}
	require.ErrorContains(t, c.validate(), "allowed Discord user")
	c.AllowedUsers = []string{" person ", ""}
	require.NoError(t, c.validate())
	require.Equal(t, []string{"person"}, c.AllowedUsers)
	originalQueue := c.TaskQueue
	c.AgentURL, c.TaskQueue = "http://localhost:3285", ""
	require.NoError(t, c.validate())
	require.NotEqual(t, originalQueue, c.TaskQueue)
}
