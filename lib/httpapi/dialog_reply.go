package httpapi

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
)

// freeTextFocusDelay is how long to wait after opening a prompt's free-text
// field before pasting into it, giving the TUI time to focus the field.
const freeTextFocusDelay = 300 * time.Millisecond

var (
	// Claude Code's ExitPlanMode approval dialog. The prompt text wraps
	// across lines, so match any whitespace between words.
	planApprovalPrompt = regexp.MustCompile(`(?i)would\s+you\s+like\s+to\s+proceed|ready\s+to\s+code\?`)
	// The option that turns into an inline feedback field when selected.
	planFeedbackOption = regexp.MustCompile(`(?mi)^\s*[❯›>]?\s*(\d)\.\s+Tell\s+Claude\s+what\s+to\s+change`)

	// Codex's question tool (request_user_input) shows one question at a
	// time with numbered options and an inline notes field.
	codexQuestionFooter = regexp.MustCompile(`(?i)enter\s+to\s+submit\s+(answer|all)`)
	// The notes field is already open.
	codexNotesOpen = regexp.MustCompile(`(?i)tab\s+or\s+esc\s+to\s+clear\s+notes`)
	// The highlighted option.
	codexHighlightedOption = regexp.MustCompile(`(?m)^\s*›\s*(\d+)\.\s`)
	// The option meant for free-text answers.
	codexFreeTextOption = regexp.MustCompile(`(?mi)^\s*›?\s*(\d+)\.\s+(?:None\s+of\s+the\s+above|.*add\s+details\s+in\s+notes)`)
)

// freeTextReplyKeys returns the keys that open the free-text field of the
// interactive prompt on screen, so that a chat message can be delivered as
// the answer. ok is false when the screen shows no such prompt.
//
// A chat message typed while a prompt is up must not be sent like a regular
// message: the prompt ignores the pasted text and the trailing carriage
// return would pick the highlighted option (e.g. approve a plan, or answer a
// question with its default).
func freeTextReplyKeys(agentType mf.AgentType, screen string) (string, bool) {
	if !isTerminalQuestionScreen(agentType, screen) {
		return "", false
	}
	tail := screenTail(screen, terminalQuestionTailLines)
	if agentType == mf.AgentTypeClaude {
		return claudePlanFeedbackKey(tail)
	}
	if agentType == mf.AgentTypeCodex {
		return codexQuestionNotesKeys(tail)
	}
	return "", false
}

// claudePlanFeedbackKey selects "Tell Claude what to change" in the plan
// approval dialog, which turns the option into a feedback field.
func claudePlanFeedbackKey(tail string) (string, bool) {
	if !planApprovalPrompt.MatchString(tail) {
		return "", false
	}
	match := planFeedbackOption.FindStringSubmatch(tail)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// codexQuestionNotesKeys moves the highlight to the question's free-text
// option ("None of the above") and opens its notes field. Typing the
// option's number would submit it immediately, so arrow keys are used.
func codexQuestionNotesKeys(tail string) (string, bool) {
	if !codexQuestionFooter.MatchString(tail) {
		return "", false
	}
	if codexNotesOpen.MatchString(tail) {
		return "", true
	}
	highlighted := codexHighlightedOption.FindStringSubmatch(tail)
	freeText := codexFreeTextOption.FindStringSubmatch(tail)
	if highlighted == nil || freeText == nil {
		return "", false
	}
	from, _ := strconv.Atoi(highlighted[1])
	to, _ := strconv.Atoi(freeText[1])
	move := "\x1b[B" // down
	if to < from {
		move = "\x1b[A" // up
	}
	steps := to - from
	if steps < 0 {
		steps = -steps
	}
	return strings.Repeat(move, steps) + "\t", true
}

// keyPressPart writes keys to the agent and waits for the TUI to react. It
// is hidden from the recorded user message.
type keyPressPart struct {
	keys  string
	delay time.Duration
}

func (p keyPressPart) Do(writer st.AgentIO) error {
	if _, err := writer.Write([]byte(p.keys)); err != nil {
		return err
	}
	time.Sleep(p.delay)
	return nil
}

func (p keyPressPart) String() string { return "" }

// formatFreeTextReply opens the prompt's free-text field with keys and pastes
// the message into it. The conversation's carriage return then submits it.
func formatFreeTextReply(agentType mf.AgentType, keys string, message string) []st.MessagePart {
	var parts []st.MessagePart
	if keys != "" {
		parts = append(parts, keyPressPart{keys: keys, delay: freeTextFocusDelay})
	}
	// Paste only: the composer clearing FormatMessage adds for Codex would
	// send Backspace into the open free-text field.
	return append(parts, formatPaste(mf.TrimWhitespace(message))...)
}

// isTerminalKeyAnswer reports whether a handoff answer names a key press
// (Enter, Esc or an option number) rather than free text.
func isTerminalKeyAnswer(answer string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "enter" || answer == "esc" {
		return true
	}
	_, err := strconv.Atoi(answer)
	return err == nil && len(answer) == 1
}

// currentScreenLocked returns the last rendered terminal screen.
func (s *Server) currentScreenLocked() string {
	if s.emitter == nil {
		return ""
	}
	s.emitter.mu.Lock()
	defer s.emitter.mu.Unlock()
	return s.emitter.screen
}
