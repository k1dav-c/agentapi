package jsonlwatcher

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Sub-agent lifecycle states.
const (
	SubAgentRunning     = "running"
	SubAgentCompleted   = "completed"
	SubAgentInterrupted = "interrupted"
	SubAgentFailed      = "failed"
)

// SubAgent summarizes a sub-agent spawned during the agent's session.
type SubAgent struct {
	ThreadID       string    `json:"thread_id" doc:"Sub-agent thread identifier"`
	ParentThreadID string    `json:"parent_thread_id" doc:"Thread that spawned this sub-agent"`
	Path           string    `json:"path" doc:"Sub-agent path in the agent tree, e.g. /root/worker"`
	Nickname       string    `json:"nickname,omitempty" doc:"Display nickname assigned by the agent"`
	Role           string    `json:"role,omitempty" doc:"Sub-agent role, when one was requested"`
	Depth          int       `json:"depth" doc:"Nesting depth; direct children of the main agent have depth 1"`
	Status         string    `json:"status" enum:"running,completed,interrupted,failed" doc:"Lifecycle state of the sub-agent's current or last turn"`
	Activity       string    `json:"activity,omitempty" doc:"What the sub-agent is doing right now, e.g. the shell command it is running"`
	LastMessage    string    `json:"last_message,omitempty" doc:"Most recent message the sub-agent produced"`
	TotalTokens    int       `json:"total_tokens,omitempty" doc:"Total tokens used by the sub-agent"`
	StartedAt      time.Time `json:"started_at" doc:"When the sub-agent was spawned"`
	UpdatedAt      time.Time `json:"updated_at" doc:"Time of the sub-agent's latest recorded activity"`
}

// CodexAgentTracker follows the sub-agents of the Codex session found by a
// CodexResolver. Every sub-agent writes its own rollout file whose metadata
// names the session's root thread, so the tracker scans the session
// directories for those files and tails each of them. Nested sub-agents are
// found the same way.
type CodexAgentTracker struct {
	resolver *CodexResolver
	// metas caches session metadata by file path. Metadata never changes
	// once written, so every file is decoded at most once.
	metas  map[string]*codexSessionMeta
	agents map[string]*trackedAgent
	last   []SubAgent
}

type trackedAgent struct {
	agent  SubAgent
	offset int64
	// line is the index of the next line to read.
	line         int
	historyStart int
}

// NewCodexAgentTracker creates a tracker for the session that resolver finds.
func NewCodexAgentTracker(resolver *CodexResolver) *CodexAgentTracker {
	return &CodexAgentTracker{
		resolver: resolver,
		metas:    make(map[string]*codexSessionMeta),
		agents:   make(map[string]*trackedAgent),
		last:     []SubAgent{},
	}
}

// Run polls for sub-agent changes until ctx is canceled, calling onChange
// with the full list whenever it differs from the previous poll.
func (t *CodexAgentTracker) Run(ctx context.Context, interval time.Duration, onChange func([]SubAgent)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if agents, changed := t.Poll(); changed {
			onChange(agents)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Poll scans for sub-agent files, reads their new records, and returns the
// current sub-agents ordered by spawn time. changed reports whether the list
// differs from the previous Poll.
func (t *CodexAgentTracker) Poll() (agents []SubAgent, changed bool) {
	sessionID := t.sessionID()
	if sessionID != "" {
		t.discover(sessionID)
	}
	agents = make([]SubAgent, 0, len(t.agents))
	for path, tracked := range t.agents {
		tracked.readNew(path)
		agents = append(agents, tracked.agent)
	}
	sort.Slice(agents, func(i, j int) bool {
		if !agents[i].StartedAt.Equal(agents[j].StartedAt) {
			return agents[i].StartedAt.Before(agents[j].StartedAt)
		}
		return agents[i].Path < agents[j].Path
	})
	changed = !reflect.DeepEqual(agents, t.last)
	t.last = agents
	return agents, changed
}

// sessionID returns the root thread ID of the resolved main session.
func (t *CodexAgentTracker) sessionID() string {
	path, err := t.resolver.Resolve()
	if err != nil {
		return ""
	}
	meta := t.meta(path)
	if meta == nil {
		return ""
	}
	return meta.Payload.SessionID
}

func (t *CodexAgentTracker) meta(path string) *codexSessionMeta {
	if meta, ok := t.metas[path]; ok {
		return meta
	}
	meta, err := readCodexSessionMeta(path)
	if err != nil {
		// Possibly still being written; retry on the next poll.
		return nil
	}
	t.metas[path] = meta
	return meta
}

// discover starts tracking sub-agent files that belong to sessionID.
func (t *CodexAgentTracker) discover(sessionID string) {
	root, err := codexSessionsRoot(t.resolver.SessionsDir)
	if err != nil {
		return
	}
	for _, dir := range codexSessionDirs(root, time.Now()) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if _, ok := t.agents[path]; ok {
				continue
			}
			meta := t.meta(path)
			if meta == nil || !meta.isSubAgent() || meta.Payload.SessionID != sessionID {
				continue
			}
			t.agents[path] = newTrackedAgent(meta)
		}
	}
}

