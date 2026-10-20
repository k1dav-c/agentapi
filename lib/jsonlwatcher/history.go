package jsonlwatcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/agenthome"
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

// maxHistoryLine bounds a session log line; a longer one (a huge tool
// result) is skipped.
const maxHistoryLine = 64 * 1024 * 1024

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
	parser func() LineParser
	namer  func() SessionNamer
	// scan reads a session's lines, as scanLog does; nil is scanLog. It
	// differs for a log that refers to another one's lines (a Codex fork).
	scan func(path string, fn func(line []byte) bool) bool
}

// lines calls fn with the lines of the session log at path.
func (h historyLog) lines(path string, fn func(line []byte) bool) bool {
	if h.scan != nil {
		return h.scan(path, fn)
	}
	return scanLog(path, fn)
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
			files: claudeHistoryFiles, header: claudeHistoryHeader, prompt: claudePrompt,
			parser: func() LineParser { return NewClaudeParser() }, namer: func() SessionNamer { return &claudeSessionNamer{} },
		}, true
	case "codex":
		return historyLog{
			files: codexHistoryFiles, header: codexHistoryHeader, prompt: codexPrompt,
			parser: func() LineParser { return NewCodexParser() }, namer: func() SessionNamer { return &codexSessionNamer{} },
			scan: scanCodexLog,
		}, true
	case "pi":
		return historyLog{
			files: piHistoryFiles, header: piHistoryHeader, prompt: piPrompt,
			parser: func() LineParser { return NewPiParser() }, namer: func() SessionNamer { return &piSessionNamer{} },
		}, true
	}
	return historyLog{}, false
}

// HasSessionHistory reports whether ListSessions reads the sessions of an
// agent of agentType.
func HasSessionHistory(agentType string) bool {
	_, ok := historyLogFor(agentType)
	return ok
}

// ListSessions returns up to limit of the sessions an agent of agentType
// ("claude", "codex" or "pi") had in cwd, the latest first. Sessions in
// which the user sent nothing are left out.
func ListSessions(agentType, cwd string, limit int) ([]SessionSummary, error) {
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
	if polled, isPolled := namer.(PolledSessionNamer); isPolled {
		if name, changed := polled.Poll(); changed {
			s.Title = name
		}
	}
	return s, ok && s.ID != "" && s.Prompts > 0
}

