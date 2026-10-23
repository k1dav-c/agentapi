package codex

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog/codexlog"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

// endedTurns is how many ended turns the read loop remembers.
const endedTurns = 16

// loopState is the state of the read loop. Only the loop goroutine uses it.
type loopState struct {
	a        *Adapter
	threadID string
	model    string
	turn     *turnState
	// ended lists the turns that ended last, so late notifications don't
	// start them again.
	ended []string
	// dirty lists messages with changes not sent yet.
	dirty []*msgState
	// usage is what app-server reported of the context and rate limits.
	usage native.AgentUsage
	// spawns holds the spawn_agent tool calls of the subagents still
	// running, by their thread, to end once they are done.
	spawns map[string]*itemState
}

func (s *loopState) now() time.Time { return s.a.cfg.Clock.Now() }

func (s *loopState) sink() native.Sink { return s.a.sink }

func (s *loopState) warn(message string) { s.sink().Error(message, conv.ErrorLevelWarning) }

// threadOpened returns the handler of a thread/start or thread/resume
// result.
func (s *loopState) threadOpened(resumed bool) func(json.RawMessage, *rpcError) {
	return func(result json.RawMessage, rpcErr *rpcError) {
		if rpcErr != nil {
			return
		}
		var resp threadStartResponse
		if err := json.Unmarshal(result, &resp); err != nil || resp.Thread.ID == "" {
			// Without a thread, the start fails (see openThread).
			s.a.log.Warn("Unexpected thread response from codex app-server", "error", err)
			return
		}
		th := resp.Thread
		s.threadID = th.ID
		s.model = resp.Model
		s.a.threadSettings(resp)
		s.a.mu.Lock()
		s.a.threadID = th.ID
		s.a.mu.Unlock()
		s.a.log.Info("Codex thread ready", "thread", th.ID, "resumed", resumed, "model", resp.Model, "rollout", th.Path)
		s.sink().SessionRef(th.ID)
		if th.Name != "" {
			s.sink().SessionName(th.Name)
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
		s.systemEvent(fmt.Sprintf("session %s: %s (codex %s)", verb, cwd, th.CLIVersion), eventTime, th.ID)
	}
}

// ours reports whether a notification's thread is the adapter's thread.
// Notifications without a thread belong to it.
func (s *loopState) ours(threadID string) bool {
	return threadID == "" || s.threadID == "" || threadID == s.threadID
}

// beginTurn makes turnID the active turn, if it isn't already.
func (s *loopState) beginTurn(turnID string) *turnState {
	if slices.Contains(s.ended, turnID) {
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
	s.a.turnDone = make(chan struct{})
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
	if turnID == "" || slices.Contains(s.ended, turnID) {
		return nil
	}
	return s.beginTurn(turnID)
}

func (s *loopState) newSegment(t *turnState, id string) *segment {
	seg := &segment{id: id, timestamp: s.now()}
	seg.build = func() (transcript.RichMessage, bool) { return seg.snapshot(s.model) }
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
		if !m.dirty {
			// Sent since (urgently); flush drops it.
			continue
		}
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

// notification handles a notification from app-server.
func (s *loopState) notification(msg rpcMessage) {
	switch msg.Method {
	case "turn/started":
		handle(msg, s.turnStarted)
	case "turn/completed":
		handle(msg, s.turnCompleted)
	case "item/started", "item/completed":
		handle(msg, func(n itemNotification) { s.itemNotified(msg, n) })
	case "item/agentMessage/delta", "item/plan/delta":
		handle(msg, s.textDelta)
	case "item/reasoning/summaryPartAdded", "item/reasoning/summaryTextDelta":
		handle(msg, func(d deltaNotification) { s.reasoningDelta(d, true) })
	case "item/reasoning/textDelta":
		handle(msg, func(d deltaNotification) { s.reasoningDelta(d, false) })
	case "item/commandExecution/outputDelta":
		handle(msg, s.outputDelta)
	case "thread/tokenUsage/updated":
		handle(msg, s.tokenUsageUpdated)
	case "turn/plan/updated":
		handle(msg, s.planUpdated)
	case "error":
		handle(msg, s.errorReported)
	case "thread/name/updated":
		handle(msg, s.threadNameUpdated)
	case "thread/status/changed":
		handle(msg, s.threadStatusChanged)
	case "serverRequest/resolved":
		handle(msg, func(n serverRequestResolvedNotification) { s.requestResolved(string(n.RequestID)) })
	case "warning":
		handle(msg, s.warningReported)
	case "configWarning":
		handle(msg, s.configWarningReported)
	case "mcpServer/startupStatus/updated":
		handle(msg, s.mcpServerStatusUpdated)
	case "model/rerouted":
		handle(msg, s.modelRerouted)
	case "account/rateLimits/updated":
		handle(msg, s.rateLimitsUpdated)
	default:
		s.a.log.Debug("Codex notification", "method", msg.Method)
	}
}

// handle passes the params of msg to f, unless they don't decode.
func handle[T any](msg rpcMessage, f func(T)) {
	var params T
	if decode(msg, &params) {
		f(params)
	}
}

func decode(msg rpcMessage, v any) bool {
	return len(msg.Params) > 0 && json.Unmarshal(msg.Params, v) == nil
}

func (s *loopState) turnStarted(n turnNotification) {
	if s.ours(n.ThreadID) && n.Turn.ID != "" {
		s.beginTurn(n.Turn.ID)
	}
}

func (s *loopState) turnCompleted(n turnNotification) {
	if s.ours(n.ThreadID) && s.turn != nil && s.turn.id == n.Turn.ID {
		s.completeTurn(n.Turn)
	}
}

// itemNotified handles item/started and item/completed.
func (s *loopState) itemNotified(msg rpcMessage, n itemNotification) {
	if !s.ours(n.ThreadID) {
		return
	}
	t := s.turnFor(n.TurnID)
	if t == nil {
		return
	}
	at := native.UnixMilli(n.StartedAtMs)
	if msg.Method == "item/completed" {
		at = native.UnixMilli(n.CompletedAtMs)
	}
	if at.IsZero() {
		at = native.UnixMilli(msg.EmittedAtMs)
	}
	if msg.Method == "item/started" {
		s.itemStarted(t, &n.Item, at)
	} else {
		s.itemCompleted(t, &n.Item, at)
	}
}

// deltaItem returns the open item with a block a delta notification is
// for.
func (s *loopState) deltaItem(d deltaNotification) *itemState {
	if !s.ours(d.ThreadID) || s.turn == nil || (d.TurnID != "" && d.TurnID != s.turn.id) {
		return nil
	}
	if it := s.turn.items[d.ItemID]; it != nil && it.blk != nil {
		return it
	}
	return nil
}

// textDelta streams the text of an agent message or a plan.
func (s *loopState) textDelta(d deltaNotification) {
	if it := s.deltaItem(d); it != nil {
		it.blk.Text += d.Delta
		s.touch(&it.seg.msgState, false)
	}
}

// reasoningDelta streams a part of a reasoning item's summary or of its
// raw content.
func (s *loopState) reasoningDelta(d deltaNotification, summary bool) {
	it := s.deltaItem(d)
	if it == nil {
		return
	}
	parts, i := &it.blk.content, d.ContentIndex
	if summary {
		parts, i = &it.blk.summary, d.SummaryIndex
	}
	if i < 0 {
		return
	}
	for len(*parts) <= i {
		*parts = append(*parts, "")
	}
	(*parts)[i] += d.Delta
	it.blk.Thinking = joinReasoning(it.blk.summary, it.blk.content)
	s.touch(&it.seg.msgState, false)
}

// outputDelta previews a command's output. The deltas miss output (the
// start of a command, all of a short one), so they are only a live
// preview; the completed item has the whole output.
func (s *loopState) outputDelta(d deltaNotification) {
	if it := s.deltaItem(d); it != nil && !it.done {
		it.output.WriteString(d.Delta)
		it.resultBlock = toolResultBlock(it.id, it.output.String(), "running")
		s.touch(s.resultMsg(it), false)
	}
}

func (s *loopState) tokenUsageUpdated(n threadTokenUsageUpdatedNotification) {
	if !s.ours(n.ThreadID) {
		return
	}
	// The last model call's tokens are what the context holds.
	s.contextUsed(n.TokenUsage.Last.TotalTokens, n.TokenUsage.ModelContextWindow)
	if s.turn == nil || s.turn.id != n.TurnID {
		return
	}
	seg := s.turn.current()
	// Codex counts the cached input in its input; the transcript doesn't.
	last := n.TokenUsage.Last
	seg.usage = codexlog.Totals{
		InputTokens:       last.InputTokens,
		CachedInputTokens: last.CachedInputTokens,
		CacheWriteTokens:  last.CacheWriteInputTokens,
		OutputTokens:      last.OutputTokens,
	}.Usage()
	s.touch(&seg.msgState, false)
}

// errorReported handles an error: one app-server retries after is a
// warning, one of the turn is reported when the turn ends (as the turn's
// error if it fails).
func (s *loopState) errorReported(n errorNotification) {
	if !s.ours(n.ThreadID) {
		return
	}
	text := turnErrorText(&n.Error)
	switch {
	case n.WillRetry:
		s.warn(text)
	case s.turn != nil && (n.TurnID == "" || n.TurnID == s.turn.id):
		s.turn.lastError = text
		if isAuthError(&n.Error) {
			s.reportLogin(s.turn)
		}
	default:
		if isAuthError(&n.Error) {
			s.reportLogin(nil)
		}
		s.sink().Error(text, conv.ErrorLevelError)
	}
}

func turnErrorText(e *turnError) string {
	text := e.Message
	if e.AdditionalDetails != "" {
		text += "\n" + e.AdditionalDetails
	}
	return text
}

func (s *loopState) threadNameUpdated(n threadNameUpdatedNotification) {
	if s.ours(n.ThreadID) && n.ThreadName != nil {
		s.sink().SessionName(*n.ThreadName)
	}
}

func (s *loopState) threadStatusChanged(n threadStatusChangedNotification) {
	if s.ours(n.ThreadID) && n.Status.Type == "systemError" {
		s.sink().Error("Codex reported a system error for the thread", conv.ErrorLevelError)
	}
}

func (s *loopState) warningReported(n warningNotification) {
	if s.ours(n.ThreadID) && n.Message != "" {
		s.warn(n.Message)
	}
}

func (s *loopState) configWarningReported(n configWarningNotification) {
	if n.Summary == "" {
		return
	}
	text := n.Summary
	if n.Details != "" {
		text += "\n" + n.Details
	}
	s.warn(text)
}

func (s *loopState) mcpServerStatusUpdated(n mcpServerStatusUpdatedNotification) {
	if !s.ours(n.ThreadID) || (n.Status != "failed" && n.Error == "") {
		return
	}
	reason := n.Status
	if n.Error != "" {
		reason = n.Error
	}
	s.warn(fmt.Sprintf("MCP server %s failed to start: %s", n.Name, reason))
}

func (s *loopState) modelRerouted(n modelReroutedNotification) {
	if s.ours(n.ThreadID) && n.ToModel != "" {
		s.model = n.ToModel
	}
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
		s.addItem(t, it, nil)
	case "agentMessage", "plan":
		s.addItem(t, it, &block{RichContentBlock: transcript.RichContentBlock{Type: "text", Text: it.Text}})
	case "reasoning":
		b := &block{RichContentBlock: transcript.RichContentBlock{Type: "thinking"}, summary: it.Summary, content: it.reasoningContent()}
		b.Thinking = joinReasoning(b.summary, b.content)
		s.addItem(t, it, b)
	default:
		name, input, ok := toolUse(it)
		if !ok {
			s.a.log.Debug("Ignoring Codex item", "type", it.Type)
			return
		}
		state := s.addItem(t, it, &block{RichContentBlock: transcript.RichContentBlock{
			Type: "tool_use", ToolUseID: it.ID, ToolName: name, ToolInput: input, Status: "running",
		}})
		s.toolCallEvent(state.blk, at, it.ID)
	}
}

// addItem records an item and adds its block to the current segment.
func (s *loopState) addItem(t *turnState, it *threadItem, b *block) *itemState {
	seg := t.current()
	state := &itemState{id: it.ID, kind: it.Type, seg: seg, blk: b, changes: it.Changes, server: it.Server}
	t.items[it.ID] = state
	t.order = append(t.order, state)
	if b != nil {
		seg.blocks = append(seg.blocks, b)
		s.touch(&seg.msgState, true)
	}
	return state
}

func (s *loopState) itemCompleted(t *turnState, it *threadItem, at time.Time) {
	if it.Type == "subAgentActivity" && it.Kind != "started" {
		s.subagentActivity(it, at)
		return
	}
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
		s.sink().RichMessage(transcript.RichMessage{
			MessageID: it.ID, Role: "user", Timestamp: s.now(),
			Content: []transcript.RichContentBlock{{Type: "text", Text: text}},
		})
		s.textEvent("text", "user", text, at, it.ID)
	case "agentMessage", "plan":
		state.blk.Text = it.Text
		s.touch(&state.seg.msgState, true)
		if it.Type == "plan" {
			t.plan = it.Text
		}
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
		s.finishTool(state, toolResult(it), at)
		if it.Type == "subAgentActivity" && it.AgentThreadID != "" {
			if s.spawns == nil {
				s.spawns = make(map[string]*itemState)
			}
			s.spawns[it.AgentThreadID] = state
		}
	}
}

// subagentActivity ends the spawn_agent call of a subagent once Codex says
// it completed or was interrupted.
func (s *loopState) subagentActivity(it *threadItem, at time.Time) {
	state := s.spawns[it.AgentThreadID]
	if state == nil {
		return
	}
	switch it.Kind {
	case "completed":
		s.finishTool(state, toolResultBlock(state.id, "Finished "+it.AgentPath, "completed"), at)
	case "interrupted":
		s.finishTool(state, toolResultBlock(state.id, "Interrupted "+it.AgentPath, "failed"), at)
	default:
		return
	}
	delete(s.spawns, it.AgentThreadID)
}

// finishTool sets a tool call's final status and sends its result.
func (s *loopState) finishTool(state *itemState, result transcript.RichContentBlock, at time.Time) {
	state.done = true
	state.blk.Status = result.Status
	s.touch(&state.seg.msgState, true)
	state.resultBlock = result
	s.touch(s.resultMsg(state), true)
	content := result.Text
	s.sink().SessionEvents([]transcript.SessionEvent{{
		Kind: "tool_result", EventTime: at, Content: &content,
		SessionID: optString(s.threadID), ToolUseID: optString(state.id),
	}})
}

// resultMsg returns the tool result message of an item.
func (s *loopState) resultMsg(state *itemState) *msgState {
	if state.result == nil {
		ts := s.now()
		state.result = &msgState{}
		state.result.build = func() (transcript.RichMessage, bool) {
			return transcript.RichMessage{
				MessageID: "result-" + state.id,
				Role:      "user",
				Timestamp: ts,
				Content:   []transcript.RichContentBlock{state.resultBlock},
			}, true
		}
	}
	return state.result
}

// planUpdated shows the todo list (update_plan) as one tool call per
// turn, replaced on every update.
func (s *loopState) planUpdated(n turnPlanUpdatedNotification) {
	if !s.ours(n.ThreadID) {
		return
	}
	t := s.turnFor(n.TurnID)
	if t == nil {
		return
	}
	id := "plan-" + t.id
	in := map[string]any{"plan": n.Plan}
	if n.Plan == nil {
		in["plan"] = []turnPlanStep{}
	}
	if n.Explanation != "" {
		in["explanation"] = n.Explanation
	}
	input := mustJSON(in)
	state := t.items[id]
	if state == nil {
		state = s.addItem(t, &threadItem{ID: id, Type: "plan/update"}, &block{RichContentBlock: transcript.RichContentBlock{
			Type: "tool_use", ToolUseID: id, ToolName: "update_plan", ToolInput: input,
		}})
	}
	state.blk.ToolInput = input
	s.toolCallEvent(state.blk, s.now(), id)
	s.finishTool(state, toolResultBlock(id, "Plan updated", "completed"), s.now())
}

// endActiveTurnLocked records that app-server no longer works on a turn.
// Caller holds a.mu.
func (a *Adapter) endActiveTurnLocked() {
	a.activeTurn = ""
	if a.turnDone != nil {
		close(a.turnDone)
		a.turnDone = nil
	}
}

// completeTurn ends the active turn: it closes the items app-server left
// open (an interrupt leaves them so), sends the final snapshots and
// reports the end.
func (s *loopState) completeTurn(tr turn) {
	t := s.turn
	if t == nil {
		return
	}
	// From here on Steer and Interrupt see no turn; reading the interrupt
	// mark in the same step means Interrupt can't add one after it.
	s.a.mu.Lock()
	interrupting := s.a.interrupting[t.id]
	delete(s.a.interrupting, t.id)
	if s.a.activeTurn == t.id {
		s.a.endActiveTurnLocked()
	}
	s.a.mu.Unlock()

	status := tr.Status
	if interrupting && status == "completed" {
		status = "interrupted"
	}
	result := native.TurnResult{StopReason: native.StopReasonEndTurn}
	switch status {
	case "interrupted":
		result.StopReason = native.StopReasonInterrupted
	case "failed":
		result.StopReason = native.StopReasonError
		result.Error = t.lastError
		if tr.Error != nil {
			result.Error = turnErrorText(tr.Error)
			if isAuthError(tr.Error) {
				s.reportLogin(t)
			}
		}
		if result.Error == "" {
			result.Error = "The Codex turn failed"
		}
		if isLimitError(tr.Error) || (tr.Error == nil && native.IsLimitError(result.Error)) {
			result.StopReason = native.StopReasonLimited
		}
	}
	s.closeOpenItems(t, status)
	s.closeTurnRequests()

	last := t.lastVisible()
	last.stop = result.StopReason
	s.emit(&last.msgState)
	s.flush(true)
	switch status {
	case "interrupted":
		s.systemEvent("turn interrupted", s.now(), t.id)
	case "failed":
		s.systemEvent("turn failed: "+result.Error, s.now(), t.id)
	}
	if t.lastError != "" && t.lastError != result.Error {
		// The turn went on after the error, or failed with another one.
		s.sink().Error(t.lastError, conv.ErrorLevelError)
	}

	s.turn = nil
	s.ended = append(s.ended, t.id)
	if len(s.ended) > endedTurns {
		s.ended = s.ended[1:]
	}
	if status == "completed" && strings.TrimSpace(t.plan) != "" {
		// The turn goes on once the user decides about the plan.
		s.awaitPlanDecision(t)
		return
	}
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
		s.finishTool(state, toolResultBlock(state.id, reason, "failed"), s.now())
	}
}

