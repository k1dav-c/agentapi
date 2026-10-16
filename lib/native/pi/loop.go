package pi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// abortedRunMessage is the error of the extra assistant message Pi adds
// when a run is aborted during a tool call. It is not a model error.
const abortedRunMessage = "This operation was aborted"

// loopState is what the event loop keeps between records.
type loopState struct {
	sink native.Sink
	cwd  string

	sessionID   string
	sessionName string
	events      *jsonlwatcher.PiSessionEventParser
	parser      *jsonlwatcher.PiParser

	// stream is the assistant message being streamed.
	stream *streamMessage
	// usedIDs are the message IDs already given out.
	usedIDs map[string]bool
	// tools are the tool calls running, in start order.
	tools []runningTool
	// exitCodes holds bash exit codes from tool_execution_end until the
	// tool result message arrives.
	exitCodes map[string]int
	throttles map[string]*throttle

	// The outcome of the turn so far.
	stopReason string
	errMessage string
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
		events:    jsonlwatcher.NewPiSessionEventParser(),
		parser:    jsonlwatcher.NewPiParser(),
		usedIDs:   make(map[string]bool),
		exitCodes: make(map[string]int),
		throttles: make(map[string]*throttle),
	}
}

// eventLoop handles Pi's records and the adapter's timers. It is the only
// goroutine that calls the sink.
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
		case fn := <-a.timers:
			fn()
		}
	}
}

// post runs fn on the event loop, unless the loop has stopped.
func (a *Adapter) post(fn func()) {
	select {
	case a.timers <- fn:
	case <-a.loopDone:
	}
}

// handleExit fails what waits on Pi once its stdout closed.
func (a *Adapter) handleExit() {
	a.mu.Lock()
	a.exited = true
	a.runActive = false
	pending := a.pending
	a.pending = make(map[string]*pendingCmd)
	a.dialogs = make(map[string]*dialog)
	a.mu.Unlock()
	for _, p := range pending {
		if !p.detached {
			deliverResult(p, cmdResult{err: errExited})
		}
	}
	for _, t := range a.loop.throttles {
		t.stop()
	}
	a.logger.Info("Pi closed its output")
}

