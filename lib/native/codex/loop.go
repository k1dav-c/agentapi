package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// loopState is the state of the read loop. Only the loop goroutine uses it.
type loopState struct {
	a        *Adapter
	threadID string
	model    string
	turn     *turnState
	// ended holds the turns that ended, so late notifications don't start
	// them again.
	ended map[string]bool
	// dirty lists messages with changes not sent yet.
	dirty []*msgState
}

func (s *loopState) now() time.Time { return s.a.cfg.Now() }

func (s *loopState) sink() native.Sink { return s.a.sink }

func (s *loopState) warn(message string) { s.sink().Error(message, st.ErrorLevelWarning) }

// threadOpened returns the handler of a thread/start or thread/resume
// result.
func (s *loopState) threadOpened(resumed bool) func(json.RawMessage, *rpcError) {
	return func(result json.RawMessage, rpcErr *rpcError) {
		if rpcErr != nil {
			return
		}
		var resp threadStartResponse
		if err := json.Unmarshal(result, &resp); err != nil || resp.Thread.ID == "" {
			s.a.log.Warn("Unexpected thread response from codex app-server", "error", err)
			return
		}
		th := resp.Thread
		s.threadID = th.ID
		s.model = resp.Model
		s.a.mu.Lock()
		s.a.threadID = th.ID
		s.a.mu.Unlock()
		path := ""
		if th.Path != nil {
			path = *th.Path
		}
		s.a.log.Info("Codex thread ready", "thread", th.ID, "resumed", resumed, "model", resp.Model, "rollout", path)
		s.sink().SessionRef(th.ID)
		if th.Name != nil && *th.Name != "" {
			s.sink().SessionName(*th.Name)
		}
		cwd := th.CWD
		if cwd == "" {
			cwd = resp.CWD
		}
		verb := "started"
		eventTime := time.Unix(th.CreatedAt, 0).UTC()
		if resumed || th.CreatedAt == 0 {
			eventTime = s.now()
		}
		if resumed {
			verb = "resumed"
		}
		content := fmt.Sprintf("session %s: %s (codex %s)", verb, cwd, th.CLIVersion)
		s.sink().SessionEvents([]jsonlwatcher.SessionEvent{{
			Kind: "system", Role: strPtr("system"), EventTime: eventTime, Content: &content,
			SessionID: optString(th.ID), SourceID: optString(th.ID),
		}})
	}
}

// ours reports whether a notification's thread is the adapter's thread.
// Notifications without a thread belong to it.
func (s *loopState) ours(threadID string) bool {
	return threadID == "" || s.threadID == "" || threadID == s.threadID
}

// beginTurn makes turnID the active turn, if it isn't already.
func (s *loopState) beginTurn(turnID string) *turnState {
	if s.ended[turnID] {
		return nil
	}
	if s.turn != nil {
		if s.turn.id == turnID {
			return s.turn
		}
		// A new turn without the end of the old one: end it first.
		s.completeTurn(turn{ID: s.turn.id, Status: "completed"})
	}
	t := &turnState{id: turnID, items: make(map[string]*itemState)}
	s.newSegment(t, turnID)
	s.turn = t
	s.a.mu.Lock()
	s.a.activeTurn = turnID
	s.a.mu.Unlock()
	s.sink().TurnStarted()
	return t
}

// turnFor returns the active turn if turnID is it. Items of a turn the
// adapter didn't start (the agent started it on its own) start it.
func (s *loopState) turnFor(turnID string) *turnState {
	if s.turn != nil && (turnID == "" || s.turn.id == turnID) {
		return s.turn
	}
	if turnID == "" || s.ended[turnID] {
		return nil
	}
	return s.beginTurn(turnID)
}

func (s *loopState) newSegment(t *turnState, id string) *segment {
	seg := &segment{id: id, timestamp: s.now()}
	seg.build = func() (jsonlwatcher.RichMessage, bool) { return seg.snapshot(s.model) }
	if n := len(t.segments); n > 0 {
		seg.usage = t.segments[n-1].usage
	}
	t.segments = append(t.segments, seg)
	return seg
}

func (s *loopState) interruptSent(turnID string) {
	if s.turn != nil && s.turn.id == turnID && s.turn.interrupted.IsZero() {
		s.turn.interrupted = s.now()
	}
}

// --- snapshots ---

// touch sends m now, or marks it to be sent at the end of the snapshot
// interval when it was sent recently and urgent is false.
func (s *loopState) touch(m *msgState, urgent bool) {
	if urgent || s.now().Sub(m.lastEmit) >= s.a.cfg.SnapshotInterval {
		s.emit(m)
		return
	}
	if !m.dirty {
		m.dirty = true
		s.dirty = append(s.dirty, m)
	}
}

