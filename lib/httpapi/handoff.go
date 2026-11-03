package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/handoff"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
)

// Only expose deliberate, bounded terminal actions. Reply text never becomes
// arbitrary terminal escape sequences. The browser's terminal remains available
// for unsupported prompts.
//
// Claude Code and Codex select a numbered option as soon as its digit is
// typed, so no carriage return follows it there: a trailing Enter would
// submit an empty feedback field (plan dialog "Tell Claude what to change"),
// answer the next question with its default, or land on the next prompt and
// approve it.
func terminalReply(agentType mf.AgentType, content, answer string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "enter":
		return "\r", true
	case "esc":
		return "\x1b", true
	}
	answer = strings.TrimSpace(answer)
	options := mf.TerminalOptions(content)
	if len(options) < 2 {
		return answer + "\r", answer != ""
	}
	if _, err := strconv.Atoi(answer); err != nil || len(answer) != 1 {
		return "", false
	}
	for _, option := range options {
		if option == answer {
			if agentType == mf.AgentTypeClaude || agentType == mf.AgentTypeCodex {
				return answer, true
			}
			return answer + "\r", true
		}
	}
	return "", false
}

type handoffReceipt struct {
	replyID string
	result  handoff.Result
}

// handoffState is what the human reply protocol remembers: the request it
// stopped offering, and the outcome of the replies it took, by request. It
// has a lock of its own, so that GET /handoff doesn't take s.mu.
type handoffState struct {
	mu         sync.Mutex
	suppressed string
	receipts   map[string]handoffReceipt
}

// suppress stops offering the request id.
func (h *handoffState) suppress(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.suppressed = id
}

// isSuppressed reports whether the request id is no longer offered.
func (h *handoffState) isSuppressed(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return id == h.suppressed
}

// receipt returns the outcome of the reply the request id took.
func (h *handoffState) receipt(id string) (handoffReceipt, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	receipt, ok := h.receipts[id]
	return receipt, ok
}

// record stops offering the request id and records the outcome of the
// reply replyID to it.
func (h *handoffState) record(id, replyID string, result handoff.Result) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.receipts == nil {
		h.receipts = make(map[string]handoffReceipt)
	}
	if _, ok := h.receipts[id]; !ok && len(h.receipts) >= 256 {
		clear(h.receipts)
	}
	h.suppressed = id
	h.receipts[id] = handoffReceipt{replyID, result}
}

type (
	PendingHandoffResponse struct{ Body handoff.Pending }
	HandoffReplyRequest    struct{ Body handoff.Reply }
	HandoffReplyResponse   struct{ Body handoff.Result }
)

// pendingHandoff returns what the human reply protocol offers now, or nil.
// For kind "request" it also returns the native request offered, which a
// reply must answer even if other requests opened since. It doesn't need
// s.mu, so GET /handoff answers while a call into the agent holds it; a
// reply holds s.mu, so that what it answers stays offered until it is
// delivered.
func (s *Server) pendingHandoff() (*handoff.Request, native.RequestState) {
	if s.emitter == nil || s.conversation == nil || s.restarting.Load() || s.updating.Load() {
		// While the agent restarts, what it showed belongs to the old
		// one, and a reply would reach no agent; AgentAPI restarting for
		// an update would cut it short.
		return nil, native.RequestState{}
	}
	view := s.emitter.HandoffView()
	if view.Status.Lifecycle != LifecycleReady || s.conversation.Status() != conv.StatusStable {
		return nil, native.RequestState{}
	}
	kind, content, request, ok := s.backend().handoffOffer(view)
	if !ok {
		if s.queue.Len() > 0 {
			// The agent's last message isn't its last word.
			return nil, native.RequestState{}
		}
		kind, content = "message", view.Reply
	}
	if strings.TrimSpace(content) == "" {
		return nil, native.RequestState{}
	}
	status := view.Status
	// The request ID is part of the hash: two identical approvals are
	// different requests.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s:%q:%s", status.ServerID, view.Revision, status.Turn, kind, request.ID, content)))
	id := hex.EncodeToString(sum[:])
	if s.handoff.isSuppressed(id) {
		return nil, native.RequestState{}
	}
	return &handoff.Request{
		ID: id, ServerID: status.ServerID, Turn: status.Turn,
		AgentType: string(status.AgentType), Kind: kind, Question: view.Question, Content: content,
	}, request
}

// getPendingHandoff handles GET /handoff, without s.mu (see
// pendingHandoff).
func (s *Server) getPendingHandoff(_ context.Context, _ *struct{}) (*PendingHandoffResponse, error) {
	pending, _ := s.pendingHandoff()
	return &PendingHandoffResponse{Body: handoff.Pending{Request: pending}}, nil
}

func (s *Server) replyHandoff(_ context.Context, input *HandoffReplyRequest) (*HandoffReplyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := input.Body
	if reply.ID == "" || reply.RequestID == "" || strings.TrimSpace(reply.Content) == "" || len(reply.Content) > 16000 {
		return nil, huma.Error400BadRequest("request_id, id and nonempty content (at most 16000 bytes) are required")
	}
	if receipt, ok := s.handoff.receipt(reply.RequestID); ok {
		if receipt.replyID == reply.ID {
			return &HandoffReplyResponse{Body: receipt.result}, nil
		}
		return &HandoffReplyResponse{Body: handoff.Result{Outcome: "superseded"}}, nil
	}
	pending, request := s.pendingHandoff()
	if pending == nil || pending.ID != reply.RequestID {
		return &HandoffReplyResponse{Body: handoff.Result{Outcome: "superseded"}}, nil
	}
	if pending.Kind != "message" {
		return &HandoffReplyResponse{Body: s.backend().answerHandoff(pending, request, reply.ID, reply.Content)}, nil
	}
	// The reply to the agent's last message is the next message, which goes
	// out as POST /message sends one: queued if the agent got busy since.
	result := handoff.Result{Outcome: "uncertain"}
	s.handoff.record(pending.ID, reply.ID, result)
	var resp MessageResponse
	switch err := s.backend().deliver(reply.Content, DispatchQueue, &resp); {
	case err != nil:
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", pending.ID, "error", err)
	case resp.Body.Queued:
		result.Outcome = "queued"
	default:
		result.Outcome = "applied"
	}
	s.handoff.record(pending.ID, reply.ID, result)
	return &HandoffReplyResponse{Body: result}, nil
}

func (s *Server) registerHandoffRoutes() {
	huma.Get(s.api, "/handoff", s.getPendingHandoff)
	huma.Post(s.api, "/handoff/reply", s.replyHandoff)
}
