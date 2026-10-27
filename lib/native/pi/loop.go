package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/agentlog/pilog"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/agentio"
)

// abortedRunMessage is the error of the extra assistant message Pi adds
// when a run is aborted during a tool call. It is not a model error.
const abortedRunMessage = "This operation was aborted"

// loopState is what the event loop keeps between records.
type loopState struct {
	sink native.Sink
	cwd  string
	// usage is the context usage reported last.
	usage native.AgentUsage

	sessionName string
	events      *pilog.EventParser
	parser      *pilog.Parser

	// stream is the assistant message being streamed.
	stream *streamMessage
	// usedIDs are the message IDs already given out.
	usedIDs map[string]bool
	// tools are the tool calls running, in start order.
	tools     []runningTool
	throttles map[string]*throttle

	// The outcome of the turn so far.
	stopReason string
	errMessage string
	// ran: a run started during the turn. A turn without one (an
	// extension command and its dialogs) ends as handled.
	ran bool
	// loginReported: the sink was told the provider login is missing.
	loginReported bool
	// turnDialogs are the dialogs opened during the current turn.
	turnDialogs []string
}

type runningTool struct {
	id   string
	name string
	args json.RawMessage
}

func newLoopState(sink native.Sink, cwd string) loopState {
	return loopState{
		sink:      sink,
		cwd:       cwd,
		events:    pilog.NewEventParser(),
		parser:    pilog.NewParser(),
		usedIDs:   make(map[string]bool),
		throttles: make(map[string]*throttle),
	}
}

// eventLoop handles Pi's records and the functions posted to it (timers).
// It is the only goroutine that calls the sink.
func (a *Adapter) eventLoop(lines <-chan []byte) {
	defer close(a.loopDone)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				a.handleExit()
				return
			}
			a.handleLine(line)
			if a.lineHandled != nil {
				a.lineHandled()
			}
		case fn := <-a.posted:
			fn()
		}
	}
}

// post runs fn on the event loop, unless the loop has stopped.
func (a *Adapter) post(fn func()) {
	select {
	case a.posted <- fn:
	case <-a.loopDone:
	}
}