func (s *loopState) emit(m *msgState) {
	m.dirty = false
	msg, ok := m.build()
	if !ok {
		return
	}
	m.lastEmit = s.now()
	s.sink().RichMessage(msg)
}

// flush sends the pending changes; all of them when force is set,
// otherwise those whose interval is over.
func (s *loopState) flush(force bool) {
	kept := s.dirty[:0]
	now := s.now()
	for _, m := range s.dirty {
		if !m.dirty {
			continue
		}
		if force || now.Sub(m.lastEmit) >= s.a.cfg.SnapshotInterval {
			s.emit(m)
			continue
		}
		kept = append(kept, m)
	}
	s.dirty = kept
}

// nextWake is when the loop has to act without a message: send a pending
// snapshot or give up waiting for an interrupted turn.
func (s *loopState) nextWake() time.Time {
	var next time.Time
	for _, m := range s.dirty {
		if t := m.lastEmit.Add(s.a.cfg.SnapshotInterval); next.IsZero() || t.Before(next) {
			next = t
		}
	}
	if s.turn != nil && !s.turn.interrupted.IsZero() {
		if t := s.turn.interrupted.Add(s.a.cfg.InterruptTimeout); next.IsZero() || t.Before(next) {
			next = t
		}
	}
	return next
}

func (s *loopState) wake() {
	s.flush(false)
	if t := s.turn; t != nil && !t.interrupted.IsZero() && s.now().Sub(t.interrupted) >= s.a.cfg.InterruptTimeout {
		s.a.log.Warn("Codex did not end the interrupted turn; ending it here", "turn", t.id)
		s.completeTurn(turn{ID: t.id, Status: "interrupted"})
	}
}

// --- notifications ---