func (a *Adapter) handleLine(line []byte) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		a.logger.Warn("Pi wrote a line that is not JSON", "error", err)
		return
	}
	switch env.Type {
	case "response":
		var r response
		if err := json.Unmarshal(line, &r); err != nil {
			a.logger.Warn("Malformed Pi response", "error", err)
			return
		}
		a.handleResponse(r)
	case "agent_start":
		a.handleAgentStart()
	case "agent_settled":
		a.handleSettled()
	case "agent_end", "turn_start", "turn_end", "queue_update", "thinking_level_changed",
		"compaction_start", "auto_retry_start", "bash_execution_update", "entry_appended",
		"summarization_retry_scheduled", "summarization_retry_attempt_start", "summarization_retry_finished":
		a.logger.Debug("Pi event", "type", env.Type)
	case "message_start", "message_update", "message_end":
		var ev messageEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			a.logger.Warn("Malformed Pi message event", "type", env.Type, "error", err)
			return
		}
		a.handleMessageEvent(env.Type, ev)
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
		var ev toolEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			a.logger.Warn("Malformed Pi tool event", "type", env.Type, "error", err)
			return
		}
		a.handleToolEvent(env.Type, ev)
	case "extension_ui_request":
		var req uiRequest
		if err := json.Unmarshal(line, &req); err != nil {
			a.logger.Warn("Malformed Pi extension request", "error", err)
			return
		}
		a.handleUIRequest(req)
	case "session_info_changed":
		var ev sessionInfoChanged
		_ = json.Unmarshal(line, &ev)
		a.setSessionName(ev.Name)
	case "auto_retry_end":
		var ev autoRetryEnd
		_ = json.Unmarshal(line, &ev)
		if !ev.Success && ev.FinalError != "" {
			a.loop.stopReason = "error"
			a.loop.errMessage = ev.FinalError
		}
	case "compaction_end":
		var ev compactionEnd
		_ = json.Unmarshal(line, &ev)
		if ev.ErrorMessage != "" && !ev.Aborted {
			a.loop.sink.Error(ev.ErrorMessage, st.ErrorLevelWarning)
		}
	case "extension_error":
		var ev extensionError
		_ = json.Unmarshal(line, &ev)
		a.loop.sink.Error(fmt.Sprintf("Pi extension %s failed on %s: %s", ev.ExtensionPath, ev.Event, ev.Error), st.ErrorLevelWarning)
	default:
		a.logger.Debug("Unknown Pi record", "type", env.Type)
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
			a.logger.Warn("Pi rejected a command", "command", r.Command, "error", r.Error)
		}
		return
	}
	var res cmdResult
	if r.Success {
		var pr promptResult
		_ = json.Unmarshal(r.Data, &pr)
		res = cmdResult{disposition: pr.Disposition, data: r.Data}
	} else {
		res = cmdResult{err: errors.New(r.Error)}
	}

	switch p.kind {
	case cmdInit:
		// Without a model, Start checks the login first.
		if res.err == nil && stateHasModel(r.Data) {
			a.handleState(r.Data)
		}
	case cmdTurn, cmdSteer:
		switch {
		case res.err != nil && !deliver:
			// The caller was already told the prompt started.
			a.logger.Warn("Pi rejected a prompt", "error", res.err)
			if a.isTurnActive() && !a.isRunActive() {
				a.loop.stopReason = "error"
				a.loop.errMessage = res.err.Error()
				a.endTurn()
			} else if isAuthError(res.err.Error()) {
				a.reportLogin()
			}
		case res.err != nil:
			// The caller gets the error ("No API key found for …").
			if isAuthError(res.err.Error()) {
				a.reportLogin()
			}
		case res.disposition == "handled":
			// An extension command ran without starting a run. A prompt
			// that started the turn ends it.
			if p.kind == cmdTurn && !a.isRunActive() {
				a.beginTurn()
				a.endTurn()
			}
		default:
			a.beginTurn()
		}
	case cmdQuery:
	case cmdAbort:
		if res.err != nil {
			a.loop.sink.Error("Pi couldn't stop the turn: "+res.err.Error(), st.ErrorLevelWarning)
		} else if a.isTurnActive() && !a.isRunActive() {
			// An extension command waiting on a dialog: abort doesn't
			// reach it, but cancelling its dialogs ends it.
			a.cancelTurnDialogs()
		}
	}
	if deliver {
		deliverResult(p, res)
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
func (a *Adapter) handleState(data json.RawMessage) {
	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		a.logger.Warn("Malformed Pi session state", "error", err)
	}
	a.loop.sessionID = state.SessionID
	if a.resumeRef != "" && state.SessionID != a.resumeRef {
		a.logger.Warn("Pi opened a different session than the one to resume", "want", a.resumeRef, "got", state.SessionID)
	}
	if state.Model != nil && len(state.Model.Input) > 0 {
		images := false
		for _, in := range state.Model.Input {
			if in == "image" {
				images = true
			}
		}
		a.mu.Lock()
		a.caps.Images = images
		a.mu.Unlock()
	}
	a.logger.Info("Pi session open", "session", state.SessionID, "file", state.SessionFile)
	if state.SessionID != "" {
		a.loop.sink.SessionRef(state.SessionID)
	}
	if state.SessionName != "" {
		a.setSessionName(state.SessionName)
	}
	header, _ := json.Marshal(map[string]string{
		"type":      "session",
		"id":        state.SessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"cwd":       a.loop.cwd,
	})
	if events, err := a.loop.events.ParseSessionEvents(header); err == nil && len(events) > 0 {
		a.loop.sink.SessionEvents(events)
	}
	if state.IsStreaming {
		a.setRunActive(true)
		a.beginTurn()
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
// dialog), so it returns before an extension command finishes.
func (a *Adapter) acceptPrompt() {
	a.mu.Lock()
	var accepted []*pendingCmd
	for _, p := range a.pending {
		if p.kind == cmdTurn && !p.detached {
			p.detached = true
			accepted = append(accepted, p)
		}
	}
	a.mu.Unlock()
	for _, p := range accepted {
		deliverResult(p, cmdResult{disposition: "started"})
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
	a.mu.Unlock()
	a.loop.stopReason = ""
	a.loop.errMessage = ""
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
	a.cancelTurnDialogs()
	result := native.TurnResult{StopReason: "end_turn"}
	switch {
	case interrupted || a.loop.stopReason == "aborted":
		result.StopReason = "interrupted"
	case a.loop.stopReason == "error":
		result.StopReason = "error"
		result.Error = a.loop.errMessage
		if result.Error == "" {
			result.Error = "Pi stopped with an error"
		}
	case a.loop.stopReason == "length":
		result.StopReason = "max_tokens"
	}
	a.loop.stopReason = ""
	a.loop.errMessage = ""
	if result.Error != "" && isAuthError(result.Error) {
		a.reportLogin()
	}
	a.loop.sink.TurnCompleted(result)
}

func (a *Adapter) handleAgentStart() {
	a.setRunActive(true)
	a.acceptPrompt()
	a.beginTurn()
}

// handleSettled ends the turn: agent_settled comes once per prompt, after
// retries, queued messages and aborts.
func (a *Adapter) handleSettled() {
	a.setRunActive(false)
	a.endTurn()
}

// cancelTurnDialogs cancels the dialogs of the turn that are still open,
// so an extension doesn't wait for an answer that can't come any more.
func (a *Adapter) cancelTurnDialogs() {
	ids := a.loop.turnDialogs
	a.loop.turnDialogs = nil
	var cancel []string
	a.mu.Lock()
	for _, id := range ids {
		if d := a.dialogs[id]; d != nil {
			d.stopTimer()
			delete(a.dialogs, id)
			cancel = append(cancel, id)
		}
	}
	a.mu.Unlock()
	for _, id := range cancel {
		a.loop.sink.RequestClosed(id)
		go func(id string) {
			if err := a.send(uiResponse{Type: "extension_ui_response", ID: id, Cancelled: true}); err != nil {
				a.logger.Debug("Failed to cancel a Pi dialog", "id", id, "error", err)
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
		return "result-" + msg.ToolCallID
	}
	return fmt.Sprintf("%s-%d", strings.ToLower(msg.Role), msg.Timestamp)
}
