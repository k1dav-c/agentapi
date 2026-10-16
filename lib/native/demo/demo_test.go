package demo_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/demo"
	st "github.com/coder/agentapi/lib/screentracker"
)

// observer records what the conversation reports.
type observer struct {
	mu       sync.Mutex
	rich     map[string]jsonlwatcher.RichMessage
	requests map[string]native.RequestState
}

func (o *observer) Started(native.Capabilities) {}
func (o *observer) RichMessage(m jsonlwatcher.RichMessage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rich[m.MessageID+"/"+m.Role] = m
}
func (o *observer) SessionEvents([]jsonlwatcher.SessionEvent) {}
func (o *observer) SessionName(string)                        {}
func (o *observer) SessionRef(string)                         {}
func (o *observer) RequestUpdated(s native.RequestState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests[s.ID] = s
}

func (o *observer) reply(n int) jsonlwatcher.RichMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rich[fmt.Sprintf("demo-%d-reply/assistant", n)]
}

func (o *observer) toolResults() []jsonlwatcher.RichContentBlock {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []jsonlwatcher.RichContentBlock
	for _, m := range o.rich {
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				out = append(out, b)
			}
		}
	}
	return out
}

func start(t *testing.T) (*native.Conversation, *observer) {
	t.Helper()
	obs := &observer{rich: map[string]jsonlwatcher.RichMessage{}, requests: map[string]native.RequestState{}}
	c := native.NewConversation(native.Config{
		Factory:  demo.Factory(demo.Options{Step: time.Millisecond}),
		Observer: obs,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
	})
	c.Start(ctx)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true}, c.Capabilities())
	return c, obs
}

func pending(t *testing.T, c *native.Conversation) native.RequestState {
	t.Helper()
	require.Eventually(t, func() bool { return len(c.PendingRequests()) == 1 }, 5*time.Second, time.Millisecond)
	return c.PendingRequests()[0]
}

func TestTurn(t *testing.T) {
	c, obs := start(t)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)

	reply := obs.reply(1)
	assert.Equal(t, "end_turn", reply.StopReason)
	assert.Contains(t, reply.Content[0].Text, "Listed the workspace")
	require.Len(t, obs.toolResults(), 1)
	msgs := c.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, st.ConversationRoleAgent, msgs[1].Role)
	assert.Contains(t, msgs[1].Message, "native demo agent")
}

func TestApprovalDenyWithFeedback(t *testing.T) {
	c, obs := start(t)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "please approve this"}))
	req := pending(t, c)
	assert.Equal(t, native.RequestKindApproval, req.Kind)
	assert.True(t, req.Feedback)
	assert.NotEmpty(t, req.ToolUseID)
	assert.Equal(t, st.ConversationStatusStable, c.Status())

	_, err := c.Respond(context.Background(), req.ID, native.Response{OptionID: "deny", Text: "use make build"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)

	var denied bool
	for _, r := range obs.toolResults() {
		if r.ToolUseID == req.ToolUseID {
			denied = r.IsError != nil && *r.IsError && strings.Contains(r.Text, "use make build")
		}
	}
	assert.True(t, denied, "the tool result reports the denial")
	assert.Contains(t, obs.reply(1).Content[0].Text, "denied")
}

func TestQuestionAndPlan(t *testing.T) {
	c, obs := start(t)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "ask me, then plan"}))
	q := pending(t, c)
	require.Equal(t, native.RequestKindQuestion, q.Kind)
	require.Len(t, q.Questions, 3)
	assert.True(t, q.Questions[1].MultiSelect)
	assert.True(t, q.Questions[2].FreeText)
	_, err := c.Respond(context.Background(), q.ID, native.Response{Answers: map[string][]string{
		"pm": {"bun"}, "checks": {"Lint", "Unit tests"}, "notes": {"none"},
	}})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		p := c.PendingRequests()
		return len(p) == 1 && p[0].Kind == native.RequestKindPlan
	}, 5*time.Second, time.Millisecond)
	plan := c.PendingRequests()[0]
	assert.Contains(t, plan.Detail, "## Plan")
	_, err = c.Respond(context.Background(), plan.ID, native.Response{OptionID: "approve"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	text := obs.reply(1).Content[0].Text
	assert.Contains(t, text, "Checks: Lint, Unit tests")
	assert.Contains(t, text, "approved the plan")
}

func TestSteerAndInterrupt(t *testing.T) {
	c, obs := start(t)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "a long one"}))
	require.Eventually(t, func() bool { return obs.reply(1).MessageID != "" }, 5*time.Second, time.Millisecond)
	require.NoError(t, c.Steer(context.Background(), native.Input{Text: "use tabs"}))
	require.Eventually(t, func() bool {
		return strings.Contains(obs.reply(1).Content[0].Text, "Steered: use tabs")
	}, 5*time.Second, time.Millisecond)

	require.NoError(t, c.Interrupt(context.Background()))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, "interrupted", obs.reply(1).StopReason)
	assert.ErrorIs(t, c.Interrupt(context.Background()), native.ErrNoActiveTurn)
}

func TestInterruptExpiresRequests(t *testing.T) {
	c, obs := start(t)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "ask"}))
	req := pending(t, c)
	require.NoError(t, c.Interrupt(context.Background()))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	obs.mu.Lock()
	state := obs.requests[req.ID]
	obs.mu.Unlock()
	assert.Equal(t, native.RequestStatusExpired, state.Status)
	assert.Equal(t, native.ResponseMessage, state.Capability)
	_, err := c.Respond(context.Background(), req.ID, native.Response{Answers: map[string][]string{"pm": {"bun"}}})
	assert.ErrorIs(t, err, native.ErrRequestNotFound)
}