func (s *loopState) notification(msg rpcMessage) {
	switch msg.Method {
	case "turn/started":
		var n turnNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && n.Turn.ID != "" {
			s.beginTurn(n.Turn.ID)
		}
	case "turn/completed":
		var n turnNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && s.turn != nil && s.turn.id == n.Turn.ID {
			s.completeTurn(n.Turn)
		}
	case "item/started", "item/completed":
		var n itemNotification
		if !decode(msg, &n) || !s.ours(n.ThreadID) {
			return
		}
		t := s.turnFor(n.TurnID)
		if t == nil {
			return
		}
		at := msTime(n.StartedAtMs)
		if msg.Method == "item/completed" {
			at = msTime(n.CompletedAtMs)
		}
		if at.IsZero() {
			at = msTime(msg.EmittedAtMs)
		}
		if msg.Method == "item/started" {
			s.itemStarted(t, &n.Item, at)
		} else {
			s.itemCompleted(t, &n.Item, at)
		}
	case "item/agentMessage/delta", "item/plan/delta":
		if it, d := s.deltaItem(msg); it != nil && it.blk != nil {
			it.blk.Text += d.Delta
			s.touch(&it.seg.msgState, false)
		}
	case "item/reasoning/summaryPartAdded", "item/reasoning/summaryTextDelta":
		if it, d := s.deltaItem(msg); it != nil && it.blk != nil {
			for len(it.blk.summary) <= d.SummaryIndex {
				it.blk.summary = append(it.blk.summary, "")
			}
			it.blk.summary[d.SummaryIndex] += d.Delta
			it.blk.Thinking = joinReasoning(it.blk.summary, it.blk.content)
			s.touch(&it.seg.msgState, false)
		}
	case "item/reasoning/textDelta":
		if it, d := s.deltaItem(msg); it != nil && it.blk != nil {
			for len(it.blk.content) <= d.ContentIndex {
				it.blk.content = append(it.blk.content, "")
			}
			it.blk.content[d.ContentIndex] += d.Delta
			it.blk.Thinking = joinReasoning(it.blk.summary, it.blk.content)
			s.touch(&it.seg.msgState, false)
		}
	case "item/commandExecution/outputDelta":
		// The deltas miss output (the start of a command, all of a short
		// one), so they are only a live preview; the completed item has
		// the whole output.
		if it, d := s.deltaItem(msg); it != nil && it.blk != nil && !it.done {
			it.output.WriteString(d.Delta)
			isError := false
			it.resultBlock = jsonlwatcher.RichContentBlock{
				Type: "tool_result", ToolUseID: it.id, Text: it.output.String(), Status: "running", IsError: &isError,
			}
			s.touch(s.resultMsg(it), false)
		}
	case "thread/tokenUsage/updated":
		var n threadTokenUsageUpdatedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && s.turn != nil && s.turn.id == n.TurnID {
			seg := s.turn.current()
			seg.usage = &jsonlwatcher.Usage{
				InputTokens:          n.TokenUsage.Last.InputTokens,
				OutputTokens:         n.TokenUsage.Last.OutputTokens,
				CacheReadInputTokens: n.TokenUsage.Last.CachedInputTokens,
			}
			s.touch(&seg.msgState, false)
		}
	case "turn/plan/updated":
		var n turnPlanUpdatedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) {
			if t := s.turnFor(n.TurnID); t != nil {
				s.planUpdated(t, n)
			}
		}
	case "error":
		var n errorNotification
		if !decode(msg, &n) || !s.ours(n.ThreadID) {
			return
		}
		text := turnErrorText(&n.Error)
		switch {
		case n.WillRetry:
			s.warn(text)
		case s.turn != nil && (n.TurnID == "" || n.TurnID == s.turn.id):
			s.turn.failure = text
			if isAuthError(&n.Error) {
				s.reportLogin(s.turn)
			}
		default:
			if isAuthError(&n.Error) {
				s.reportLogin(nil)
			}
			s.sink().Error(text, st.ErrorLevelError)
		}
	case "thread/name/updated":
		var n threadNameUpdatedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && n.ThreadName != nil {
			s.sink().SessionName(*n.ThreadName)
		}
	case "thread/status/changed":
		var n threadStatusChangedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && n.Status.Type == "systemError" {
			s.sink().Error("Codex reported a system error for the thread", st.ErrorLevelError)
		}
	case "serverRequest/resolved":
		var n serverRequestResolvedNotification
		if decode(msg, &n) {
			s.requestResolved(string(n.RequestID))
		}
	case "warning":
		var n warningNotification
		if decode(msg, &n) && (n.ThreadID == nil || s.ours(*n.ThreadID)) && n.Message != "" {
			s.warn(n.Message)
		}
	case "configWarning":
		var n configWarningNotification
		if decode(msg, &n) && n.Summary != "" {
			text := n.Summary
			if n.Details != nil && *n.Details != "" {
				text += "\n" + *n.Details
			}
			s.warn(text)
		}
	case "mcpServer/startupStatus/updated":
		var n mcpServerStatusUpdatedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && (n.Status == "failed" || (n.Error != nil && *n.Error != "")) {
			reason := n.Status
			if n.Error != nil && *n.Error != "" {
				reason = *n.Error
			}
			s.warn(fmt.Sprintf("MCP server %s failed to start: %s", n.Name, reason))
		}
	case "model/rerouted":
		var n modelReroutedNotification
		if decode(msg, &n) && s.ours(n.ThreadID) && n.ToModel != "" {
			s.model = n.ToModel
		}
	default:
		s.a.log.Debug("Codex notification", "method", msg.Method)
	}
}

func decode(msg rpcMessage, v any) bool {
	return len(msg.Params) > 0 && json.Unmarshal(msg.Params, v) == nil
}

func turnErrorText(e *turnError) string {
	text := e.Message
	if e.AdditionalDetails != nil && *e.AdditionalDetails != "" {
		text += "\n" + *e.AdditionalDetails
	}
	return text
}

// deltaItem returns the open item a delta notification is for.
func (s *loopState) deltaItem(msg rpcMessage) (*itemState, deltaNotification) {
	var d deltaNotification
	if !decode(msg, &d) || !s.ours(d.ThreadID) || s.turn == nil || (d.TurnID != "" && d.TurnID != s.turn.id) {
		return nil, d
	}
	return s.turn.items[d.ItemID], d
}

// --- items ---

func (s *loopState) itemStarted(t *turnState, it *threadItem, at time.Time) {
	if existing := t.items[it.ID]; existing != nil {
		return
	}
	switch it.Type {
	case "userMessage":
		if t.promptSeen {
			// A steered message: what follows answers it.
			s.flush(true)
			s.newSegment(t, fmt.Sprintf("%s-%d", t.id, len(t.segments)))
		}
		t.promptSeen = true
		s.addItem(t, it, nil, at)
	case "agentMessage", "plan":
		s.addItem(t, it, &block{RichContentBlock: jsonlwatcher.RichContentBlock{Type: "text", Text: it.Text}}, at)
	case "reasoning":
		b := &block{RichContentBlock: jsonlwatcher.RichContentBlock{Type: "thinking"}, summary: it.Summary, content: it.reasoningContent()}
		b.Thinking = joinReasoning(b.summary, b.content)
		s.addItem(t, it, b, at)
	default:
		name, input, ok := toolUse(it)
		if !ok {
			s.a.log.Debug("Ignoring Codex item", "type", it.Type)
			return
		}
		state := s.addItem(t, it, &block{RichContentBlock: jsonlwatcher.RichContentBlock{
			Type: "tool_use", ToolUseID: it.ID, ToolName: name, ToolInput: input, Status: "running",
		}}, at)
		s.toolCallEvent(state.blk, at, it.ID)
	}
}

