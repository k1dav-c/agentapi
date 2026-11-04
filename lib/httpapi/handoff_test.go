package httpapi

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/handoff"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/transcript"
)

// pendingHandoff returns what the human reply protocol of s offers.
func pendingHandoff(s *Server) *handoff.Request {
	pending, _ := s.pendingHandoff()
	return pending
}

// The human reply protocol offers the agent's last message after the
// user's last one, whatever it holds.
func TestHandoffView(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		messages        []string
		question, reply string
	}{
		{"no message of the user", []string{"agent:hi"}, "", ""},
		{"no answer yet", []string{"agent:", "user:fix it"}, "fix it", ""},
		{"the last answer", []string{"user:first", "agent:old", "user:fix it", "agent:on it", "agent:done"}, "fix it", "done"},
		{"an empty last answer", []string{"user:fix it", "agent:done", "agent:"}, "fix it", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := convstate.NewEventEmitter()
			var msgs []conv.Message
			for i, m := range tc.messages {
				role, text, _ := strings.Cut(m, ":")
				msgs = append(msgs, conv.Message{Id: i, Role: conv.Role(role), Message: text})
			}
			e.EmitMessages(msgs)
			view := e.HandoffView()
			assert.Equal(t, tc.question, view.Question)
			assert.Equal(t, tc.reply, view.Reply)
		})
	}
}

// offerLastMessage has the native agent say text and end its turn with
// result, and returns what the human-reply protocol offers then: that
// message.
func offerLastMessage(t *testing.T, s *Server, a *nativetest.Adapter, text string, result native.TurnResult) *handoff.Request {
	t.Helper()
	a.Sink().RichMessage(transcript.RichMessage{MessageID: "m-" + text, Role: "assistant", Content: []transcript.RichContentBlock{{Type: "text", Text: text}}})
	a.Sink().TurnCompleted(result)
	pending := pendingHandoff(s)
	require.NotNil(t, pending)
	require.Equal(t, "message", pending.Kind)
	require.Equal(t, text, pending.Content)
	return pending
}

func replyToHandoff(t *testing.T, s *Server, pending *handoff.Request, id, content string) string {
	t.Helper()
	result, err := s.replyHandoff(t.Context(), &HandoffReplyRequest{Body: handoff.Reply{RequestID: pending.ID, ID: id, Content: content}})
	require.NoError(t, err)
	return result.Body.Outcome
}

func turnTexts(a *nativetest.Adapter) []string {
	var texts []string
	for _, turn := range a.Turns() {
		texts = append(texts, turn.Text)
	}
	return texts
}

// With the native transport a reply to the agent's last message goes as a
// message the user sends: at once while the agent is idle, also past a
// usage limit that holds the queue (it may be what continues the work),
// and into the queue while the agent works, where a limit stop holds it.
func TestNativeHandoffReplyGoesAsAMessage(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		s, factory := newQuietNativeServer(t, nil)
		a := factory.Last()
		sendUserMessage(t, s, "Check the build")
		pending := offerLastMessage(t, s, a, "Shall I deploy?", native.TurnResult{StopReason: native.StopReasonEndTurn})
		assert.Equal(t, "applied", replyToHandoff(t, s, pending, "reply-1", "Yes"))
		assert.Equal(t, []string{"Check the build", "Yes"}, turnTexts(a))
	})

	t.Run("limit-held", func(t *testing.T) {
		s, factory := newQuietNativeServer(t, nil)
		a := factory.Last()
		sendUserMessage(t, s, "Check the build")
		resets := time.Now().Add(time.Hour)
		pending := offerLastMessage(t, s, a, "You've hit your limit", native.TurnResult{StopReason: native.StopReasonLimited, Error: "You've hit your limit", ResetsAt: &resets})
		require.True(t, s.native.HoldsQueue(time.Now()))
		assert.Equal(t, "applied", replyToHandoff(t, s, pending, "reply-1", "Go on"))
		assert.Equal(t, []string{"Check the build", "Go on"}, turnTexts(a))
		assert.Nil(t, s.native.LimitStop())
	})

	t.Run("busy", func(t *testing.T) {
		s, factory := newQuietNativeServer(t, nil)
		a := factory.Last()
		sendUserMessage(t, s, "Check the build")
		pending := offerLastMessage(t, s, a, "Shall I deploy?", native.TurnResult{StopReason: native.StopReasonEndTurn})
		// The agent started a turn of its own meanwhile, and says so.
		a.TurnErr = native.ErrTurnActive
		assert.Equal(t, "queued", replyToHandoff(t, s, pending, "reply-1", "Yes"))
		assert.Equal(t, "queued", replyToHandoff(t, s, pending, "reply-1", "Yes"), "answered from the receipt")
		require.Len(t, s.queue.Messages(), 1)
		a.TurnErr = nil

		// That turn stops on a usage limit, which holds the queue.
		resets := time.Now().Add(time.Hour)
		a.Sink().TurnStarted()
		a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonLimited, Error: "You've hit your limit", ResetsAt: &resets})
		s.dispatchNextQueuedMessage()
		assert.Equal(t, []string{"Check the build"}, turnTexts(a))
		require.NoError(t, s.native.DismissLimitStop())
		s.dispatchNextQueuedMessage()
		assert.Equal(t, []string{"Check the build", "Yes"}, turnTexts(a))
		assert.Empty(t, s.queue.Messages())
	})
}

