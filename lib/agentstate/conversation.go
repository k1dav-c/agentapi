package agentstate

import "slices"

// ConversationState is the state of a native conversation beyond its
// messages and requests: what the agent supports, waits on and reports.
// lib/native reports it whole whenever a part changes (native.Observer);
// lib/convstate serves it in status_change events and stores Stored.
// A new piece of state is a field here (or in StoredState, if it survives
// an AgentAPI restart).
type ConversationState struct {
	// Capabilities is nil until an agent started; while no agent runs it is
	// what the last one supported.
	Capabilities *Capabilities
	// LoginRequired is set while the agent waits for the user to log in.
	LoginRequired *LoginRequired
	// Settings are the agent's session settings, zero until it reports
	// them; kept while no agent runs.
	Settings Settings
	// Usage is nil until the agent reports its usage of its limits.
	Usage *AgentUsage
	// Stored is the part a restarted AgentAPI restores.
	Stored StoredState
}

// StoredState is the part of a conversation's state that survives an
// AgentAPI restart: the state database keeps it (as JSON, versioned) and
// gives it back to the conversation (native.Config.State).
type StoredState struct {
	// Settings were chosen for the session (PUT /settings, or followed
	// from the agent): every agent that starts gets them.
	Settings Settings `json:"settings"`
	// LimitStop is set while a turn a usage limit stopped holds the queue.
	LimitStop *LimitStop `json:"limit_stop,omitempty"`
	// RetryWait is set while a failed turn waits to be tried again, which
	// holds the queue.
	RetryWait *RetryWait `json:"retry_wait,omitempty"`
}

// Clone returns a copy of s that shares nothing that can be changed.
func (s ConversationState) Clone() ConversationState {
	s.Capabilities = clonePtr(s.Capabilities)
	s.LoginRequired = clonePtr(s.LoginRequired)
	if s.Usage != nil {
		usage := *s.Usage
		usage.Context = clonePtr(usage.Context)
		usage.PromptCache = clonePtr(usage.PromptCache)
		usage.RateLimits = slices.Clone(usage.RateLimits)
		s.Usage = &usage
	}
	s.Stored = s.Stored.Clone()
	return s
}

// Clone returns a copy of s that shares nothing that can be changed.
func (s StoredState) Clone() StoredState {
	if s.LimitStop != nil {
		ls := *s.LimitStop
		ls.ResetsAt = clonePtr(ls.ResetsAt)
		ls.ContinueAt = clonePtr(ls.ContinueAt)
		s.LimitStop = &ls
	}
	s.RetryWait = clonePtr(s.RetryWait)
	return s
}

// clonePtr returns a copy of *p, or nil for nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	copied := *p
	return &copied
}
