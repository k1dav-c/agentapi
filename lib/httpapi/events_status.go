package httpapi

import (
	"reflect"

	"github.com/coder/agentapi/internal/version"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
)

func (e *EventEmitter) EmitStatus(newStatus conv.Status) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newAgentStatus := convertStatus(newStatus)
	newLifecycle := LifecycleRunning
	if newStatus == conv.StatusInitializing {
		newLifecycle = LifecycleStarting
	} else if newStatus == conv.StatusStable {
		newLifecycle = LifecycleReady
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

func (e *EventEmitter) SetLifecycle(lifecycle LifecycleState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lifecycle == lifecycle {
		return
	}
	e.lifecycle = lifecycle
	e.handoffRevision++
	e.notifyStatusLocked()
}

// SetSessionName records the name the agent gave its session.
func (e *EventEmitter) SetSessionName(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	defer e.mu.Unlock()
	return e.statusChangeBody()
}

func (e *EventEmitter) statusChangeBody() StatusChangeBody {
	pending := 0
	for _, r := range e.requests {
		if r.Status == native.RequestStatusPending {
			pending++
		}
	}
	return StatusChangeBody{
		Status: e.status, Lifecycle: e.lifecycle, ServerID: e.serverID, Turn: e.turn,
		AgentSessionID: e.agentSessionID, AgentType: e.agentType, Transport: e.transport, Version: version.Version,
		TerminalPrompt: e.terminalPrompt, TerminalColumns: e.terminalColumns,
		SessionName: e.sessionName, PendingRequests: pending, Capabilities: e.capabilities,
		LoginRequired: e.loginRequired, Settings: e.settings, Usage: e.usage,
		LimitStop: e.limitStop,
		Terminal:  e.terminal,
	}
}

// SetAgentSessionID records the agent's own session, which a restart or a
// resume changes.
func (e *EventEmitter) SetAgentSessionID(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agentSessionID == id {
		return
	}
	e.agentSessionID = id
	e.notifyStatusLocked()
}

// SetCapabilities records the optional operations the agent supports.
func (e *EventEmitter) SetCapabilities(caps native.Capabilities) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.capabilities != nil && *e.capabilities == caps {
		return
	}
	e.capabilities = &caps
	e.notifyStatusLocked()
}

// SetSettings records the agent's settings.
func (e *EventEmitter) SetSettings(settings native.Settings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.settings != nil && *e.settings == settings {
		return
	}
	e.settings = &settings
	e.notifyStatusLocked()
}

// SetUsage records the agent's usage of its limits.
func (e *EventEmitter) SetUsage(usage native.AgentUsage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.usage != nil && reflect.DeepEqual(*e.usage, usage) {
		return
	}
	e.usage = &usage
	e.notifyStatusLocked()
}

// SetLimitStop records the turn a usage limit stopped (nil: none).
func (e *EventEmitter) SetLimitStop(ls *native.LimitStop) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if reflect.DeepEqual(e.limitStop, ls) {
		return
	}
	e.limitStop = ls
	e.notifyStatusLocked()
}

// SetLoginRequired records that the agent waits for a login (nil: it no
// longer does).
func (e *EventEmitter) SetLoginRequired(login *native.LoginRequired) {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	defer e.mu.Unlock()

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
