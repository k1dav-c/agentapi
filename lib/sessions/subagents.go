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
		if s.from > 0 && !codexOwnLine(line, s.from) {
			return true
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
	if !ValidID(sessionID) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidID, sessionID)
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
		all = append(all, sub.tagged(s.subagentEvents(sub))...)
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
	return subs[i], s.subagentRun(subs[i]), nil
}

// subagentView is what was read of a subagent's log for its timeline
// events and its run, as the log was at size and modified: the timeline
// and the subagent are asked for on every turn, of logs that are mostly
// done growing. It is read again once the log changed.
type subagentView struct {
	size      int64
	modified  time.Time
	events    []transcript.SessionEvent
	hasEvents bool
	run       SubagentRun
	hasRun    bool
}

// Line reads none of the log: the view reads it whole when asked for it.
func (v *subagentView) Line([]byte) bool { return false }

// Size is about how many bytes of memory the view holds: about as many
// as the log has, for each of the events and the run.
func (v *subagentView) Size() int {
	n := 256
	if v.hasEvents {
		n += int(v.size)
	}
	if v.hasRun {
		n += int(v.size)
	}
	return n
}

// withView calls fn with the view of sub's log, up to date with it, while
// no other caller uses it; false if the log can't be read.
func (s *Store) withView(sub Subagent, fn func(*subagentView)) bool {
	return s.views.Read(sub.path, func(v *subagentView, info agentlog.FileInfo) {
		if v.size != info.Size || !v.modified.Equal(info.ModTime) {
			*v = subagentView{size: info.Size, modified: info.ModTime}
		}
		fn(v)
	}) == nil
}

// subagentEvents returns the timeline events of sub's log, untagged; the
// caller may change them, not what they point to.
func (s *Store) subagentEvents(sub Subagent) []transcript.SessionEvent {
	var events []transcript.SessionEvent
	if !s.withView(sub, func(v *subagentView) {
		if !v.hasEvents {
			v.events, v.hasEvents = slices.Clip(sub.events()), true
		}
		events = slices.Clone(v.events)
	}) {
		return sub.events()
	}
	return events
}

// subagentRun returns what sub did, which the caller may change: its
// messages and their content blocks are copies.
func (s *Store) subagentRun(sub Subagent) SubagentRun {
	var run SubagentRun
	if !s.withView(sub, func(v *subagentView) {
		if !v.hasRun {
			v.run, v.hasRun = sub.run(), true
		}
		run = SubagentRun{Prompt: v.run.Prompt, Messages: slices.Clone(v.run.Messages)}
	}) {
		return sub.run()
	}
	for i := range run.Messages {
		run.Messages[i].Content = slices.Clone(run.Messages[i].Content)
	}
	return run
}

// parser returns the line parsers of the subagent's log.
func (s Subagent) parser() (agentlog.LineParser, agentlog.SessionEventParser, func(line []byte) (string, time.Time, bool)) {
	if s.kind == "codex" {
		return codexlog.Format.NewParser(), codexlog.Format.NewEventParser(), codexlog.Prompt
	}
	return claudelog.Format.NewParser(), claudelog.Format.NewEventParser(), func(line []byte) (string, time.Time, bool) { return claudelog.UserText(line, true) }
}

// run reads the subagent's task and messages. The messages' IDs start with
// the subagent's, so they can't be taken for the agent's own.
func (s Subagent) run() SubagentRun {
	parser, _, prompt := s.parser()
	var run SubagentRun
	rich := newRichLog("subagent-" + s.ID + "-")
	s.lines(func(line []byte) bool {
		if run.Prompt == "" {
			if text, _, ok := prompt(line); ok {
				run.Prompt = text
			}
		}
		if msgs, err := parser.ParseLine(line); err == nil {
			rich.add(msgs)
		}
		return true
	})
	rich.add(parser.Flush())
	run.Messages = rich.messages()
	return run
}

// events reads the subagent's log into timeline events.
func (s Subagent) events() []transcript.SessionEvent {
	_, parser, _ := s.parser()
	var events []transcript.SessionEvent
	s.lines(func(line []byte) bool {
		if parsed, err := parser.ParseSessionEvents(line); err == nil {
			events = append(events, parsed...)
		}
		return true
	})
	return events
}

// tagged returns events, the subagent's, tagged with it after a system
// event that says it started.
func (s Subagent) tagged(events []transcript.SessionEvent) []transcript.SessionEvent {
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

// claudeSubagents reads <session>/subagents next to the session's log
// (<session>.jsonl): an agent-<id>.jsonl log per subagent, with
// agent-<id>.meta.json saying what it is and which tool call started it.
func (s *Store) claudeSubagents(sessionID string) (subs []Subagent, err error) {
	// Next to the session's log; before Claude Code wrote it, where it
	// would. Another directory's session has none here.
	var dirs []string
	if path, ok := s.find(sessionID); ok {
		dirs = []string{filepath.Dir(path)}
	} else if own, _ := claudeLogsByID(s, sessionID); len(own) == 0 {
		if dirs, err = claudeProjectDirs(s); err != nil {
			return nil, err
		}
	}
	var files []string
	for _, dir := range dirs {
		logs, err := logsIn(filepath.Join(dir, sessionID, "subagents"))
		if err != nil {
			return nil, err
		}
		files = append(files, logs...)
	}
	for _, path := range files {
		snap, err := s.readSubagentLog(s.subs, path, false)
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
		snap, err := s.readSubagentLog(s.subs, t.path, false)
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