// addItem records an item and adds its block to the current segment.
func (s *loopState) addItem(t *turnState, it *threadItem, b *block, at time.Time) *itemState {
	seg := t.current()
	state := &itemState{id: it.ID, kind: it.Type, seg: seg, blk: b, startedAt: at, changes: it.Changes, server: it.Server}
	t.items[it.ID] = state
	t.order = append(t.order, state)
	if b != nil {
		seg.blocks = append(seg.blocks, b)
		s.touch(&seg.msgState, true)
	}
	return state
}

func (s *loopState) itemCompleted(t *turnState, it *threadItem, at time.Time) {
	state := t.items[it.ID]
	if state == nil {
		// Completed without a start: add it now.
		s.itemStarted(t, it, at)
		if state = t.items[it.ID]; state == nil {
			if it.Type == "contextCompaction" {
				s.systemEvent("context compacted", at, it.ID)
			}
			return
		}
	}
	if state.done {
		return
	}
	state.done = true
	switch it.Type {
	case "userMessage":
		text := strings.Join(it.userTexts(), "\n")
		if text == "" {
			return
		}
		s.sink().RichMessage(jsonlwatcher.RichMessage{
			MessageID: it.ID, Role: "user", Timestamp: s.now(),
			Content: []jsonlwatcher.RichContentBlock{{Type: "text", Text: text}},
		})
		s.textEvent("text", "user", text, at, it.ID)
	case "agentMessage", "plan":
		state.blk.Text = it.Text
		s.touch(&state.seg.msgState, true)
		if strings.TrimSpace(it.Text) != "" {
			s.textEvent("text", "assistant", it.Text, at, it.ID)
		}
	case "reasoning":
		state.blk.summary, state.blk.content = it.Summary, it.reasoningContent()
		state.blk.Thinking = joinReasoning(state.blk.summary, state.blk.content)
		s.touch(&state.seg.msgState, true)
		if state.blk.Thinking != "" {
			s.textEvent("thinking", "assistant", state.blk.Thinking, at, it.ID)
		}
	default:
		if name, input, ok := toolUse(it); ok {
			// The completed item is authoritative (e.g. the final command).
			state.blk.ToolName, state.blk.ToolInput = name, input
			state.changes = it.Changes
		}
		result := toolResult(it)
		s.finishTool(state, result, at)
	}
}

// finishTool sets a tool call's final status and sends its result.
func (s *loopState) finishTool(state *itemState, result jsonlwatcher.RichContentBlock, at time.Time) {
	state.done = true
	state.blk.Status = result.Status
	s.touch(&state.seg.msgState, true)
	state.resultBlock = result
	s.touch(s.resultMsg(state), true)
	content := result.Text
	s.sink().SessionEvents([]jsonlwatcher.SessionEvent{{
		Kind: "tool_result", EventTime: at, Content: &content,
		SessionID: optString(s.threadID), ToolUseID: optString(state.id),
	}})
}

// resultMsg returns the tool result message of an item.
func (s *loopState) resultMsg(state *itemState) *msgState {
	if state.result == nil {
		ts := s.now()
		state.result = &msgState{}
		state.result.build = func() (jsonlwatcher.RichMessage, bool) {
			return jsonlwatcher.RichMessage{
				MessageID: "result-" + state.id,
				Role:      "user",
				Timestamp: ts,
				Content:   []jsonlwatcher.RichContentBlock{state.resultBlock},
			}, true
		}
	}
	return state.result
}

// planUpdated shows the todo list (update_plan) as one tool call per
// turn, replaced on every update.
func (s *loopState) planUpdated(t *turnState, n turnPlanUpdatedNotification) {
	id := "plan-" + t.id
	in := map[string]any{"plan": n.Plan}
	if n.Plan == nil {
		in["plan"] = []turnPlanStep{}
	}
	if n.Explanation != nil && *n.Explanation != "" {
		in["explanation"] = *n.Explanation
	}
	input := mustJSON(in)
	state := t.items[id]
	if state == nil {
		state = s.addItem(t, &threadItem{ID: id, Type: "plan/update"}, &block{RichContentBlock: jsonlwatcher.RichContentBlock{
			Type: "tool_use", ToolUseID: id, ToolName: "update_plan", ToolInput: input,
		}}, s.now())
	}
	state.blk.ToolInput = input
	s.toolCallEvent(state.blk, s.now(), id)
	isError := false
	s.finishTool(state, jsonlwatcher.RichContentBlock{
		Type: "tool_result", ToolUseID: id, Text: "Plan updated", Status: "completed", IsError: &isError,
	}, s.now())
}