func newTrackedAgent(meta *codexSessionMeta) *trackedAgent {
	p := meta.Payload
	agent := SubAgent{
		ThreadID:       p.ID,
		ParentThreadID: p.ParentThreadID,
		Path:           p.AgentPath,
		Nickname:       p.AgentNickname,
		Depth:          1,
		Status:         SubAgentRunning,
	}
	if spawn := meta.subAgentSpawn(); spawn != nil {
		agent.Depth = spawn.Depth
		if spawn.AgentRole != nil {
			agent.Role = *spawn.AgentRole
		}
	}
	if agent.Path == "" {
		agent.Path = p.ID
	}
	if started, err := time.Parse(time.RFC3339Nano, meta.Timestamp); err == nil {
		agent.StartedAt = started
		agent.UpdatedAt = started
	}
	return &trackedAgent{agent: agent, historyStart: p.HistoryStart}
}

// readNew applies the complete lines appended since the last read.
func (a *trackedAgent) readNew(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(a.offset, io.SeekStart); err != nil {
		return
	}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			// A partial line is re-read once it is complete.
			return
		}
		if err != nil {
			return
		}
		a.offset += int64(len(line))
		index := a.line
		a.line++
		// Line 0 is the sub-agent's own metadata, followed by a copy of
		// the parent's history up to historyStart.
		if index < a.historyStart {
			continue
		}
		a.apply(line)
	}
}

// codexAgentEvent holds the event_msg fields the tracker uses.
type codexAgentEvent struct {
	Type             string  `json:"type"`
	LastAgentMessage *string `json:"last_agent_message"`
	Reason           string  `json:"reason"`
	Message          string  `json:"message"`
	Item             *struct {
		Type    string `json:"type"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
	Info *struct {
		TotalTokenUsage *codexTokenUsage `json:"total_token_usage"`
	} `json:"info"`
}

// execCommandRe extracts the shell command from the exec tool's script,
// e.g. tools.exec_command({cmd:"sleep 300",...}).
var execCommandRe = regexp.MustCompile(`exec_command\(\{\s*cmd\s*:\s*("(?:[^"\\]|\\.)*")`)

func (a *trackedAgent) apply(line []byte) {
	var entry codexLine
	if err := json.Unmarshal(line, &entry); err != nil {
		return
	}
	if ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil && ts.After(a.agent.UpdatedAt) {
		a.agent.UpdatedAt = ts
	}
	switch entry.Type {
	case "event_msg":
		var ev codexAgentEvent
		if err := json.Unmarshal(entry.Payload, &ev); err != nil {
			return
		}
		a.applyEvent(&ev)
	case "response_item":
		var payload codexPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return
		}
		a.applyResponseItem(&payload)
	}
}

func (a *trackedAgent) applyEvent(ev *codexAgentEvent) {
	switch ev.Type {
	case "task_started":
		a.agent.Status = SubAgentRunning
		a.agent.Activity = ""
	case "task_complete":
		a.agent.Status = SubAgentCompleted
		a.agent.Activity = ""
		if ev.LastAgentMessage != nil && *ev.LastAgentMessage != "" {
			a.agent.LastMessage = *ev.LastAgentMessage
		}
	case "turn_aborted":
		a.agent.Status = SubAgentInterrupted
		a.agent.Activity = ""
	case "error":
		a.agent.Status = SubAgentFailed
		a.agent.Activity = ""
		if ev.Message != "" {
			a.agent.LastMessage = ev.Message
		}
	case "item_completed":
		if ev.Item == nil || ev.Item.Type != "AgentMessage" {
			return
		}
		var parts []string
		for _, c := range ev.Item.Content {
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) > 0 {
			a.agent.LastMessage = strings.Join(parts, "\n")
		}
	case "token_count":
		if ev.Info != nil && ev.Info.TotalTokenUsage != nil {
			a.agent.TotalTokens = ev.Info.TotalTokenUsage.TotalTokens
		}
	}
}

func (a *trackedAgent) applyResponseItem(payload *codexPayload) {
	switch payload.Type {
	case "custom_tool_call":
		var script string
		if err := json.Unmarshal(payload.Input, &script); err != nil {
			script = string(payload.Input)
		}
		if match := execCommandRe.FindStringSubmatch(script); match != nil {
			var cmd string
			if err := json.Unmarshal([]byte(match[1]), &cmd); err == nil {
				a.agent.Activity = "$ " + cmd
				return
			}
		}
		// Polling a running command (write_stdin) keeps the activity of
		// the command it polls.
		if !strings.Contains(script, "write_stdin") {
			a.agent.Activity = payload.Name
		}
	case "function_call":
		// "wait" polls the command already shown as the activity.
		if payload.Name != "wait" {
			a.agent.Activity = payload.Name
		}
	}
}
