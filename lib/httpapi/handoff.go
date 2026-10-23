package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/handoff"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native"
)

var (
	terminalSelection    = regexp.MustCompile(`(?m)^\s*[❯›>]\s*(\d+)\.\s+`)
	terminalConfirmation = regexp.MustCompile(`(?i)(enter\s+to\s+confirm|enter\s+to\s+submit|press\s+enter|esc\s+to\s+cancel|enter\s+select|would\s+you\s+like\s+to\s+proceed|do\s+you\s+want\s+to\s+proceed)`)
	terminalOption       = regexp.MustCompile(`(?m)^\s*[❯›>]?\s*(\d+)\.\s+`)
)

// terminalQuestionKeywords must appear (case-insensitively) next to a
// numbered list for it to count as a prompt.
var terminalQuestionKeywords = []string{"choose", "select", "option", "continue", "cancel", "allow", "deny", "approve", "permission"}

// Prompt detection runs on every screen change. Go's regexps are slow on
// the case-insensitive alternations above, and agent output almost never
// contains these words, so each regexp is only tried once a plain substring
// check shows it could match.
var terminalConfirmationWords = []string{"enter", "esc", "proceed"}

func containsAny(s string, words []string) bool {
	for _, word := range words {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

// hasConfirmationHint reports whether content (lower is its lowercase form)
// shows a confirmation hint such as "Enter to confirm · Esc to cancel".
func hasConfirmationHint(content, lower string) bool {
	return containsAny(lower, terminalConfirmationWords) && terminalConfirmation.MatchString(content)
}

// terminalQuestionTailLines bounds how much of the bottom of the screen is
// inspected for an interactive prompt. The PTY screen includes scrollback, so
// scanning all of it would match numbered lists in earlier agent output and
// block the message queue indefinitely.
const terminalQuestionTailLines = 40

// isTerminalQuestionScreen reports whether the agent is currently showing an
// interactive terminal prompt (selection list, confirmation) that a queued
// message must not be typed into.
func isTerminalQuestionScreen(agentType mf.AgentType, screen string) bool {
	// Only the bottom of the screen matters, and the screen can be a
	// thousand lines; this runs on every screen change.
	screen = screenTail(screen, max(mf.InputBoxScanLines, terminalQuestionTailLines))
	visible, ok := mf.HasInputBox(agentType, screen)
	if ok && visible {
		// The agent's own input box is showing, so it's waiting for a
		// message rather than an answer to a dialog.
		return false
	}
	tail := screenTail(screen, terminalQuestionTailLines)
	lower := strings.ToLower(tail)
	if ok && hasConfirmationHint(tail, lower) {
		// The input box is gone and a confirmation hint is showing, e.g.
		// Claude Code's folder trust dialog, whose options aren't numbered.
		return true
	}
	return isTerminalQuestionText(tail, lower)
}

// screenTail returns the last n lines of screen, ignoring trailing blank lines.
// It scans back from the end instead of splitting the whole screen.
func screenTail(screen string, n int) string {
	screen = strings.TrimRight(screen, " \t\r\n")
	start := len(screen)
	for range n {
		i := strings.LastIndexByte(screen[:start], '\n')
		if i < 0 {
			return screen
		}
		start = i
	}
	return screen[start+1:]
}

func isTerminalQuestionText(content, lower string) bool {
	if hasConfirmationHint(content, lower) && strings.ContainsAny(content, "❯›>") &&
		terminalSelection.MatchString(content) {
		return true
	}
	return containsAny(lower, terminalQuestionKeywords) && len(terminalOption.FindAllString(content, -1)) >= 2
}

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
	if len(terminalOption.FindAllString(content, -1)) < 2 {
		return answer + "\r", answer != ""
	}
	if _, err := strconv.Atoi(answer); err != nil || len(answer) != 1 {
		return "", false
	}
	for _, match := range terminalOption.FindAllStringSubmatch(content, -1) {
		if match[1] == answer {
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

// handoffView is what the human reply protocol reads of the emitter, as of
// one moment.
type handoffView struct {
	status StatusChangeBody
	// revision changes on every lifecycle transition and reset, which
	// invalidates the replies to what was offered before.
	revision uint64
	screen   string
	// dialog: the screen shows a dialog of the agent's terminal.
	dialog bool
	// question is the user's last message, and reply the agent's last
	// message after it; both are empty without one.
	question, reply string
}

// handoffView returns what the human reply protocol reads of the emitter.
func (e *EventEmitter) handoffView() handoffView {
	e.mu.Lock()
	defer e.mu.Unlock()
	view := handoffView{
		status: e.statusChangeBody(), revision: e.handoffRevision,
		screen: e.screen, dialog: e.screenQuestion,
	}
	reply, replied := "", false
	for i := len(e.messages) - 1; i >= 0; i-- {
		msg := e.messages[i]
		if msg.Role == conv.RoleUser {
			view.question, view.reply = msg.Message, reply
			break
		}
		if msg.Role == conv.RoleAgent && !replied {
			reply, replied = msg.Message, true
		}
	}
	return view
}

// pendingHandoff returns what the human reply protocol offers now, or nil.
// For kind "request" it also returns the native request offered, which a
// reply must answer even if other requests opened since. It doesn't need
// s.mu, so GET /handoff answers while a call into the agent holds it; a
// reply holds s.mu, so that what it answers stays offered until it is
// delivered.
func (s *Server) pendingHandoff() (*handoff.Request, native.RequestState) {
	if s.emitter == nil || s.conversation == nil || s.restarting.Load() {
		// While the agent restarts, what it showed belongs to the old
		// one, and a reply would reach no agent.
		return nil, native.RequestState{}
	}
	view := s.emitter.handoffView()
	if view.status.Lifecycle != LifecycleReady || s.conversation.Status() != conv.StatusStable {
		return nil, native.RequestState{}
	}
	kind, content, request, ok := s.backend().handoffOffer(view)
	if !ok {
		if s.queue.Len() > 0 {
			// The agent's last message isn't its last word.
			return nil, native.RequestState{}
		}
		kind, content = "message", view.reply
	}
	if strings.TrimSpace(content) == "" {
		return nil, native.RequestState{}
	}
	status := view.status
	// The request ID is part of the hash: two identical approvals are
	// different requests.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s:%q:%s", status.ServerID, view.revision, status.Turn, kind, request.ID, content)))
	id := hex.EncodeToString(sum[:])
	if s.handoff.isSuppressed(id) {
		return nil, native.RequestState{}
	}
	return &handoff.Request{
		ID: id, ServerID: status.ServerID, Turn: status.Turn,
		AgentType: string(status.AgentType), Kind: kind, Question: view.question, Content: content,
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
	// The reply to the agent's last message is the next message.
	result := handoff.Result{Outcome: "uncertain"}
	s.handoff.record(pending.ID, reply.ID, result)
	if err := s.conversation.Send(FormatMessage(s.agentType, reply.Content)...); err != nil {
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", pending.ID, "error", err)
	} else {
		result.Outcome = "applied"
	}
	s.handoff.record(pending.ID, reply.ID, result)
	return &HandoffReplyResponse{Body: result}, nil
}

func (s *Server) registerHandoffRoutes() {
	huma.Get(s.api, "/handoff", s.getPendingHandoff)
	huma.Post(s.api, "/handoff/reply", s.replyHandoff)
}
