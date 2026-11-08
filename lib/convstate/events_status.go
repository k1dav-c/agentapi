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
	newLifecycle := LifecycleRunning
	if newStatus == conv.StatusInitializing {
		newLifecycle = LifecycleStarting
	} else if newStatus == conv.StatusStable {
		newLifecycle = LifecycleReady
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
	if newLifecycle == LifecycleRunning && e.lifecycle == LifecycleReady {
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
func (e *EventEmitter) SetLifecycle(lifecycle LifecycleState) {
	e.mu.Lock()
	defer e.unlock()
	e.lifecycleHeld = lifecycle == LifecycleRestarting || lifecycle == LifecycleExited || lifecycle == LifecycleFailed
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

// SetAgentSessionID records the agent's own session, which a restart or a
// resume changes.
func (e *EventEmitter) SetAgentSessionID(id string) {
	e.mu.Lock()
	defer e.unlock()
	if e.agentSessionID == id {
		return
	}
	e.agentSessionID = id
	e.notifyStatusLocked()
}

// SetCapabilities records the optional operations the agent supports.
func (e *EventEmitter) SetCapabilities(caps agentstate.Capabilities) {
	e.mu.Lock()
	defer e.unlock()
	if e.capabilities != nil && *e.capabilities == caps {
		return
	}
	e.capabilities = &caps
	e.notifyStatusLocked()
}

// SetSettings records the agent's settings.
func (e *EventEmitter) SetSettings(settings agentstate.Settings) {
	e.mu.Lock()
	defer e.unlock()
	if e.settings != nil && *e.settings == settings {
		return
	}
	e.settings = &settings
	e.notifyStatusLocked()
}

// SetUsage records the agent's usage of its limits.
func (e *EventEmitter) SetUsage(usage agentstate.AgentUsage) {
	e.mu.Lock()
	defer e.unlock()
	if e.usage != nil && reflect.DeepEqual(*e.usage, usage) {
		return
	}
	e.usage = &usage
	e.notifyStatusLocked()
}

// SetLimitStop records the turn a usage limit stopped (nil: none).
func (e *EventEmitter) SetLimitStop(ls *agentstate.LimitStop) {
	e.mu.Lock()
	defer e.unlock()
	if reflect.DeepEqual(e.limitStop, ls) {
		return
	}
	e.limitStop = ls
	e.notifyStatusLocked()
}

// SetRetryWait records the failed turn that waits to be tried again (nil:
// none).
func (e *EventEmitter) SetRetryWait(rw *agentstate.RetryWait) {
	e.mu.Lock()
	defer e.unlock()
	if reflect.DeepEqual(e.retryWait, rw) {
		return
	}
	e.retryWait = rw
	e.notifyStatusLocked()
}

// SetLoginRequired records that the agent waits for a login (nil: it no
// longer does).
func (e *EventEmitter) SetLoginRequired(login *agentstate.LoginRequired) {
	e.mu.Lock()
	defer e.unlock()
	if login == nil && e.loginRequired == nil {
		return
	}
	if login != nil {
		copied := *login
		login = &copied
	}
	e.loginRequired = login
	e.handoffRevision++
	e.notifyStatusLocked()
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