// runOnLoop runs fn on the event loop and waits for it. It returns false
// if the loop stopped without running it.
func (a *Adapter) runOnLoop(fn func()) bool {
	done := make(chan struct{})
	a.post(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
		return true
	case <-a.loopDone:
		// If the loop took fn, it ran it before it stopped.
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}

// handleExit fails what waits on Pi once its stdout closed, and sends what
// was waiting to be shown.
func (a *Adapter) handleExit() {
	a.mu.Lock()
	a.exited = true
	a.runActive = false
	pending := a.pending
	a.pending = make(map[string]*pendingCmd)
	for _, d := range a.dialogs {
		d.stopTimer()
	}
	a.dialogs = make(map[string]*dialog)
	a.mu.Unlock()
	for _, p := range pending {
		// A caller that stopped waiting doesn't read it.
		deliverResult(p, cmdResult{err: errExited})
	}
	a.flushStream()
	for id, t := range a.loop.throttles {
		t.stop()
		a.flushThrottle(id)
	}
	a.log.Info("Pi closed its output")
}

// decodeRecord decodes a stdout record of type typ. A field of another
// type is left out, not the record (see agentio.Decode); a record that
// doesn't decode is counted as drift.
func decodeRecord[T any](a *Adapter, line []byte, typ string) (T, bool) {
	var v T
	return v, a.drift.Decode(typ, line, &v) == nil
}

func (a *Adapter) handleLine(line []byte) {
	var env envelope
	if _, err := agentio.Decode(line, &env); err != nil {
		a.log.Warn("Pi wrote a line that is not JSON", "error", err)
		return
	}
	switch env.Type {
	case "response":
		if r, ok := decodeRecord[response](a, line, env.Type); ok {
			a.handleResponse(r)
		}
	case "agent_start":
		a.handleAgentStart()
	case "agent_settled":
		a.handleSettled()
	case "thinking_level_changed":
		if ev, ok := decodeRecord[thinkingLevelChanged](a, line, env.Type); ok {
			a.handleThinkingLevel(ev.Level)
		}
	case "agent_end", "turn_start", "turn_end", "queue_update",
		"compaction_start", "auto_retry_start", "bash_execution_update", "entry_appended",
		"summarization_retry_scheduled", "summarization_retry_attempt_start", "summarization_retry_finished":
		a.log.Debug("Pi event", "type", env.Type)
	case "message_start", "message_update", "message_end":
		if ev, ok := decodeRecord[messageEvent](a, line, env.Type); ok {
			a.handleMessageEvent(env.Type, ev)
		}
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
		if ev, ok := decodeRecord[toolEvent](a, line, env.Type); ok {
			a.handleToolEvent(env.Type, ev)
		}
	case "extension_ui_request":
		if req, ok := decodeRecord[uiRequest](a, line, env.Type); ok {
			a.handleUIRequest(req)
		}
	case "session_info_changed":
		if ev, ok := decodeRecord[sessionInfoChanged](a, line, env.Type); ok {
			a.setSessionName(ev.Name)
		}
	case "auto_retry_end":
		if ev, ok := decodeRecord[autoRetryEnd](a, line, env.Type); ok && !ev.Success && ev.FinalError != "" {
			a.loop.stopReason, a.loop.errMessage = "error", ev.FinalError
		}
	case "compaction_end":
		if ev, ok := decodeRecord[compactionEnd](a, line, env.Type); ok && ev.ErrorMessage != "" && !ev.Aborted {
			a.loop.sink.Error(ev.ErrorMessage, conv.ErrorLevelWarning)
		}
	case "extension_error":
		if ev, ok := decodeRecord[extensionError](a, line, env.Type); ok && ev.Error != "" {
			a.loop.sink.Error(fmt.Sprintf("Pi extension %s failed on %s: %s", ev.ExtensionPath, ev.Event, ev.Error), conv.ErrorLevelWarning)
		}
	default:
		a.drift.Unknown(env.Type)
	}
}

// takePending removes the command waiting for response id. deliver says
// whether its caller still waits.
func (a *Adapter) takePending(id string) (p *pendingCmd, deliver bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p = a.pending[id]
	if p == nil {
		return nil, false
	}
	delete(a.pending, id)
	return p, !p.detached
}

func (a *Adapter) handleResponse(r response) {
	p, deliver := a.takePending(r.ID)
	if p == nil {
		if !r.Success {
			a.log.Warn("Pi rejected a command", "command", r.Command, "error", r.Error)
		}
		return
	}
	res := cmdResult{data: r.Data}
	if !r.Success {
		res = cmdResult{err: &native.RefusedError{Reason: r.Error}}
	}
	switch p.kind {
	case cmdTurn, cmdSteer:
		a.handlePromptResponse(p, deliver, r)
	case cmdAbort:
		switch {
		case !r.Success:
			a.loop.sink.Error("Pi couldn't stop the turn: "+r.Error, conv.ErrorLevelWarning)
		case a.isTurnActive() && !a.isRunActive():
			// An extension command waiting on a dialog: abort doesn't
			// reach it, but cancelling its dialogs ends it.
			a.cancelTurnDialogs(true)
		}
	case cmdQuery:
	}
	if deliver {
		deliverResult(p, res)
	}
}

// handlePromptResponse follows the turn from the response to a prompt.
// deliver says whether its caller still waits for it.
func (a *Adapter) handlePromptResponse(p *pendingCmd, deliver bool, r response) {
	if !r.Success {
		switch {
		case deliver:
			// The caller gets the error ("No API key found for …").
		case p.turn == 0:
			a.log.Warn("Pi rejected a prompt after its caller stopped waiting", "error", r.Error)
		case a.isCurrentTurn(p.turn) && !a.isRunActive():
			// The caller was told the prompt started: an extension dialog
			// began its turn, which ends with the error.
			a.loop.stopReason, a.loop.errMessage = "error", r.Error
			a.endTurn()
			return
		default:
			// The caller was told the prompt started, but Pi went on
			// without the message.
			a.loop.sink.Error("Pi refused the message: "+r.Error, conv.ErrorLevelError)
		}
		if isAuthError(r.Error) {
			a.reportLogin()
		}
		return
	}
	var result promptResult
	_ = a.drift.Decode("prompt response", r.Data, &result)
	if result.Disposition != "handled" {
		// A run started, or the message went into the running one.
		a.beginTurn()
		return
	}
	if p.kind != cmdTurn {
		return
	}
	// An extension command ran. Unless something reported its prompt
	// started, it was a turn of its own.
	if p.turn == 0 {
		a.beginTurn()
	} else if !a.isCurrentTurn(p.turn) {
		// Its turn ended already (agent_settled of a run it waited for).
		return
	}
	if !a.isRunActive() {
		// Unless it started a run that goes on, the turn ends.
		a.endTurn()
	}
}

// deliverResult hands a result to the caller waiting on p.
func deliverResult(p *pendingCmd, res cmdResult) {
	select {
	case p.done <- res:
	default:
	}
}

// handleState handles the get_state response of the handshake.
func (a *Adapter) handleState(state sessionState) {
	if a.resumeRef != "" && !a.fork && state.SessionID != a.resumeRef {
		a.log.Warn("Pi opened a different session than the one to resume", "want", a.resumeRef, "got", state.SessionID)
	}
	a.setSettings(stateSettings(state))
	a.setModelInfo(state.Model)
	a.log.Info("Pi session open", "session", state.SessionID, "file", state.SessionFile)
	if state.SessionID != "" {
		a.loop.sink.SessionRef(state.SessionID)
	}
	if state.SessionName != "" {
		a.setSessionName(state.SessionName)
	}
	header, _ := json.Marshal(map[string]string{
		"type":      "session",
		"id":        state.SessionID,
		"timestamp": a.clock.Now().UTC().Format(time.RFC3339Nano),
		"cwd":       a.loop.cwd,
	})
	if events, err := a.loop.events.ParseSessionEvents(header); err == nil && len(events) > 0 {
		a.loop.sink.SessionEvents(events)
	}
	if state.IsStreaming {
		// A run an extension started.
		a.handleAgentStart()
	}
}

func (a *Adapter) setSessionName(name string) {
	if name == "" || name == a.loop.sessionName {
		return
	}
	a.loop.sessionName = name
	a.loop.sink.SessionName(name)
}

func (a *Adapter) isTurnActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turnActive
}

