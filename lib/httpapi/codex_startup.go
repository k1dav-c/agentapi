package httpapi

import (
	"regexp"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
)

// codexUpdatePromptTailLines bounds how much of the bottom of the screen is
// inspected for the Codex update dialog, so the older boxed "✨ Update
// available!" banner left in scrollback is never mistaken for it.
const codexUpdatePromptTailLines = 15

// codexUpdatePromptRetryInterval is how long to wait before pressing Esc
// again if the update dialog is still showing.
const codexUpdatePromptRetryInterval = 3 * time.Second

var (
	codexUpdatePromptTitle = regexp.MustCompile(`(?m)^\s*Update available\b`)
	codexUpdatePromptHint  = regexp.MustCompile(`(?i)\besc\s+skip\b`)
)

// isCodexUpdatePrompt reports whether Codex is showing its startup "Update
// available" dialog:
//
//	Update available · 0.158.0 → 0.159.3
//	› 1. Update now (...)
//	  2. Skip
//	  3. Skip until next version
//	enter continue · esc skip
//
// Since Codex 0.158 this is a full-screen selection dialog shown before the
// composer whenever a newer release exists, instead of a banner. Until it is
// answered no message or initial prompt can be delivered.
func isCodexUpdatePrompt(screen string) bool {
	if visible, _ := mf.HasInputBox(mf.AgentTypeCodex, screen); visible {
		return false
	}
	tail := screenTail(screen, codexUpdatePromptTailLines)
	return codexUpdatePromptTitle.MatchString(tail) && codexUpdatePromptHint.MatchString(tail)
}

// dismissCodexUpdatePrompt skips the Codex update dialog with Esc ("esc
// skip"), which only skips this launch and doesn't persist anything. An
// unattended server has nobody to answer the dialog, and updating the CLI
// is the workspace's job, not the agent session's.
func (s *Server) dismissCodexUpdatePrompt() {
	if s.agentType != mf.AgentTypeCodex || s.transport != TransportPTY || s.emitter == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Wait for the dialog to finish rendering before answering it.
	if s.conversation.Status() != st.ConversationStatusStable {
		return
	}
	now := s.clock.Now()
	if now.Sub(s.codexUpdateDismissedAt) < codexUpdatePromptRetryInterval {
		return
	}
	screen := s.currentScreenLocked()
	if !isCodexUpdatePrompt(screen) {
		return
	}
	s.codexUpdateDismissedAt = now
	if _, err := s.agentio.Write([]byte("\x1b")); err != nil {
		s.logger.Error("Failed to dismiss the Codex update prompt", "error", err)
		return
	}
	s.logger.Info("Skipped the Codex update prompt so messages can be delivered")
}
