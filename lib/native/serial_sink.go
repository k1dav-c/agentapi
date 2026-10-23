package native

import (
	"sync"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
)

// SerialSink returns a Sink that passes calls on to sink one at a time, as
// the Sink contract asks, for an adapter that calls its Sink from more than
// one goroutine (the demo: the turn it plays, and Start and Compact on the
// caller's). It is a SettingsSink and a UsageSink when sink is.
func SerialSink(sink Sink) Sink {
	return &serialSink{sink: sink}
}

type serialSink struct {
	mu   sync.Mutex
	sink Sink
}

var (
	_ SettingsSink = (*serialSink)(nil)
	_ UsageSink    = (*serialSink)(nil)
)

func (s *serialSink) do(f func(Sink)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s.sink)
}

func (s *serialSink) TurnStarted()                  { s.do(func(k Sink) { k.TurnStarted() }) }
func (s *serialSink) TurnCompleted(r TurnResult)    { s.do(func(k Sink) { k.TurnCompleted(r) }) }
func (s *serialSink) RequestOpened(r Request)       { s.do(func(k Sink) { k.RequestOpened(r) }) }
func (s *serialSink) RequestClosed(id string)       { s.do(func(k Sink) { k.RequestClosed(id) }) }
func (s *serialSink) SessionName(name string)       { s.do(func(k Sink) { k.SessionName(name) }) }
func (s *serialSink) SessionRef(ref string)         { s.do(func(k Sink) { k.SessionRef(ref) }) }
func (s *serialSink) LoginRequired(l LoginRequired) { s.do(func(k Sink) { k.LoginRequired(l) }) }

func (s *serialSink) RichMessage(m transcript.RichMessage) {
	s.do(func(k Sink) { k.RichMessage(m) })
}

func (s *serialSink) SessionEvents(events []transcript.SessionEvent) {
	s.do(func(k Sink) { k.SessionEvents(events) })
}

func (s *serialSink) Error(message string, level conv.ErrorLevel) {
	s.do(func(k Sink) { k.Error(message, level) })
}

func (s *serialSink) SettingsChanged(settings Settings) {
	s.do(func(k Sink) { ReportSettings(k, settings) })
}

func (s *serialSink) UsageChanged(usage AgentUsage) {
	s.do(func(k Sink) { ReportUsage(k, usage) })
}
