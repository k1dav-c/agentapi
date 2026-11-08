package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/handoff"
	"github.com/coder/agentapi/lib/native"
)

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
	// order is the request ids in receipts, oldest first.
	order []string
}

// maxHandoffReceipts bounds the receipts kept; past it the oldest goes.
const maxHandoffReceipts = 256

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
	if _, ok := h.receipts[id]; !ok {
		// Drop the oldest alone: the reply just recorded, retried, must
		// still find its receipt rather than be delivered again.
		if len(h.order) >= maxHandoffReceipts {
			delete(h.receipts, h.order[0])
			h.order = h.order[1:]
		}
		h.order = append(h.order, id)
	}
	h.suppressed = id
	h.receipts[id] = handoffReceipt{replyID, result}
}

type (
	PendingHandoffResponse struct{ Body handoff.Pending }
	HandoffReplyRequest    struct{ Body handoff.Reply }
	HandoffReplyResponse   struct{ Body handoff.Result }
)

// pendingHandoff returns what the human reply protocol offers now, or nil:
// the agent's oldest pending request (kind "request"), else its last
// message. For a request it also returns the request offered, which a
// reply must answer even if other requests opened since. It doesn't need
// s.mu, so GET /handoff answers while a call into the agent holds it; a
// reply holds s.mu, so that what it answers stays offered until it is
// delivered.
func (s *Server) pendingHandoff() (*handoff.Request, agentstate.RequestState) {
	if s.emitter == nil || s.native == nil || s.turnGateLocked(turnUser, s.clock.Now()) != nil {
		// While the agent restarts, what it showed belongs to the old
		// one, and a reply would reach no agent; AgentAPI stopping, or
		// restarting for an update, would cut it short.
		return nil, agentstate.RequestState{}
	}
	view := s.emitter.HandoffView()
	if view.Status.Lifecycle != LifecycleReady || s.native.Status() != conv.StatusStable {
		return nil, agentstate.RequestState{}
	}
	kind := "request"
	request, content, ok := s.pendingNativeHandoff()
	if !ok {
		if s.queue.Len() > 0 {
			// The agent's last message isn't its last word.
			return nil, agentstate.RequestState{}
		}
		kind, content = "message", view.Reply
	}
	if strings.TrimSpace(content) == "" {
		return nil, agentstate.RequestState{}
	}
	status := view.Status
	// The request ID is part of the hash: two identical approvals are
	// different requests.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s:%q:%s", status.ServerID, view.Revision, status.Turn, kind, request.ID, content)))
	id := hex.EncodeToString(sum[:])
	if s.handoff.isSuppressed(id) {
		return nil, agentstate.RequestState{}
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
		return &HandoffReplyResponse{Body: s.answerHandoff(pending, request, reply.ID, reply.Content)}, nil
	}
	// The reply to the agent's last message is the next message, which goes
	// out as POST /message sends one: queued if the agent got busy since.
	result := handoff.Result{Outcome: "uncertain"}
	s.handoff.record(pending.ID, reply.ID, result)
	var resp MessageResponse
	switch err := s.createNativeMessageLocked(reply.Content, DispatchQueue, &resp); {
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

// answerHandoff answers r, the request the handoff pending offered, with
// content, records the outcome for the reply replyID, and returns it.
// Caller holds s.mu, so no other reply is delivered meanwhile.
func (s *Server) answerHandoff(pending *handoff.Request, r agentstate.RequestState, replyID, content string) handoff.Result {
	response, ok := nativeHandoffResponse(r, content)
	if !ok {
		return handoff.Result{Outcome: "invalid"}
	}
	ctx, cancel := s.agentCtx(nativeCallTimeout)
	defer cancel()
	result := handoff.Result{Outcome: "applied"}
	switch _, err := s.native.Respond(ctx, r.ID, response); {
	case errors.Is(err, native.ErrInvalidResponse):
		// Nothing was sent, so a corrected reply may follow.
		return handoff.Result{Outcome: "invalid"}
	case errors.Is(err, native.ErrRequestNotFound), errors.Is(err, native.ErrNotStarted):
		// Answered elsewhere, withdrawn or expired; nothing was sent.
		result.Outcome = "superseded"
	case err != nil:
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", r.ID, "error", err)
		result.Outcome = "uncertain"
	}
	s.handoff.record(pending.ID, replyID, result)
	return result
}

func (s *Server) registerHandoffRoutes() {
	huma.Get(s.api, "/handoff", s.getPendingHandoff)
	huma.Post(s.api, "/handoff/reply", s.replyHandoff)
}