// isCurrentTurn reports whether turn is the one running.
func (a *Adapter) isCurrentTurn(turn int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turnActive && a.turnSeq == turn
}

func (a *Adapter) isRunActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runActive
}

func (a *Adapter) setRunActive(v bool) {
	a.mu.Lock()
	a.runActive = v
	a.mu.Unlock()
}

// acceptPrompt tells a StartTurn still waiting for its prompt's response
// that the prompt started (a run began or its extension command opened a
// dialog), so it returns before an extension command finishes. The prompts
// waiting for their response belong to the running turn.
func (a *Adapter) acceptPrompt() {
	a.mu.Lock()
	var accepted []*pendingCmd
	for _, p := range a.pending {
		if p.kind != cmdTurn {
			continue
		}
		p.turn = a.turnSeq
		if !p.detached {
			p.detached = true
			accepted = append(accepted, p)
		}
	}
	a.mu.Unlock()
	for _, p := range accepted {
		deliverResult(p, cmdResult{})
	}
}

// beginTurn reports a turn start unless one is running.
func (a *Adapter) beginTurn() {
	a.mu.Lock()
	if a.turnActive {
		a.mu.Unlock()
		return
	}
	a.turnActive = true
	a.turnSeq++
	a.mu.Unlock()
	a.loop.stopReason = ""
	a.loop.errMessage = ""
	a.loop.ran = false
	a.loop.turnDialogs = nil
	a.loop.loginReported = false
	a.loop.sink.TurnStarted()
}

