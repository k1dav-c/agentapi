package httpapi

import (
	"reflect"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

func (e *EventEmitter) EmitStatus(newStatus st.ConversationStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newAgentStatus := convertStatus(newStatus)
	newLifecycle := LifecycleRunning
	if newStatus == st.ConversationStatusInitializing {
		newLifecycle = LifecycleStarting
	} else if newStatus == st.ConversationStatusStable {
		newLifecycle = LifecycleReady
	}
	if e.status == newAgentStatus && e.lifecycle == newLifecycle {
		return
	}

	previousStatus := e.status
	e.handoffRevision++
	if newLifecycle == LifecycleRunning && e.lifecycle == LifecycleReady {
		e.runID++
	}
	e.status = newAgentStatus
	e.lifecycle = newLifecycle
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
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
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
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
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
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
		Status: e.status, Lifecycle: e.lifecycle, SessionID: e.sessionID,
		RunID: e.runID, AgentType: e.agentType, Transport: e.transport, Version: version.Version,
		TerminalPrompt: e.terminalPrompt, TerminalColumns: e.terminalColumns,
		SessionName: e.sessionName, PendingRequests: pending, Capabilities: e.capabilities,
		LoginRequired: e.loginRequired, Settings: e.settings, Usage: e.usage,
		Terminal: e.terminal,
	}
}

// SetCapabilities records the optional operations the agent supports.
func (e *EventEmitter) SetCapabilities(caps native.Capabilities) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.capabilities != nil && *e.capabilities == caps {
		return
	}
	e.capabilities = &caps
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetSettings records the agent's settings.
func (e *EventEmitter) SetSettings(settings native.Settings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.settings != nil && *e.settings == settings {
		return
	}
	e.settings = &settings
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

// SetUsage records the agent's usage of its limits.
func (e *EventEmitter) SetUsage(usage native.AgentUsage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.usage != nil && reflect.DeepEqual(*e.usage, usage) {
		return
	}
	e.usage = &usage
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
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
	e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
}

func (e *EventEmitter) EmitError(message string, level st.ErrorLevel) {
	e.mu.Lock()
	defer e.mu.Unlock()

	errorBody := ErrorBody{
		Message: message,
		Level:   level,
		Time:    e.clock.Now(),
	}

	// Store the error so new subscribers can receive recent errors.
	e.errors = append(e.errors, errorBody)
	if len(e.errors) > maxStoredErrors {
		e.errors = e.errors[len(e.errors)-maxStoredErrors:]
	}

	e.notifyChannels(EventTypeError, errorBody)
}