// exited handles the end of app-server's output: the process is gone, so
// the open turn ends here. Its open requests are left to the conversation,
// which expires them when Wait returns.
func (s *loopState) exited() {
	if t := s.turn; t != nil {
		s.closeOpenItems(t, "failed")
		last := t.lastVisible()
		last.stop = native.StopReasonError
		s.emit(&last.msgState)
		s.turn = nil
	}
	s.flush(true)
	s.a.mu.Lock()
	s.a.endActiveTurnLocked()
	clear(s.a.interrupting)
	clear(s.a.requests)
	s.a.mu.Unlock()
}

// --- session events ---

func (s *loopState) textEvent(kind, role, text string, at time.Time, sourceID string) {
	s.sink().SessionEvents([]transcript.SessionEvent{{
		Kind: kind, Role: strPtr(role), EventTime: at, Content: &text,
		SessionID: optString(s.threadID), SourceID: optString(sourceID),
	}})
}

func (s *loopState) systemEvent(text string, at time.Time, sourceID string) {
	s.textEvent("system", "system", text, at, sourceID)
}

func (s *loopState) toolCallEvent(b *block, at time.Time, sourceID string) {
	s.sink().SessionEvents([]transcript.SessionEvent{{
		Kind: "tool_call", Role: strPtr("assistant"), EventTime: at,
		SessionID: optString(s.threadID), SourceID: optString(sourceID),
		ToolName: optString(b.ToolName), ToolInput: b.ToolInput, ToolUseID: optString(b.ToolUseID),
	}})
}
