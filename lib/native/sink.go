package native

import (
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/coder/agentapi/lib/transcript"
)

// sink is the Sink handed to the adapter of one generation.
type sink struct {
	c   *Conversation
	gen int
}

var (
	_ Sink         = (*sink)(nil)
	_ SettingsSink = (*sink)(nil)
	_ UsageSink    = (*sink)(nil)
)

// update runs f with the conversation locked, unless the sink's adapter was
// replaced.
func (s *sink) update(f func(c *Conversation)) {
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.gen == s.c.generation {
		f(s.c)
	}
}

func (s *sink) TurnStarted() {
	s.update(func(c *Conversation) {
		c.turnActive = true
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	})
}

func (s *sink) TurnCompleted(result TurnResult) {
	s.update(func(c *Conversation) {
		c.turnActive = false
		c.messages.endAgentMessage()
		c.expireRequestsLocked(false)
		if result.Error != "" {
			c.cfg.Emitter.EmitError(result.Error, st.ErrorLevelError)
		}
		if c.login != nil && provesLogin(result) {
			c.login = nil
			c.cfg.Observer.LoginRequired(nil)
		}
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	})
}

// provesLogin reports whether a turn that ended with result shows that the
// agent is logged in: the model answered. An interrupted turn may not have
// got that far, and a message the agent handled itself didn't need it.
func provesLogin(result TurnResult) bool {
	if result.Error != "" {
		return false
	}
	switch result.StopReason {
	case StopReasonEndTurn, StopReasonMaxTokens:
		return true
	}
	return false
}

func (s *sink) RichMessage(msg transcript.RichMessage) {
	// The adapter may change its message once the call returns; the
	// observer keeps (and changes) this copy.
	msg = msg.Clone()
	s.update(func(c *Conversation) {
		c.cfg.Observer.RichMessage(msg)
		if msg.Role == "assistant" && c.messages.addRich(msg) {
			c.cfg.Emitter.EmitMessages(c.messages.snapshot())
		}
	})
}

func (s *sink) SessionEvents(events []transcript.SessionEvent) {
	s.update(func(c *Conversation) { c.cfg.Observer.SessionEvents(events) })
}

func (s *sink) RequestOpened(req Request) {
	s.update(func(c *Conversation) {
		c.cfg.Observer.RequestUpdated(c.requests.open(req))
		c.cfg.Emitter.EmitStatus(c.statusLocked())
	})
}

func (s *sink) RequestClosed(id string) {
	s.update(func(c *Conversation) {
		if r := c.requests.find(id); r != nil {
			c.closeRequestLocked(r, RequestStatusWithdrawn, closedCapability(r.Kind))
			c.cfg.Emitter.EmitStatus(c.statusLocked())
		}
	})
}

func (s *sink) LoginRequired(login LoginRequired) {
	login = withLocalAccount(login)
	s.update(func(c *Conversation) {
		if c.setLoginLocked(login) {
			c.cfg.Emitter.EmitError(loginErrorText(login), st.ErrorLevelError)
		}
	})
}

func (s *sink) UsageChanged(usage AgentUsage) {
	s.update(func(c *Conversation) { c.setUsageLocked(usage) })
}

func (s *sink) SettingsChanged(settings Settings) {
	s.update(func(c *Conversation) { c.agentSettingsLocked(settings) })
}

func (s *sink) SessionName(name string) {
	s.update(func(c *Conversation) { c.cfg.Observer.SessionName(name) })
}

func (s *sink) SessionRef(ref string) {
	s.update(func(c *Conversation) {
		c.sessionRef = ref
		c.cfg.Observer.SessionRef(ref)
	})
}

func (s *sink) Error(message string, level st.ErrorLevel) {
	s.update(func(c *Conversation) {
		if !c.started {
			// While the agent waits for a login it is started again every
			// few seconds; what a start reports again was shown already.
			if c.loginWaiting && c.startErrors[message] {
				c.cfg.Logger.Debug("Agent message while it waits for a login", "message", message)
				return
			}
			if c.startErrors == nil {
				c.startErrors = make(map[string]bool)
			}
			c.startErrors[message] = true
		}
		c.cfg.Emitter.EmitError(message, level)
	})
}
