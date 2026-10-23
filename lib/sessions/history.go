// Package sessions reads an agent's earlier sessions in a working
// directory from its session logs: the list of them, a session's
// conversation, the subagents it ran and what its model calls cost.
package sessions

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

// ErrSessionNotFound is returned for a session the agent has no log of in
// the working directory.
var ErrSessionNotFound = errors.New("no such session in this directory")

// SessionSummary describes one of an agent's sessions in a working
// directory, as its log has it.
type SessionSummary struct {
	// ID resumes the session: Claude Code's session ID, Codex's thread ID,
	// Pi's session ID.
	ID string `json:"id" doc:"The agent's identifier of the session."`
	// Title is the name the agent or the user gave the session; empty for
	// a session never named.
	Title string `json:"title,omitempty" doc:"Name the agent or the user gave the session, if any."`
	// FirstPrompt is the user's first message, shortened.
	FirstPrompt string    `json:"first_prompt" doc:"The user's first message, shortened."`
	Prompts     int       `json:"prompts" doc:"How many messages the user sent."`
	CreatedAt   time.Time `json:"created_at" doc:"When the session started."`
	UpdatedAt   time.Time `json:"updated_at" doc:"When the session's log last changed."`
	// path is the session log.
	path string
}

// SessionHistory is a session's conversation, read from its log: the
// messages as conv.Conversation has them, and the rich messages.
type SessionHistory struct {
	Messages []conv.Message
	Rich     []transcript.RichMessage
	// Title is the session's name, if it has one.
	Title string
}

// historyMessagePrefix starts the IDs of the rich messages read from a
// session log, so that the agent that resumes the session can't replace
// one with a message of the same ID.
const historyMessagePrefix = "history-"

// firstPromptLength is how much of the first prompt a summary keeps.
const firstPromptLength = 200

// List returns up to limit of the sessions, the latest first. Sessions in
// which the user sent nothing are left out.
func (s *Store) List(limit int) ([]SessionSummary, error) {
	summaries, err := s.summaries()
	if err != nil {
		return nil, err
	}
	list := slices.DeleteFunc(summaries, func(summary SessionSummary) bool { return summary.Prompts == 0 })
	slices.SortStableFunc(list, func(a, b SessionSummary) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

// ReadHistory reads the conversation of the session id. It returns
// ErrSessionNotFound for a session it has no log of.
func (s *Store) ReadHistory(id string) (SessionHistory, error) {
	path, ok := s.find(id)
	if !ok {
		return SessionHistory{}, ErrSessionNotFound
	}
	history := s.read(path)
	if s.agent == "codex" {
		history.Title = s.threadName(id)
	}
	return history, nil
}

// read reads a session log's conversation. The user's messages are the
// prompts; an agent message joins the text of the rich messages that
// followed one.
func (s *Store) read(path string) SessionHistory {
	parser := s.src.format.NewParser()
	var namer agentlog.SessionNamer
	if s.src.namesInLog {
		namer = s.src.format.NewNamer()
	}
	var (
		history SessionHistory
		// order holds the rich messages' keys in the order they first
		// appeared, latest holds their final state: a parser reports a
		// message again as it grows, or only once the next line came.
		order  []string
		latest = make(map[string]transcript.RichMessage)
	)
	addRich := func(msgs []transcript.RichMessage) {
		for _, m := range msgs {
			if m.MessageID == "" || (m.Role == "user" && !hasToolResult(m)) {
				// The user's prompts are the messages; the chat takes only
				// the tool results from the user's rich messages.
				continue
			}
			m.MessageID = historyMessagePrefix + m.MessageID
			key := m.MessageID + "\x00" + m.Role
			if _, seen := latest[key]; !seen {
				order = append(order, key)
			}
			latest[key] = m
		}
	}
	s.src.lines(path, func(line []byte) bool {
		if namer != nil {
			if name, changed := namer.Name(line); changed {
				history.Title = name
			}
		}
		if prompt, at, ok := s.src.prompt(line); ok {
			history.Messages = append(history.Messages, conv.Message{
				Id: len(history.Messages), Role: conv.RoleUser, Message: prompt, Time: at,
			})
		}
		if msgs, err := parser.ParseLine(line); err == nil {
			addRich(msgs)
		}
		return true
	})
	addRich(parser.Flush())

	// The agent's text after each prompt becomes the agent message that
	// answers it: the text of the rich messages written since, as the chat
	// places them.
	texts := make(map[int][]string)
	times := make(map[int]time.Time)
	promptBefore := promptFinder(history.Messages)
	for _, key := range order {
		m := latest[key]
		history.Rich = append(history.Rich, m)
		if m.Role != "assistant" {
			continue
		}
		text := richText(m)
		if text == "" {
			continue
		}
		prompt := promptBefore(m.Timestamp)
		texts[prompt] = append(texts[prompt], text)
		if _, ok := times[prompt]; !ok {
			times[prompt] = m.Timestamp
		}
	}
	var messages []conv.Message
	add := func(prompt int) {
		if text := texts[prompt]; len(text) > 0 {
			messages = append(messages, conv.Message{
				Id: len(messages), Role: conv.RoleAgent, Message: strings.Join(text, "\n\n"), Time: times[prompt],
			})
		}
	}
	add(-1)
	for i, m := range history.Messages {
		m.Id = len(messages)
		messages = append(messages, m)
		add(i)
	}
	history.Messages = messages
	return history
}

// promptFinder returns a function that returns the index of the last
// prompt sent before t, or -1. A long session has thousands of prompts
// and tens of thousands of messages: while the prompts are in the order
// of their times, as a log has them, it searches them by halves.
func promptFinder(prompts []conv.Message) func(t time.Time) int {
	if slices.IsSortedFunc(prompts, func(a, b conv.Message) int { return a.Time.Compare(b.Time) }) {
		return func(t time.Time) int {
			return sort.Search(len(prompts), func(i int) bool { return prompts[i].Time.After(t) }) - 1
		}
	}
	return func(t time.Time) int {
		for i := len(prompts) - 1; i >= 0; i-- {
			if !prompts[i].Time.After(t) {
				return i
			}
		}
		return -1
	}
}

// hasToolResult reports whether a rich message holds a tool result.
func hasToolResult(m transcript.RichMessage) bool {
	return slices.ContainsFunc(m.Content, func(b transcript.RichContentBlock) bool { return b.Type == "tool_result" })
}

// richText joins the text blocks of a rich message.
func richText(m transcript.RichMessage) string {
	var texts []string
	for _, b := range m.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			texts = append(texts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}

// shorten cuts s to n runes, on one line.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > n {
		return string(runes[:n-1]) + "…"
	}
	return s
}
