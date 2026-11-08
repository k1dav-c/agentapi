package convstate

import (
	"reflect"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/instanceapi"
)

// EmitStatus records the conversation's status. Its lifecycle (starting,
// running, ready) doesn't replace one the server set and holds (see
// SetLifecycle): the old agent may still report while it restarts.
func (e *EventEmitter) EmitStatus(newStatus conv.Status) {
	e.mu.Lock()
	defer e.unlock()

	newAgentStatus := ConvertStatus(newStatus)
	newLifecycle := instanceapi.LifecycleRunning
	if newStatus == conv.StatusInitializing {
		newLifecycle = instanceapi.LifecycleStarting
	} else if newStatus == conv.StatusStable {
		newLifecycle = instanceapi.LifecycleReady
	}
	e.reported = newLifecycle
	if e.lifecycleHeld {
		newLifecycle = e.lifecycle
	}
	if e.status == newAgentStatus && e.lifecycle == newLifecycle {
		return
	}

	previousStatus := e.status
	e.handoffRevision++
	if newLifecycle == instanceapi.LifecycleRunning && e.lifecycle == instanceapi.LifecycleReady {
		e.turn++
	}
	e.status = newAgentStatus
	e.lifecycle = newLifecycle
	e.notifyStatusLocked()
	if e.onStatusChange != nil && previousStatus != newAgentStatus {
		e.onStatusChange(previousStatus, newAgentStatus)
	}
}

// SetLifecycle sets the lifecycle. Restarting, exited and failed are held
// until ReleaseLifecycle, or another SetLifecycle: the conversation's
// status doesn't replace them.
func (e *EventEmitter) SetLifecycle(lifecycle instanceapi.LifecycleState) {
	e.mu.Lock()
	defer e.unlock()
	e.lifecycleHeld = lifecycle == instanceapi.LifecycleRestarting || lifecycle == instanceapi.LifecycleExited || lifecycle == instanceapi.LifecycleFailed
	if e.lifecycle == lifecycle {
		return
	}
	e.lifecycle = lifecycle
	e.handoffRevision++
	e.notifyStatusLocked()
}

// ReleaseLifecycle ends the hold of a lifecycle SetLifecycle set: the
// lifecycle is the conversation's again, as its last status gives it. It
// is called once the agent is back: a restart finished, or a native agent
// the conversation started again accepts turns.
func (e *EventEmitter) ReleaseLifecycle() {
	e.mu.Lock()
	defer e.unlock()
	if !e.lifecycleHeld {
		return
	}
	e.lifecycleHeld = false
	if e.lifecycle == e.reported {
		return
	}
	e.lifecycle = e.reported
	e.handoffRevision++
	e.notifyStatusLocked()
}

// SetSessionName records the name the agent gave its session.
func (e *EventEmitter) SetSessionName(name string) {
	e.mu.Lock()
	defer e.unlock()
	if e.sessionName == name {
		return
	}
	e.sessionName = name
	if e.persister != nil {
		e.persister.SetSessionName(name)
	}
	e.notifyStatusLocked()
}

func (e *EventEmitter) StatusSnapshot() StatusChangeBody {
	e.mu.Lock()
	defer e.unlock()
	return e.statusChangeBody()
}

func (e *EventEmitter) statusChangeBody() StatusChangeBody {
	pending := 0
	for _, r := range e.requests {
		if r.Status == agentstate.RequestStatusPending {
			pending++
		}
	}
	return StatusChangeBody{
		Status: e.status, Lifecycle: e.lifecycle, ServerID: e.serverID, Turn: e.turn,
		AgentSessionID: e.agentSessionID, AgentType: e.agentType, Transport: instanceapi.TransportNative, Version: version.Version,
		SessionName: e.sessionName, PendingRequests: pending, Capabilities: e.capabilities,
		LoginRequired: e.loginRequired, Settings: e.settings, Usage: e.usage,
		LimitStop: e.limitStop, RetryWait: e.retryWait,
		Terminal: e.terminal,
	}
}

// SetSessionRef records the session the agent resumes, which a restart or
// a switch changes: it is stored, every time, to resume the session after
// AgentAPI restarts, and shown as the agent's session ID. The session ID
// clients use is AgentAPI's own.
func (e *EventEmitter) SetSessionRef(ref string) {
	e.mu.Lock()
	defer e.unlock()
	if e.persister != nil {
		e.persister.SetSessionRef(ref)
	}
	if e.agentSessionID == ref {
		return
	}
	e.agentSessionID = ref
	e.notifyStatusLocked()
}

// SetState records the conversation's state, as lib/native reports it
// (native.Observer.StateChanged). What changed of it in status updates is
// sent as one, and its stored part is stored if that changed.
func (e *EventEmitter) SetState(state agentstate.ConversationState) {
	e.mu.Lock()
	defer e.unlock()
	state = state.Clone()
	previous := e.agent
	e.agent = state
	if !reflect.DeepEqual(e.stored, state.Stored) {
		e.stored = state.Stored
		if e.persister != nil {
			e.persister.SetStoredState(state.Stored)
		}
	}
	changed := false
	// Status updates carry a part once the agent reported it; the
	// capabilities and the usage stay while no agent runs.
	if state.Capabilities != nil && !reflect.DeepEqual(e.capabilities, state.Capabilities) {
		e.capabilities = state.Capabilities
		changed = true
	}
	if !reflect.DeepEqual(e.loginRequired, state.LoginRequired) {
		e.loginRequired = state.LoginRequired
		e.handoffRevision++
		changed = true
	}
	if state.Settings != previous.Settings {
		settings := state.Settings
		e.settings = &settings
		changed = true
	}
	if state.Usage != nil && !reflect.DeepEqual(e.usage, state.Usage) {
		e.usage = state.Usage
		changed = true
	}
	if !reflect.DeepEqual(e.limitStop, state.Stored.LimitStop) {
		e.limitStop = state.Stored.LimitStop
		changed = true
	}
	if !reflect.DeepEqual(e.retryWait, state.Stored.RetryWait) {
		e.retryWait = state.Stored.RetryWait
		changed = true
	}
	if changed {
		e.notifyStatusLocked()
	}
}

func (e *EventEmitter) EmitError(message string, level conv.ErrorLevel) {
	e.mu.Lock()
	defer e.unlock()

	e.errorCount++
	errorBody := ErrorBody{
		Message: message,
		Level:   level,
		Time:    e.clock.Now(),
		number:  e.errorCount,
	}

	// Store the error so new subscribers can receive recent errors.
	e.errors = append(e.errors, errorBody)
	if len(e.errors) > maxStoredErrors {
		e.errors = e.errors[len(e.errors)-maxStoredErrors:]
	}

	e.notifyChannels(EventTypeError, errorBody)
	e.tasksChangedLocked(e.tasks.agentError(errorBody))
}
