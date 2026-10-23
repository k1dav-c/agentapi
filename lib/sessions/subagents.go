package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agenthome"
	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/transcript"
)

// ErrSubagentNotFound is returned for a subagent the session has no log of.
var ErrSubagentNotFound = errors.New("no such subagent in this session")

// Subagent is a subagent the agent ran in a session, as its log says.
type Subagent struct {
	ID string `json:"id" doc:"The subagent's identifier."`
	// Description and AgentType are Claude Code's.
	Description string `json:"description,omitempty" doc:"What the agent asked the subagent to do, in a few words (Claude Code)."`
	AgentType   string `json:"agent_type,omitempty" doc:"The kind of subagent, e.g. general-purpose (Claude Code)."`
	// ParentToolUseID is the Agent tool call that started it (Claude Code).
	ParentToolUseID string    `json:"parent_tool_use_id,omitempty" doc:"The agent's tool call that started the subagent (Claude Code's Agent tool)."`
	StartedAt       time.Time `json:"started_at" doc:"When the subagent started."`
	UpdatedAt       time.Time `json:"updated_at" doc:"When its log last changed."`
	// LogStatus is how far the subagent got as its own log says (Codex:
	// running, completed or failed), or empty when it doesn't.
	LogStatus string `json:"-"`
	path      string
	// kind is the agent type whose log it is.
	kind string
	// from is the first line of the log that is the subagent's own: Codex
	// starts a subagent's log with a copy of its parent's.
	from int64
}

// lines calls fn with the subagent's own lines of its log.
func (s Subagent) lines(fn func(line []byte) bool) {
	agentlog.ScanFile(s.path, func(line []byte) bool {
		if s.from > 0 {
			var entry struct {
				Ordinal *int64 `json:"ordinal"`
				Type    string `json:"type"`
			}
			if json.Unmarshal(line, &entry) == nil && (entry.Type == "session_meta" || (entry.Ordinal != nil && *entry.Ordinal < s.from)) {
				return true
			}
		}
		return fn(line)
	})
}

// SubagentRun is what a subagent did: the task it was given and its
// messages, as rich messages.
type SubagentRun struct {
	Prompt   string
	Messages []transcript.RichMessage
}

// Subagents lists the subagents the agent ran in its session sessionID,
// in the order they started: Claude Code keeps a log per subagent next to
// the session's, Codex a thread whose parent is the session's. Pi runs
// none.
func (s *Store) Subagents(sessionID string) ([]Subagent, error) {
	if sessionID == "" {
		return nil, nil
	}
	var (
		subs []Subagent
		err  error
	)
	switch s.agent {
	case "claude":
		subs, err = s.claudeSubagents(sessionID)
	case "codex":
		subs, err = s.codexSubagents(sessionID)
	}
	slices.SortStableFunc(subs, func(a, b Subagent) int { return a.StartedAt.Compare(b.StartedAt) })
	return subs, err
}

// SubagentEvents returns the timeline events of the subagents of
// Subagents, each subagent's in turn, starting with a system event. Each
// event says its subagent and, for Claude Code, the Agent tool call that
// started it.
func (s *Store) SubagentEvents(sessionID string) ([]transcript.SessionEvent, error) {
	subs, err := s.Subagents(sessionID)
	if err != nil {
		return nil, err
	}
	var all []transcript.SessionEvent
	for _, sub := range subs {
		all = append(all, sub.events()...)
	}
	return all, nil
}

// ReadSubagent reads what the subagent id of the session did. It returns
// ErrSubagentNotFound for one the session has no log of.
func (s *Store) ReadSubagent(sessionID, id string) (Subagent, SubagentRun, error) {
	subs, err := s.Subagents(sessionID)
	if err != nil {
		return Subagent{}, SubagentRun{}, err
	}
	i := slices.IndexFunc(subs, func(s Subagent) bool { return s.ID == id })
	if i < 0 {
		return Subagent{}, SubagentRun{}, ErrSubagentNotFound
	}
	return subs[i], subs[i].run(), nil
}

// parser returns the line parsers of the subagent's log.
func (s Subagent) parser() (agentlog.LineParser, agentlog.SessionEventParser, func(line []byte) (string, time.Time, bool)) {
	if s.kind == "codex" {
		return codexlog.NewParser(), codexlog.NewEventParser(), codexlog.Prompt
	}
	return claudelog.NewParser(), claudelog.NewEventParser(), func(line []byte) (string, time.Time, bool) { return claudelog.UserText(line, true) }
}