// endTurn reports the end of the running turn, once.
func (a *Adapter) endTurn() {
	a.mu.Lock()
	if !a.turnActive {
		a.mu.Unlock()
		return
	}
	a.turnActive = false
	interrupted := a.interrupted
	a.interrupted = false
	a.mu.Unlock()

	a.flushStream()
	// The conversation expires the requests of the turn.
	a.cancelTurnDialogs(false)
	result := native.TurnResult{StopReason: native.StopReasonEndTurn}
	switch {
	case interrupted || a.loop.stopReason == "aborted":
		result.StopReason = native.StopReasonInterrupted
	case a.loop.stopReason == "error":
		result.StopReason = native.StopReasonError
		result.Error = a.loop.errMessage
		if result.Error == "" {
			result.Error = "Pi stopped with an error"
		}
	case a.loop.stopReason == "length":
		result.StopReason = native.StopReasonMaxTokens
	case !a.loop.ran:
		result.StopReason = native.StopReasonHandled
	}
	a.loop.stopReason = ""
	a.loop.errMessage = ""
	if result.Error != "" && isAuthError(result.Error) {
		a.reportLogin()
	}
	if result.StopReason == native.StopReasonError && native.IsLimitError(result.Error) {
		result.StopReason = native.StopReasonLimited
	}
	a.loop.sink.TurnCompleted(result)
}

// endInterruptedTurn ends turn if it is still running interruptTimeout
// after Interrupt: Pi didn't confirm the abort (an extension command that
// doesn't stop, or a Pi that hangs).
func (a *Adapter) endInterruptedTurn(turn int) {
	if !a.isCurrentTurn(turn) {
		return
	}
	a.log.Warn("Pi didn't end the interrupted turn; ending it here")
	a.endTurn()
}

func (a *Adapter) handleAgentStart() {
	a.setRunActive(true)
	a.beginTurn()
	a.acceptPrompt()
	a.loop.ran = true
}

// handleSettled ends the turn: agent_settled comes once per prompt, after
// retries, queued messages and aborts.
func (a *Adapter) handleSettled() {
	a.setRunActive(false)
	a.endTurn()
}

// cancelTurnDialogs cancels the dialogs of the turn that are still open,
// so an extension doesn't wait for an answer that can't come any more.
// withdraw reports them closed; at the end of the turn the conversation
// expires them instead.
func (a *Adapter) cancelTurnDialogs(withdraw bool) {
	ids := a.loop.turnDialogs
	a.loop.turnDialogs = nil
	var cancel []string
	a.mu.Lock()
	for _, id := range ids {
		// A dialog whose answer is being written gets that answer, not a
		// cancel too; Respond removes it once it is written.
		if d := a.dialogs[id]; d != nil && !d.answering {
			d.stopTimer()
			delete(a.dialogs, id)
			cancel = append(cancel, id)
		}
	}
	a.mu.Unlock()
	for _, id := range cancel {
		if withdraw {
			a.loop.sink.RequestClosed(id)
		}
		go func(id string) {
			if err := a.send(context.Background(), uiResponse{Type: "extension_ui_response", ID: id, Cancelled: true}); err != nil {
				a.log.Debug("Failed to cancel a Pi dialog", "id", id, "error", err)
			}
		}(id)
	}
}

// uniqueID returns base, or base with a suffix if it was given out.
func (l *loopState) uniqueID(base string) string {
	id := base
	for n := 2; l.usedIDs[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	l.usedIDs[id] = true
	return id
}

// messageID derives a stable ID from a message: RPC messages have no
// entry id. Tool results are keyed by their tool call, which is what live
// output streams under before the result message exists.
func messageID(msg agentMessage) string {
	if msg.Role == "toolResult" && msg.ToolCallID != "" {
		return resultID(msg.ToolCallID)
	}
	return fmt.Sprintf("%s-%d", strings.ToLower(msg.Role), msg.Timestamp)
}

// resultID is the message ID of the result of tool call callID.
func resultID(callID string) string {
	return "result-" + callID
}
