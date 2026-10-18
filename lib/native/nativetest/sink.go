package nativetest

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// Sink records what an adapter reports, for adapter tests. It fails the
// test when two calls overlap, which the Sink contract forbids.
type Sink struct {
	t    testing.TB
	busy atomic.Bool

	mu    sync.Mutex
	state SinkState
}

// SinkState is what a Sink recorded.
type SinkState struct {
	// Log has the calls whose order tests check: "turn_started",
	// "turn_completed:<stop reason>", "request_opened:<id>",
	// "request_closed:<id>", "login_required" and "settings:<mode>".
	Log []string
	// Rich has every snapshot of every rich message, in order.
	Rich     []jsonlwatcher.RichMessage
	Events   []jsonlwatcher.SessionEvent
	Started  int
	Results  []native.TurnResult
	Requests []native.Request
	Closed   []string
	Names    []string
	Refs     []string
	// Errors are "<level>: <message>".
	Errors   []string
	Logins   []native.LoginRequired
	Settings []native.Settings
	Usage    []native.AgentUsage
}

var (
	_ native.Sink         = (*Sink)(nil)
	_ native.SettingsSink = (*Sink)(nil)
	_ native.UsageSink    = (*Sink)(nil)
)

// NewSink returns a Sink that reports overlapping calls to t.
func NewSink(t testing.TB) *Sink {
	return &Sink{t: t}
}

// enter takes the sink for one call, failing the test if another is under
// way.
func (s *Sink) enter() func() {
	if !s.busy.CompareAndSwap(false, true) {
		s.t.Error("sink called concurrently")
	}
	s.mu.Lock()
	return func() {
		s.mu.Unlock()
		s.busy.Store(false)
	}
}

func (s *Sink) TurnStarted() {
	defer s.enter()()
	s.state.Started++
	s.state.Log = append(s.state.Log, "turn_started")
}

func (s *Sink) TurnCompleted(r native.TurnResult) {
	defer s.enter()()
	s.state.Results = append(s.state.Results, r)
	s.state.Log = append(s.state.Log, "turn_completed:"+r.StopReason)
}

func (s *Sink) RichMessage(m jsonlwatcher.RichMessage) {
	defer s.enter()()
	s.state.Rich = append(s.state.Rich, m)
}

func (s *Sink) SessionEvents(events []jsonlwatcher.SessionEvent) {
	defer s.enter()()
	s.state.Events = append(s.state.Events, events...)
}

func (s *Sink) RequestOpened(r native.Request) {
	defer s.enter()()
	s.state.Requests = append(s.state.Requests, r)
	s.state.Log = append(s.state.Log, "request_opened:"+r.ID)
}

func (s *Sink) RequestClosed(id string) {
	defer s.enter()()
	s.state.Closed = append(s.state.Closed, id)
	s.state.Log = append(s.state.Log, "request_closed:"+id)
}

func (s *Sink) SessionName(name string) {
	defer s.enter()()
	s.state.Names = append(s.state.Names, name)
}

func (s *Sink) SessionRef(ref string) {
	defer s.enter()()
	s.state.Refs = append(s.state.Refs, ref)
}

func (s *Sink) LoginRequired(l native.LoginRequired) {
	defer s.enter()()
	s.state.Logins = append(s.state.Logins, l)
	s.state.Log = append(s.state.Log, "login_required")
}

func (s *Sink) SettingsChanged(settings native.Settings) {
	defer s.enter()()
	s.state.Settings = append(s.state.Settings, settings)
	s.state.Log = append(s.state.Log, "settings:"+string(settings.PermissionMode))
}

func (s *Sink) UsageChanged(usage native.AgentUsage) {
	defer s.enter()()
	s.state.Usage = append(s.state.Usage, usage)
}

func (s *Sink) Error(message string, level st.ErrorLevel) {
	defer s.enter()()
	s.state.Errors = append(s.state.Errors, string(level)+": "+message)
}

// State returns a copy of what the sink recorded.
func (s *Sink) State() SinkState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SinkState{
		Log:      slices.Clone(s.state.Log),
		Rich:     slices.Clone(s.state.Rich),
		Events:   slices.Clone(s.state.Events),
		Started:  s.state.Started,
		Results:  slices.Clone(s.state.Results),
		Requests: slices.Clone(s.state.Requests),
		Closed:   slices.Clone(s.state.Closed),
		Names:    slices.Clone(s.state.Names),
		Refs:     slices.Clone(s.state.Refs),
		Errors:   slices.Clone(s.state.Errors),
		Logins:   slices.Clone(s.state.Logins),
		Settings: slices.Clone(s.state.Settings),
		Usage:    slices.Clone(s.state.Usage),
	}
}

// Final returns the last snapshot of every rich message (by role and
// ID), in the order they first appeared.
func (s *Sink) Final() []jsonlwatcher.RichMessage {
	return Final(s.State().Rich)
}

// Final returns the last snapshot of every rich message in rich (by role
// and ID), in the order they first appeared.
func Final(rich []jsonlwatcher.RichMessage) []jsonlwatcher.RichMessage {
	var out []jsonlwatcher.RichMessage
	index := map[[2]string]int{}
	for _, m := range rich {
		key := [2]string{m.Role, m.MessageID}
		if i, ok := index[key]; ok {
			out[i] = m
			continue
		}
		index[key] = len(out)
		out = append(out, m)
	}
	return out
}

// Message returns the last snapshot of the rich message with id and role.
func (s *Sink) Message(id, role string) (jsonlwatcher.RichMessage, bool) {
	for _, m := range slices.Backward(s.State().Rich) {
		if m.MessageID == id && m.Role == role {
			return m, true
		}
	}
	return jsonlwatcher.RichMessage{}, false
}

// Snapshots returns every snapshot of the rich messages with id, of any
// role.
func (s *Sink) Snapshots(id string) []jsonlwatcher.RichMessage {
	var out []jsonlwatcher.RichMessage
	for _, m := range s.State().Rich {
		if m.MessageID == id {
			out = append(out, m)
		}
	}
	return out
}

// Request returns the request opened with id.
func (s *Sink) Request(id string) (native.Request, bool) {
	for _, r := range s.State().Requests {
		if r.ID == id {
			return r, true
		}
	}
	return native.Request{}, false
}

// TurnLog returns the turn entries of the log.
func (s *Sink) TurnLog() []string {
	var out []string
	for _, entry := range s.State().Log {
		if strings.HasPrefix(entry, "turn_") {
			out = append(out, entry)
		}
	}
	return out
}

// EventsOf returns the session events of kind.
func (state SinkState) EventsOf(kind string) []jsonlwatcher.SessionEvent {
	var out []jsonlwatcher.SessionEvent
	for _, e := range state.Events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}