// completeTurn ends the active turn: it closes the items app-server left
// open (an interrupt leaves them so), sends the final snapshots and
// reports the end.
func (s *loopState) completeTurn(tr turn) {
	t := s.turn
	if t == nil {
		return
	}
	s.a.mu.Lock()
	interrupting := s.a.interrupting[t.id]
	delete(s.a.interrupting, t.id)
	s.a.mu.Unlock()

	status := tr.Status
	if interrupting && status == "completed" {
		status = "interrupted"
	}
	result := native.TurnResult{}
	switch status {
	case "interrupted":
		result.StopReason = "interrupted"
	case "failed":
		result.StopReason = "error"
		result.Error = t.failure
		if tr.Error != nil {
			result.Error = turnErrorText(tr.Error)
			if isAuthError(tr.Error) {
				s.reportLogin(t)
			}
		}
		if result.Error == "" {
			result.Error = "The Codex turn failed"
		}
	default:
		result.StopReason = "end_turn"
	}
	s.closeOpenItems(t, status)
	s.closeTurnRequests(t)

	last := t.current()
	last.stop = result.StopReason
	s.emit(&last.msgState)
	s.flush(true)
	switch status {
	case "interrupted":
		s.systemEvent("turn interrupted", s.now(), t.id)
	case "failed":
		s.systemEvent("turn failed: "+result.Error, s.now(), t.id)
	}

	s.turn = nil
	if s.ended == nil {
		s.ended = map[string]bool{}
	}
	s.ended[t.id] = true
	s.a.mu.Lock()
	if s.a.activeTurn == t.id {
		s.a.activeTurn = ""
	}
	s.a.mu.Unlock()
	s.sink().TurnCompleted(result)
}

// closeOpenItems finalizes the items of t that never completed: text keeps
// what streamed, tool calls fail.
func (s *loopState) closeOpenItems(t *turnState, status string) {
	reason := "The turn ended before the tool finished"
	switch status {
	case "interrupted":
		reason = "Interrupted"
	case "failed":
		reason = "The turn failed before the tool finished"
	}
	for _, state := range t.order {
		if state.done {
			continue
		}
		state.done = true
		if state.blk == nil || state.blk.Type != "tool_use" {
			continue
		}
		isError := true
		s.finishTool(state, jsonlwatcher.RichContentBlock{
			Type: "tool_result", ToolUseID: state.id, Text: reason, Status: "failed", IsError: &isError,
		}, s.now())
	}
}

// exited handles the end of app-server's output: the process is gone, so
// the open turn ends here. Its open requests are left to the conversation,
// which expires them when Wait returns.
func (s *loopState) exited() {
	if t := s.turn; t != nil {
		s.closeOpenItems(t, "failed")
		t.current().stop = "error"
		s.emit(&t.current().msgState)
		s.turn = nil
	}
	s.flush(true)
	s.a.mu.Lock()
	s.a.activeTurn = ""
	clear(s.a.requests)
	s.a.mu.Unlock()
}

// --- session events ---

func (s *loopState) textEvent(kind, role, text string, at time.Time, sourceID string) {
	s.sink().SessionEvents([]jsonlwatcher.SessionEvent{{
		Kind: kind, Role: strPtr(role), EventTime: at, Content: &text,
		SessionID: optString(s.threadID), SourceID: optString(sourceID),
	}})
}

func (s *loopState) systemEvent(text string, at time.Time, sourceID string) {
	s.sink().SessionEvents([]jsonlwatcher.SessionEvent{{
		Kind: "system", Role: strPtr("system"), EventTime: at, Content: &text,
		SessionID: optString(s.threadID), SourceID: optString(sourceID),
	}})
}

func (s *loopState) toolCallEvent(b *block, at time.Time, sourceID string) {
	s.sink().SessionEvents([]jsonlwatcher.SessionEvent{{
		Kind: "tool_call", Role: strPtr("assistant"), EventTime: at,
		SessionID: optString(s.threadID), SourceID: optString(sourceID),
		ToolName: optString(b.ToolName), ToolInput: b.ToolInput, ToolUseID: optString(b.ToolUseID),
	}})
}
