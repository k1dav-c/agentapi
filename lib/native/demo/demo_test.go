package demo_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/quartz"
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
	logins   []*native.LoginRequired
	usage    []native.AgentUsage
}

func (o *observer) Usage(u native.AgentUsage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.usage = append(o.usage, u)
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
func (o *observer) LoginRequired(l *native.LoginRequired) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.logins = append(o.logins, l)
}

func (o *observer) RequestUpdated(s native.RequestState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests[s.ID] = s
}

func (o *observer) Settings(native.Settings)       {}
func (o *observer) SettingsChosen(native.Settings) {}

// replies returns the replies to the nth turn of each agent; every agent
// counts its turns from one.
func (o *observer) replies(n int) []jsonlwatcher.RichMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []jsonlwatcher.RichMessage
	for key, m := range o.rich {
		if strings.HasSuffix(key, fmt.Sprintf("-%d-reply/assistant", n)) {
			out = append(out, m)
		}
	}
	return out
}

// reply returns the reply to the nth turn of the only agent.
func (o *observer) reply(n int) jsonlwatcher.RichMessage {
	if replies := o.replies(n); len(replies) == 1 {
		return replies[0]
	}
	return jsonlwatcher.RichMessage{}
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

// start runs the demo under a conversation with clock (nil for the real
// one) and waits until it accepts turns.
func start(t *testing.T, clock quartz.Clock) (*native.Conversation, *observer) {
	t.Helper()
	obs := &observer{rich: map[string]jsonlwatcher.RichMessage{}, requests: map[string]native.RequestState{}}
	c := native.NewConversation(native.Config{
		Factory:  demo.Factory(demo.Options{Step: time.Millisecond}),
		Observer: obs,
		Clock:    clock,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
	})
	c.Start(ctx)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true, Settings: true, Compact: true}, c.Capabilities())
	return c, obs
}

func pending(t *testing.T, c *native.Conversation) native.RequestState {
	t.Helper()
	require.Eventually(t, func() bool { return len(c.PendingRequests()) == 1 }, 5*time.Second, time.Millisecond)
	return c.PendingRequests()[0]
}

func TestTurn(t *testing.T) {
	c, obs := start(t, nil)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)

	reply := obs.reply(1)
	assert.Equal(t, native.StopReasonEndTurn, reply.StopReason)
	assert.Contains(t, reply.Content[0].Text, "Listed the workspace")
	require.Len(t, obs.toolResults(), 1)
	msgs := c.Messages()
	require.Len(t, msgs, 2)
	assert.Equal(t, st.ConversationRoleAgent, msgs[1].Role)
	assert.Contains(t, msgs[1].Message, "native demo agent")
}

func TestApprovalDenyWithFeedback(t *testing.T) {
	c, obs := start(t, nil)
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
	c, obs := start(t, nil)
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
	c, obs := start(t, nil)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "a long one"}))
	require.Eventually(t, func() bool { return obs.reply(1).MessageID != "" }, 5*time.Second, time.Millisecond)
	require.NoError(t, c.Steer(context.Background(), native.Input{Text: "use tabs"}))
	require.Eventually(t, func() bool {
		return strings.Contains(obs.reply(1).Content[0].Text, "Steered: use tabs")
	}, 5*time.Second, time.Millisecond)

	require.NoError(t, c.Interrupt(context.Background()))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, native.StopReasonInterrupted, obs.reply(1).StopReason)
	assert.ErrorIs(t, c.Interrupt(context.Background()), native.ErrNoActiveTurn)
}