// run reads the subagent's task and messages. The messages' IDs start with
// the subagent's, so they can't be taken for the agent's own.
func (s Subagent) run() SubagentRun {
	parser, _, prompt := s.parser()
	var (
		run    SubagentRun
		order  []string
		latest = make(map[string]transcript.RichMessage)
	)
	add := func(msgs []transcript.RichMessage) {
		for _, m := range msgs {
			if m.MessageID == "" || (m.Role == "user" && !hasToolResult(m)) {
				continue
			}
			m.MessageID = "subagent-" + s.ID + "-" + m.MessageID
			key := m.MessageID + "\x00" + m.Role
			if _, seen := latest[key]; !seen {
				order = append(order, key)
			}
			latest[key] = m
		}
	}
	s.lines(func(line []byte) bool {
		if run.Prompt == "" {
			if text, _, ok := prompt(line); ok {
				run.Prompt = text
			}
		}
		if msgs, err := parser.ParseLine(line); err == nil {
			add(msgs)
		}
		return true
	})
	add(parser.Flush())
	for _, key := range order {
		run.Messages = append(run.Messages, latest[key])
	}
	return run
}

// events reads the subagent's log into timeline events tagged with it.
func (s Subagent) events() []transcript.SessionEvent {
	_, parser, _ := s.parser()
	var events []transcript.SessionEvent
	s.lines(func(line []byte) bool {
		if parsed, err := parser.ParseSessionEvents(line); err == nil {
			events = append(events, parsed...)
		}
		return true
	})
	if len(events) == 0 {
		return nil
	}
	start := "subagent started"
	if d := s.label(); d != "" {
		start += ": " + d
	}
	events = slices.Insert(events, 0, transcript.SessionEvent{Kind: "system", Role: agentlog.Ptr("system"), Content: &start, EventTime: events[0].EventTime})
	for i := range events {
		events[i].SubagentID = agentlog.Ptr(s.ID)
		if s.ParentToolUseID != "" {
			events[i].ParentToolUseID = agentlog.Ptr(s.ParentToolUseID)
		}
	}
	return events
}

// label is the description with the kind of subagent.
func (s Subagent) label() string {
	if s.AgentType == "" {
		return s.Description
	}
	return strings.TrimSpace(fmt.Sprintf("%s (%s)", s.Description, s.AgentType))
}

// claudeSubagents reads <projects>/<cwd>/<session>/subagents: an
// agent-<id>.jsonl log per subagent, with agent-<id>.meta.json saying what
// it is and which tool call started it.
func (s *Store) claudeSubagents(sessionID string) ([]Subagent, error) {
	configDir, err := agenthome.ClaudeConfigDir()
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == ".." {
		return nil, fmt.Errorf("invalid session id %q", sessionID)
	}
	files, err := logsIn(filepath.Join(configDir, "projects", claudelog.EncodeCWD(s.cwd), sessionID, "subagents"))
	if err != nil {
		return nil, err
	}
	var subs []Subagent
	for _, path := range files {
		snap, err := s.readSubagentLog(path, false)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		var meta struct {
			Description string `json:"description"`
			AgentType   string `json:"agentType"`
			ToolUseID   string `json:"toolUseId"`
		}
		if data, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
			_ = json.Unmarshal(data, &meta)
		}
		subs = append(subs, Subagent{
			ID:              strings.TrimPrefix(name, "agent-"),
			Description:     meta.Description,
			AgentType:       meta.AgentType,
			ParentToolUseID: meta.ToolUseID,
			StartedAt:       snap.firstAt,
			UpdatedAt:       snap.info.ModTime,
			path:            path,
			kind:            "claude",
		})
	}
	return subs, nil
}

// codexSubagents finds the threads whose parent is the session's thread
// among Codex's session logs.
func (s *Store) codexSubagents(threadID string) ([]Subagent, error) {
	threads, err := s.codexThreads()
	if err != nil {
		return nil, err
	}
	var subs []Subagent
	for _, t := range threads {
		meta := t.meta
		if !meta.IsSubAgent() || meta.Payload.ID == threadID {
			continue
		}
		if meta.Payload.ParentThreadID != threadID && meta.Payload.SessionID != threadID {
			continue
		}
		snap, err := s.readSubagentLog(t.path, false)
		if err != nil {
			continue
		}
		started, _ := time.Parse(time.RFC3339Nano, meta.Timestamp)
		sub := Subagent{
			ID:          meta.Payload.ID,
			Description: strings.ReplaceAll(filepath.Base(meta.Payload.AgentPath), "_", " "),
			AgentType:   meta.Payload.AgentNickname,
			StartedAt:   started,
			UpdatedAt:   snap.info.ModTime,
			LogStatus:   snap.status,
			path:        t.path,
			kind:        "codex",
			from:        meta.Payload.HistoryStart,
		}
		if meta.Payload.AgentPath == "" {
			sub.Description = ""
		}
		subs = append(subs, sub)
	}
	return subs, nil
}
