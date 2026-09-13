package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/coder/agentapi/lib/handoff"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/danielgtaylor/huma/v2"
)

var terminalSelection = regexp.MustCompile(`(?m)^\s*[❯›>]\s*(\d+)\.\s+`)
var terminalConfirmation = regexp.MustCompile(`(?i)(enter to confirm|press enter|esc to cancel|would you like to proceed|do you want to proceed)`)
var terminalOption = regexp.MustCompile(`(?m)^\s*[❯›>]?\s*(\d+)\.\s+`)

func isTerminalQuestion(content string) bool {
	return terminalSelection.MatchString(content) && terminalConfirmation.MatchString(content)
}

// Only expose deliberate, bounded terminal actions. Discord text never becomes
// arbitrary terminal escape sequences. The browser's terminal remains available
// for unsupported prompts.
func terminalReply(content, answer string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "enter":
		return "\r", true
	case "esc":
		return "\x1b", true
	}
	answer = strings.TrimSpace(answer)
	if _, err := strconv.Atoi(answer); err != nil || len(answer) != 1 {
		return "", false
	}
	for _, match := range terminalOption.FindAllStringSubmatch(content, -1) {
		if match[1] == answer {
			return answer + "\r", true
		}
	}
	return "", false
}

type handoffReceipt struct {
	replyID string
	result  handoff.Result
}

type PendingHandoffResponse struct{ Body handoff.Pending }
type HandoffReplyRequest struct{ Body handoff.Reply }
type HandoffReplyResponse struct{ Body handoff.Result }

// Caller holds s.mu. Read the emitter as one snapshot: its revision changes on
// every lifecycle transition and reset, invalidating replies to previous PTYs.
func (s *Server) pendingHandoffLocked() *handoff.Request {
	if s.emitter == nil || s.conversation == nil {
		return nil
	}
	s.emitter.mu.Lock()
	status := s.emitter.statusChangeBody()
	revision := s.emitter.handoffRevision
	screen := s.emitter.screen
	messages := append([]st.ConversationMessage(nil), s.emitter.messages...)
	s.emitter.mu.Unlock()
	if status.Lifecycle != LifecycleReady || s.conversation.Status() != st.ConversationStatusStable {
		return nil
	}
	kind, content := "message", ""
	if s.transport == TransportPTY && isTerminalQuestion(screen) {
		kind, content = "terminal", screen
	} else {
		if len(s.messageQueue) > 0 {
			return nil
		}
		hasUser := false
		for _, message := range messages {
			if message.Role == st.ConversationRoleUser {
				hasUser = true
				content = ""
			}
			if message.Role == st.ConversationRoleAgent && hasUser {
				content = message.Message
			}
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:%s:%s", status.SessionID, revision, status.RunID, kind, content)))
	id := hex.EncodeToString(sum[:])
	if id == s.handoffSuppressed {
		return nil
	}
	return &handoff.Request{ID: id, SessionID: status.SessionID, RunID: status.RunID,
		AgentType: string(status.AgentType), Kind: kind, Content: content}
}

func (s *Server) getPendingHandoff(_ context.Context, _ *struct{}) (*PendingHandoffResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &PendingHandoffResponse{Body: handoff.Pending{Request: s.pendingHandoffLocked()}}, nil
}

func (s *Server) replyHandoff(_ context.Context, input *HandoffReplyRequest) (*HandoffReplyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := input.Body
	if reply.ID == "" || reply.RequestID == "" || strings.TrimSpace(reply.Content) == "" || len(reply.Content) > 16000 {
		return nil, huma.Error400BadRequest("request_id, id and nonempty content (at most 16000 bytes) are required")
	}
	if receipt, ok := s.handoffReceipts[reply.RequestID]; ok {
		if receipt.replyID == reply.ID {
			return &HandoffReplyResponse{Body: receipt.result}, nil
		}
		return &HandoffReplyResponse{Body: handoff.Result{Outcome: "superseded"}}, nil
	}
	pending := s.pendingHandoffLocked()
	if pending == nil || pending.ID != reply.RequestID {
		return &HandoffReplyResponse{Body: handoff.Result{Outcome: "superseded"}}, nil
	}
	content := reply.Content
	if pending.Kind == "terminal" {
		var ok bool
		content, ok = terminalReply(pending.Content, content)
		if !ok {
			return &HandoffReplyResponse{Body: handoff.Result{Outcome: "invalid"}}, nil
		}
	}
	// Reserve before touching the PTY. A failed/partial write must never be
	// retried automatically; it may already have approved an operation.
	if s.handoffReceipts == nil {
		s.handoffReceipts = make(map[string]handoffReceipt)
	}
	if len(s.handoffReceipts) >= 256 {
		clear(s.handoffReceipts)
	}
	s.handoffSuppressed = pending.ID
	result := handoff.Result{Outcome: "uncertain"}
	s.handoffReceipts[pending.ID] = handoffReceipt{reply.ID, result}
	var err error
	if pending.Kind == "terminal" {
		var n int
		n, err = s.agentio.Write([]byte(content))
		if err == nil && n != len(content) {
			err = io.ErrShortWrite
		}
	} else {
		err = s.conversation.Send(FormatMessage(s.agentType, content)...)
	}
	if err == nil {
		result.Outcome = "applied"
	} else {
		s.logger.Error("Human reply delivery is uncertain; refusing automatic retry", "request_id", pending.ID, "error", err)
	}
	s.handoffReceipts[pending.ID] = handoffReceipt{reply.ID, result}
	return &HandoffReplyResponse{Body: result}, nil
}