func TestInterruptExpiresRequests(t *testing.T) {
	c, obs := start(t, nil)
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

func TestLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clock := quartz.NewMock(t)
	retry := clock.Trap().NewTimer("Conversation", "loginRetry")
	defer retry.Close()
	c, obs := start(t, clock)

	require.NoError(t, c.StartTurn(ctx, native.Input{Text: "please approve this"}))
	req := pending(t, c)
	_, err := c.Respond(ctx, req.ID, native.Response{OptionID: "allow"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	reply := c.Messages()[1].Message

	require.NoError(t, c.StartTurn(ctx, native.Input{Text: "pretend you need a login"}))
	require.Eventually(t, func() bool { return c.Idle() && c.LoginRequired() != nil }, 5*time.Second, time.Millisecond)
	assert.Equal(t, demo.LoginHint.Command, c.LoginRequired().Command)

	// Check again: the first start still has no login, the conversation
	// tries again by itself, and that start works.
	require.NoError(t, c.RetryLogin(ctx))
	retry.MustWait(ctx).Release()
	_, w := clock.AdvanceNext()
	w.MustWait(ctx)
	require.Eventually(t, func() bool { return c.Idle() && c.LoginRequired() == nil }, 5*time.Second, time.Millisecond)
	obs.mu.Lock()
	require.Len(t, obs.logins, 2, "the login is reported once, then that it's fixed")
	assert.Nil(t, obs.logins[1])
	obs.mu.Unlock()
	assert.Equal(t, "please approve this", c.Messages()[0].Message, "the conversation is kept")

	// The new agent's messages and requests don't take the place of the
	// first agent's.
	require.NoError(t, c.StartTurn(ctx, native.Input{Text: "approve that too"}))
	again := pending(t, c)
	assert.NotEqual(t, req.ID, again.ID)
	_, err = c.Respond(ctx, again.ID, native.Response{OptionID: "allow"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	msgs := c.Messages()
	require.Len(t, msgs, 5)
	assert.Equal(t, reply, msgs[1].Message)
	assert.Len(t, obs.replies(1), 2, "the first reply of each agent")
	obs.mu.Lock()
	assert.Equal(t, native.RequestStatusResolved, obs.requests[req.ID].Status)
	obs.mu.Unlock()
}

func TestSettings(t *testing.T) {
	c, _ := start(t, nil)
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeBypass, Model: "demo-swift", Effort: "medium"}, c.Settings())
	options, err := c.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, native.PermissionModeValues, options.PermissionModes)
	require.Len(t, options.Models, 3)

	// A model without the current effort gets its default one, a model
	// without efforts none.
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{Model: "demo-deep", Effort: "max"}))
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{Model: "demo-swift"}))
	assert.Equal(t, "medium", c.Settings().Effort)
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{Model: "demo-plain"}))
	assert.Empty(t, c.Settings().Effort)
	assert.ErrorIs(t, c.ApplySettings(context.Background(), native.Settings{Effort: "low"}), native.ErrInvalidSetting)
}

func TestAskModeAsksBeforeTheFirstCommand(t *testing.T) {
	c, _ := start(t, nil)
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk}))
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "hello"}))
	r := pending(t, c)
	assert.Equal(t, "Run ls", r.Title)
	_, err := c.Respond(context.Background(), r.ID, native.Response{OptionID: "allow"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
}

func TestPlanModeEndsInBypassOnceApproved(t *testing.T) {
	c, _ := start(t, nil)
	require.NoError(t, c.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModePlan}))
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "add a feature"}))
	r := pending(t, c)
	assert.Equal(t, native.RequestKindPlan, r.Kind)
	_, err := c.Respond(context.Background(), r.ID, native.Response{OptionID: "approve"})
	require.NoError(t, err)
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	assert.Equal(t, native.PermissionModeBypass, c.Settings().PermissionMode)
}

func TestUsage(t *testing.T) {
	c, obs := start(t, nil)
	for i := range 2 {
		require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: fmt.Sprint("turn ", i)}))
		require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Len(t, obs.usage, 2)
	assert.Less(t, obs.usage[0].Context.Used, obs.usage[1].Context.Used, "the context grows")
	assert.Equal(t, 200000, obs.usage[1].Context.Size)
	require.Len(t, obs.usage[1].RateLimits, 2)
	assert.Equal(t, "5-hour limit", obs.usage[1].RateLimits[0].Label)
	assert.Equal(t, c.Usage(), obs.usage[1])
}

func TestCompact(t *testing.T) {
	c, obs := start(t, nil)
	require.NoError(t, c.StartTurn(context.Background(), native.Input{Text: "long"}))
	assert.ErrorIs(t, c.Compact(context.Background()), native.ErrTurnActive)
	require.NoError(t, c.Interrupt(context.Background()))
	require.Eventually(t, c.Idle, 5*time.Second, time.Millisecond)
	before := c.Usage().Context.Used

	require.NoError(t, c.Compact(context.Background()))
	assert.Less(t, c.Usage().Context.Used, before, "the context empties")
	obs.mu.Lock()
	defer obs.mu.Unlock()
	assert.NotEmpty(t, obs.usage)
}