// ReadSessionHistory reads the conversation of the session id an agent of
// agentType had in cwd. It returns ErrSessionNotFound for a session it has
// no log of.
func ReadSessionHistory(agentType, cwd, id string) (SessionHistory, error) {
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
	scanLog(file.path, func(line []byte) bool {
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
	if polled, ok := namer.(PolledSessionNamer); ok {
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

// scanLog calls fn with each line of a session log until it returns false.
// It reports whether the log could be read.
func scanLog(path string, fn func(line []byte) bool) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 && !fn(line) {
			return true
		}
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// readLine reads a line, which can be longer than the reader's buffer. A
// line longer than maxHistoryLine is returned empty.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) <= maxHistoryLine {
			line = append(line, chunk...)
		} else {
			line = line[:0:0]
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
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
	return historyFilesIn(filepath.Join(configDir, "projects", encodeCWD(cwd)))
}

// claudeHistoryHeader takes the session ID from the file's name: the first
// lines of a log vary (a queue operation, a summary).
func claudeHistoryHeader(line []byte, _ string) (string, time.Time, bool) {
	var entry struct {
		SessionID   string `json:"sessionId"`
		Timestamp   string `json:"timestamp"`
		IsSidechain bool   `json:"isSidechain"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.IsSidechain {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return entry.SessionID, started, entry.SessionID != ""
}

// claudeCommandPrefixes start the user entries Claude Code logs for a slash
// command the user ran, and the notes it adds when the user interrupts a
// turn ("[Request interrupted by user]"), which are not the user's messages.
var claudeCommandPrefixes = []string{"<command-name>", "<command-message>", "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>", "[Request interrupted by user"}

func claudePrompt(line []byte) (string, time.Time, bool) {
	return claudeUserText(line, false)
}

// claudeUserText returns the user's message a line holds; with sidechain,
// also one of a subagent's log, where the agent wrote the "user" messages.
func claudeUserText(line []byte, sidechain bool) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry struct {
		Type        string `json:"type"`
		Timestamp   string `json:"timestamp"`
		IsMeta      bool   `json:"isMeta"`
		IsSidechain bool   `json:"isSidechain"`
		Message     *struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsMeta || (entry.IsSidechain && !sidechain) || entry.Message == nil {
		return "", time.Time{}, false
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) != nil {
		var blocks []ContentBlock
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			return "", time.Time{}, false
		}
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
		text = strings.Join(texts, "\n")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", time.Time{}, false
	}
	for _, prefix := range claudeCommandPrefixes {
		if strings.HasPrefix(text, prefix) {
			return "", time.Time{}, false
		}
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return text, at, true
}

// --- Codex ---

// codexHistoryScanLimit bounds how many session logs ListSessions reads to
// find a directory's: Codex keeps every directory's in one tree.
const codexHistoryScanLimit = 2000

func codexHistoryFiles(string) ([]historyFile, error) {
	root, err := codexSessionsRoot("")
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

func codexHistoryHeader(line []byte, cwd string) (string, time.Time, bool) {
	var meta codexSessionMeta
	if json.Unmarshal(line, &meta) != nil || meta.Payload.ID == "" || meta.isSubAgent() {
		return "", time.Time{}, false
	}
	if filepath.Clean(meta.Payload.CWD) != filepath.Clean(cwd) {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, meta.Timestamp)
	return meta.Payload.ID, started, true
}

// codexPrompt takes the user's messages from the model's input. Codex adds
// messages of its own there (the environment, AGENTS.md), which start with
// a tag.
func codexPrompt(line []byte) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"response_item"`)) || !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry codexLine
	if json.Unmarshal(line, &entry) != nil || entry.Type != "response_item" {
		return "", time.Time{}, false
	}
	var payload codexPayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Type != "message" || payload.Role != "user" {
		return "", time.Time{}, false
	}
	var blocks []codexContentBlock
	if json.Unmarshal(payload.Content, &blocks) != nil {
		return "", time.Time{}, false
	}
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(texts, "\n"))
	if text == "" || strings.HasPrefix(text, "<") {
		return "", time.Time{}, false
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return text, at, true
}

// --- Pi ---

func piHistoryFiles(cwd string) ([]historyFile, error) {
	dir, err := (&PiResolver{CWD: cwd}).sessionsDir(PiSession{})
	if err != nil {
		return nil, err
	}
	return historyFilesIn(dir)
}

func piHistoryHeader(line []byte, cwd string) (string, time.Time, bool) {
	var header piSessionHeader
	if json.Unmarshal(line, &header) != nil || header.Type != "session" || header.ID == "" {
		return "", time.Time{}, false
	}
	if header.CWD != "" && filepath.Clean(header.CWD) != filepath.Clean(cwd) {
		return "", time.Time{}, false
	}
	started, _ := time.Parse(time.RFC3339Nano, header.Timestamp)
	return header.ID, started, true
}

func piPrompt(line []byte) (string, time.Time, bool) {
	if !bytes.Contains(line, []byte(`"user"`)) {
		return "", time.Time{}, false
	}
	var entry piEntry
	if json.Unmarshal(line, &entry) != nil || entry.Type != "message" || entry.Message == nil || entry.Message.Role != "user" {
		return "", time.Time{}, false
	}
	text := strings.TrimSpace(piText(entry.Message.Content))
	if text == "" {
		return "", time.Time{}, false
	}
	return text, piTimestamp(entry.Timestamp, entry.Message.Timestamp), true
}

// maxForkDepth bounds how many forks back scanCodexLog follows.
const maxForkDepth = 16

// scanCodexLog reads a Codex session log. A fork's log holds only what
// happened since: its session_meta names the thread it forked
// (forked_from_id) and how much of that thread's log it continues
// (forked_from_ordinal_exclusive), so those lines come first, after the
// fork's own session_meta line.
func scanCodexLog(path string, fn func(line []byte) bool) bool {
	return scanCodexFork(path, -1, 0, fn)
}

// scanCodexFork reads the log at path up to the line numbered before
// (every line when negative); depth counts the forks followed. Only the
// first log's session_meta line is passed on.
func scanCodexFork(path string, before int64, depth int, fn func(line []byte) bool) bool {
	first, stopped := true, false
	ok := scanLog(path, func(line []byte) bool {
		var entry struct {
			Ordinal *int64 `json:"ordinal"`
			Type    string `json:"type"`
			Payload struct {
				ForkedFromID string `json:"forked_from_id"`
				ForkedBefore *int64 `json:"forked_from_ordinal_exclusive"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(line, &entry)
		if before >= 0 && entry.Ordinal != nil && *entry.Ordinal >= before {
			return false
		}
		isMeta := first && entry.Type == "session_meta"
		first = false
		if isMeta && depth > 0 {
			// The forked thread's meta: the fork's own came first.
			return true
		}
		if isMeta {
			if !fn(line) {
				stopped = true
				return false
			}
		}
		if isMeta && entry.Payload.ForkedFromID != "" && depth < maxForkDepth {
			if parent, found := codexLogOf(entry.Payload.ForkedFromID); found {
				limit := int64(-1)
				if entry.Payload.ForkedBefore != nil {
					limit = *entry.Payload.ForkedBefore
				}
				scanCodexFork(parent, limit, depth+1, func(line []byte) bool {
					if !fn(line) {
						stopped = true
						return false
					}
					return true
				})
				if stopped {
					return false
				}
			}
			return true
		}
		if isMeta {
			return true
		}
		if !fn(line) {
			stopped = true
			return false
		}
		return true
	})
	return ok
}

// codexLogOf finds the log of a Codex thread: its file is named after it
// (rollout-<time>-<thread id>.jsonl).
func codexLogOf(threadID string) (string, bool) {
	if threadID == "" || strings.ContainsAny(threadID, `/\*?[`) {
		return "", false
	}
	root, err := codexSessionsRoot("")
	if err != nil {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}