// offeringServer is a server whose agent answered "Check the build" with
// "Shall I deploy?", which the human-reply protocol offers.
func offeringServer(t *testing.T) (*Server, *nativetest.Adapter, *handoff.Request) {
	t.Helper()
	s, factory := newQuietNativeServer(t, nil)
	a := factory.Last()
	sendUserMessage(t, s, "Check the build")
	return s, a, offerLastMessage(t, s, a, "Shall I deploy?", native.TurnResult{StopReason: native.StopReasonEndTurn})
}

func TestHandoffReplyDeduplicatesAndInvalidates(t *testing.T) {
	s, a, pending := offeringServer(t)
	for range 2 {
		assert.Equal(t, "applied", replyToHandoff(t, s, pending, "reply-1", "continue"))
	}
	assert.Equal(t, []string{"Check the build", "continue"}, turnTexts(a))
	assert.Equal(t, "superseded", replyToHandoff(t, s, pending, "reply-2", "continue"))
	assert.Nil(t, pendingHandoff(s))
}

func TestHandoffBrowserReplyWins(t *testing.T) {
	s, a, pending := offeringServer(t)
	sendUserMessage(t, s, "from browser")
	assert.Equal(t, "superseded", replyToHandoff(t, s, pending, "reply-1", "stale"))
	assert.Equal(t, []string{"Check the build", "from browser"}, turnTexts(a))
}

func TestHandoffRestartInvalidatesEvenIdenticalPrompt(t *testing.T) {
	s, a, pending := offeringServer(t)
	s.emitter.SetLifecycle(LifecycleRestarting)
	require.Nil(t, pendingHandoff(s))
	s.emitter.EmitStatus(conv.StatusStable)
	require.Nil(t, pendingHandoff(s), "restarting until the restart is done")
	s.emitter.ReleaseLifecycle()
	require.NotEqual(t, pending.ID, pendingHandoff(s).ID)
	assert.Equal(t, "superseded", replyToHandoff(t, s, pending, "late", "continue"))
	assert.Equal(t, []string{"Check the build"}, turnTexts(a))
}

func TestHandoffIgnoresStartupRunningAndQueuedMessages(t *testing.T) {
	s, _ := newQuietNativeServer(t, nil)
	require.Nil(t, pendingHandoff(s), "nothing to answer yet")
	s, a, _ := offeringServer(t)
	a.Sink().TurnStarted()
	require.Nil(t, pendingHandoff(s), "the agent works")
	a.Sink().TurnCompleted(native.TurnResult{StopReason: native.StopReasonEndTurn})
	require.NotNil(t, pendingHandoff(s))
	s.queue.Restore([]QueuedMessage{{ID: 1, Content: "queued"}}, 0)
	require.Nil(t, pendingHandoff(s), "the queued message goes next")
}

func TestHandoffRestartInvalidatesRequest(t *testing.T) {
	s, a, pending := offeringServer(t)
	_, err := s.deleteMessages(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "superseded", replyToHandoff(t, s, pending, "late", "continue"))
	assert.Equal(t, []string{"Check the build"}, turnTexts(a), "the old agent got nothing more")
}

// The receipts are bounded, but only the oldest goes when a new one comes:
// a reply retried right after the bound was reached still finds its
// receipt instead of being delivered twice.
func TestHandoffReceiptsDropTheOldestOnly(t *testing.T) {
	t.Parallel()
	var h handoffState
	id := func(i int) string { return "request-" + strconv.Itoa(i) }
	for i := range maxHandoffReceipts {
		h.record(id(i), "reply-"+strconv.Itoa(i), handoff.Result{Outcome: "applied"})
	}
	h.record(id(maxHandoffReceipts), "reply-new", handoff.Result{Outcome: "applied"})

	_, ok := h.receipt(id(0))
	assert.False(t, ok, "the oldest receipt is dropped")
	for i := 1; i <= maxHandoffReceipts; i++ {
		_, ok := h.receipt(id(i))
		require.True(t, ok, "receipt %d is kept", i)
	}
	// Recording a known request again keeps the bound.
	h.record(id(5), "reply-5", handoff.Result{Outcome: "queued"})
	assert.Len(t, h.receipts, maxHandoffReceipts)
	assert.Len(t, h.order, maxHandoffReceipts)
}
