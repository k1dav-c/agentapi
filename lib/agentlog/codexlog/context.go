package codexlog

import (
	"strings"
)

// contextFragments are the messages Codex adds to the model's input in
// the user's name: the environment, AGENTS.md, a skill, a shell command
// the user ran, the note that a turn was interrupted, what a subagent
// reported, reminders. Codex itself tells them from what the user wrote
// by how they start and end (its contextual user fragments), so a user's
// message that merely starts with a tag, such as pasted HTML, is the
// user's.
var contextFragments = []struct{ start, end string }{
	{"<environment_context>", "</environment_context>"},
	{"# AGENTS.md instructions for ", "</INSTRUCTIONS>"},
	{"<user_instructions>", "</user_instructions>"},
	{"<skill>", "</skill>"},
	{"<user_shell_command>", "</user_shell_command>"},
	{"<turn_aborted>", "</turn_aborted>"},
	{"<subagent_notification>", "</subagent_notification>"},
	{"<current_time_reminder>", "</current_time_reminder>"},
	{"<context_window_reminder>", "</context_window_reminder>"},
	{"<user_verification_notice>", "</user_verification_notice>"},
	{"<codex_internal_context", "</codex_internal_context>"},
}

// isContext reports whether a text block of a user message is context
// Codex added rather than something the user wrote.
func isContext(text string) bool {
	text = strings.TrimSpace(text)
	for _, f := range contextFragments {
		if hasPrefixFold(text, f.start) && hasSuffixFold(text, f.end) {
			return true
		}
	}
	return false
}

// isImageTag reports whether a text block is one of the tags Codex puts
// around an image the user attached (<image name=[Image #1] ...> and
// </image>), which the user didn't write either.
func isImageTag(text string) bool {
	text = strings.TrimSpace(text)
	if text == "</image>" {
		return true
	}
	return strings.HasPrefix(text, "<image") && strings.HasSuffix(text, ">") && !strings.Contains(text, "\n") &&
		(len(text) == len("<image>") || text[len("<image")] == ' ' || text[len("<image")] == '>')
}

// userText joins what the user wrote of a user message's content: its
// text blocks, without the context Codex added and the tags around the
// images. It is the one place the parser, the timeline events and the
// prompts of the session list tell the two apart.
func userText(blocks []codexContentBlock) string {
	var texts []string
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" || isContext(b.Text) || isImageTag(b.Text) {
			continue
		}
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, "\n")
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
