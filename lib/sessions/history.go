// Package sessions reads an agent's earlier sessions in a working
// directory from its session logs: the list of them, a session's
// conversation, the subagents it ran and what its model calls cost.
package sessions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/agenthome"
	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
	st "github.com/coder/agentapi/lib/screentracker"
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
// messages as st.Conversation has them, and the rich messages.
type SessionHistory struct {
	Messages []st.ConversationMessage
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

// historyLog is what reading one agent's session logs takes.
type historyLog struct {
	// files lists the session logs of the working directory, newest first,
	// with the session IDs (empty when only the log says).
	files func(cwd string) ([]historyFile, error)
	// header reads the session's ID and start from its log's first line,
	// and reports false for a log that isn't a session of the directory
	// (another directory's, a subagent's).
	header func(line []byte, cwd string) (id string, started time.Time, ok bool)
	// prompt returns the user's message a line holds, if it holds one.
	prompt func(line []byte) (string, time.Time, bool)
	parser func() agentlog.LineParser
	namer  func() agentlog.SessionNamer
	// scan reads a session's lines, as agentlog.ScanFile does; nil is
	// agentlog.ScanFile. It differs for a log that refers to another one's
	// lines (a Codex fork).
	scan func(path string, fn func(line []byte) bool) bool
}

// lines calls fn with the lines of the session log at path.
func (h historyLog) lines(path string, fn func(line []byte) bool) bool {
	if h.scan != nil {
		return h.scan(path, fn)
	}
	return agentlog.ScanFile(path, fn)
}

// historyFile is a session log.
type historyFile struct {
	path    string
	modTime time.Time
}

func historyLogFor(agentType string) (historyLog, bool) {
	switch agentType {
	case "claude":
		return historyLog{
			files: claudeHistoryFiles, header: func(line []byte, _ string) (string, time.Time, bool) { return claudelog.HistoryHeader(line) }, prompt: claudelog.Prompt,
			parser: func() agentlog.LineParser { return claudelog.NewParser() }, namer: func() agentlog.SessionNamer { return claudelog.NewNamer() },
		}, true
	case "codex":
		return historyLog{
			files: codexHistoryFiles, header: codexlog.HistoryHeader, prompt: codexlog.Prompt,
			parser: func() agentlog.LineParser { return codexlog.NewParser() }, namer: func() agentlog.SessionNamer { return codexlog.NewNamer() },
			scan: codexlog.ScanLog,
		}, true
	case "pi":
		return historyLog{
			files: piHistoryFiles, header: pilog.HistoryHeader, prompt: pilog.Prompt,
			parser: func() agentlog.LineParser { return pilog.NewParser() }, namer: func() agentlog.SessionNamer { return pilog.NewNamer() },
		}, true
	}
	return historyLog{}, false
}

// HasHistory reports whether List reads the sessions of an agent of
// agentType.
func HasHistory(agentType string) bool {
	_, ok := historyLogFor(agentType)
	return ok
}

// List returns up to limit of the sessions an agent of agentType
// ("claude", "codex" or "pi") had in cwd, the latest first. Sessions in
// which the user sent nothing are left out.
func List(agentType, cwd string, limit int) ([]SessionSummary, error) {
	h, ok := historyLogFor(agentType)
	if !ok {
		return nil, fmt.Errorf("no session history for agent %q", agentType)
	}
	files, err := h.files(cwd)
	if err != nil {
		return nil, err
	}
	var sessions []SessionSummary
	for _, file := range files {
		if len(sessions) >= limit {
			break
		}
		s, ok := h.summarize(file, cwd)
		if ok {
			sessions = append(sessions, s)
		}
	}
	return sessions, nil
}

// summaryCache keeps the summaries of session logs that haven't changed
// since they were read: reading a long log takes a while.
var summaryCache = struct {
	sync.Mutex
	entries map[string]cachedSummary
}{entries: make(map[string]cachedSummary)}

type cachedSummary struct {
	modTime time.Time
	cwd     string
	summary SessionSummary
	ok      bool
}

// summarize returns the summary of a session log, from the cache while the
// log is unchanged.
func (h historyLog) summarize(file historyFile, cwd string) (SessionSummary, bool) {
	summaryCache.Lock()
	cached, hit := summaryCache.entries[file.path]
	summaryCache.Unlock()
	if hit && cached.modTime.Equal(file.modTime) && cached.cwd == cwd {
		return cached.summary, cached.ok
	}
	s, ok := h.readSummary(file, cwd)
	summaryCache.Lock()
	summaryCache.entries[file.path] = cachedSummary{modTime: file.modTime, cwd: cwd, summary: s, ok: ok}
	summaryCache.Unlock()
	return s, ok
}

// readSummary reads a session log for its summary.
func (h historyLog) readSummary(file historyFile, cwd string) (SessionSummary, bool) {
	s := SessionSummary{UpdatedAt: file.modTime, path: file.path}
	namer := h.namer()
	first := true
	ok := h.lines(file.path, func(line []byte) bool {
		if first {
			first = false
			id, started, ok := h.header(line, cwd)
			if !ok {
				return false
			}
			s.ID, s.CreatedAt = id, started
		}
		if name, changed := namer.Name(line); changed {
			s.Title = name
		}
		if prompt, at, ok := h.prompt(line); ok {
			if s.Prompts == 0 {
				s.FirstPrompt = shorten(prompt, firstPromptLength)
				if s.CreatedAt.IsZero() {
					s.CreatedAt = at
				}
			}
			s.Prompts++
		}
		return true
	})
	if polled, isPolled := namer.(agentlog.PolledSessionNamer); isPolled {
		if name, changed := polled.Poll(); changed {
			s.Title = name
		}
	}
	return s, ok && s.ID != "" && s.Prompts > 0
}

// ReadHistory reads the conversation of the session id an agent of
// agentType had in cwd. It returns ErrSessionNotFound for a session it has
// no log of.
func ReadHistory(agentType, cwd, id string) (SessionHistory, error) {
	h, ok := historyLogFor(agentType)
	if !ok {
		return SessionHistory{}, fmt.Errorf("no session history for agent %q", agentType)
	}
	if path, ok := cachedPath(id, cwd); ok {
		// Listed before: no need to look through every log for it.
		return h.read(path), nil
	}
	files, err := h.files(cwd)
	if err != nil {
		return SessionHistory{}, err
	}
	for _, file := range files {
		if h.idOf(file, cwd) == id {
			return h.read(file.path), nil
		}
	}
	return SessionHistory{}, ErrSessionNotFound
}

// cachedPath returns the log of the session id of cwd, if a listing read it
// and it is still there.
func cachedPath(id, cwd string) (string, bool) {
	summaryCache.Lock()
	var path string
	for p, c := range summaryCache.entries {
		if c.ok && c.cwd == cwd && c.summary.ID == id {
			path = p
			break
		}
	}
	summaryCache.Unlock()
	if path == "" {
		return "", false
	}
	_, err := os.Stat(path)
	return path, err == nil
}

// idOf returns the ID of the session a log holds, or "" for a log that
// isn't a session of cwd.
func (h historyLog) idOf(file historyFile, cwd string) string {
	var id string
	agentlog.ScanFile(file.path, func(line []byte) bool {
		id, _, _ = h.header(line, cwd)
		return false
	})
	return id
}

// read reads a session log's conversation. The user's messages are the
// prompts; an agent message joins the text of the rich messages that
// followed one.
func (h historyLog) read(path string) SessionHistory {
	parser, namer := h.parser(), h.namer()
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
	h.lines(path, func(line []byte) bool {
		if name, changed := namer.Name(line); changed {
			history.Title = name
		}
		if prompt, at, ok := h.prompt(line); ok {
			history.Messages = append(history.Messages, st.ConversationMessage{
				Id: len(history.Messages), Role: st.ConversationRoleUser, Message: prompt, Time: at,
			})
		}
		if msgs, err := parser.ParseLine(line); err == nil {
			addRich(msgs)
		}
		return true
	})
	addRich(parser.Flush())
	if polled, ok := namer.(agentlog.PolledSessionNamer); ok {
		if name, changed := polled.Poll(); changed {
			history.Title = name
		}
	}

	// The agent's text after each prompt becomes the agent message that
	// answers it: the text of the rich messages written since, as the chat
	// places them.
	texts := make(map[int][]string)
	times := make(map[int]time.Time)
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
		prompt := promptBefore(history.Messages, m.Timestamp)
		texts[prompt] = append(texts[prompt], text)
		if _, ok := times[prompt]; !ok {
			times[prompt] = m.Timestamp
		}
	}
	var messages []st.ConversationMessage
	add := func(prompt int) {
		if text := texts[prompt]; len(text) > 0 {
			messages = append(messages, st.ConversationMessage{
				Id: len(messages), Role: st.ConversationRoleAgent, Message: strings.Join(text, "\n\n"), Time: times[prompt],
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

// promptBefore returns the index of the last prompt sent before t, or -1.
func promptBefore(prompts []st.ConversationMessage, t time.Time) int {
	for i := len(prompts) - 1; i >= 0; i-- {
		if !prompts[i].Time.After(t) {
			return i
		}
	}
	return -1
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

// historyFilesIn lists the .jsonl files of dir, newest first.
func historyFilesIn(dir string) ([]historyFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []historyFile
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, historyFile{path: filepath.Join(dir, entry.Name()), modTime: info.ModTime()})
	}
	slices.SortFunc(files, func(a, b historyFile) int { return b.modTime.Compare(a.modTime) })
	return files, nil
}

// --- Claude Code ---

func claudeHistoryFiles(cwd string) ([]historyFile, error) {
	configDir, err := agenthome.ClaudeConfigDir()
	if err != nil {
		return nil, err
	}
	return historyFilesIn(filepath.Join(configDir, "projects", claudelog.EncodeCWD(cwd)))
}

// --- Codex ---

// codexHistoryScanLimit bounds how many session logs List reads to
// find a directory's: Codex keeps every directory's in one tree.
const codexHistoryScanLimit = 2000

func codexHistoryFiles(string) ([]historyFile, error) {
	root, err := codexlog.SessionsRoot("")
	if err != nil {
		return nil, err
	}
	var files []historyFile
	// The tree is sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl: walking
	// it in reverse name order goes from the latest.
	err = walkReverse(root, 3, func(dir string) bool {
		day, err := historyFilesIn(dir)
		if err != nil {
			return true
		}
		files = append(files, day...)
		return len(files) < codexHistoryScanLimit
	})
	return files, err
}

// walkReverse calls fn with the directories depth levels below root, in
// reverse name order, until it returns false.
func walkReverse(root string, depth int, fn func(dir string) bool) error {
	if depth == 0 {
		if !fn(root) {
			return fs.SkipAll
		}
		return nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if !entries[i].IsDir() {
			continue
		}
		if err := walkReverse(filepath.Join(root, entries[i].Name()), depth-1, fn); err != nil {
			if errors.Is(err, fs.SkipAll) && depth == 3 {
				return nil
			}
			return err
		}
	}
	return nil
}

// --- Pi ---

func piHistoryFiles(cwd string) ([]historyFile, error) {
	dir, err := pilog.SessionsDir(cwd, "")
	if err != nil {
		return nil, err
	}
	return historyFilesIn(dir)
}
