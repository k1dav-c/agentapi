// Package nativetest provides a scriptable native.Adapter for tests.
package nativetest

import (
	"context"
	"errors"
	"sync"

	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// ErrClosed is returned by a Start that StartBlock holds when the adapter is
// closed.
var ErrClosed = errors.New("nativetest: adapter closed")

// Response is a recorded Respond call.
type Response struct {
	RequestID string
	Response  native.Response
}

// Adapter records the calls it receives. Tests play the agent's side
// through Sink.
type Adapter struct {
	Caps native.Capabilities
	// Ref is reported with Sink.SessionRef when the adapter starts.
	Ref string
	// ResumeRef is the ref the factory created the adapter with.
	ResumeRef string
	StartErr  error
	// StartWarnings are reported with Sink.Error at the start, before
	// StartErr is returned.
	StartWarnings []string
	// StartBlock, if set, holds Start until it is closed, the adapter is
	// closed (Start then returns ErrClosed) or Start's ctx ends.
	StartBlock chan struct{}
	TurnErr    error
	// SteerErr is returned by Steer.
	SteerErr error
	// RespondErr is returned by Respond.
	RespondErr error
	// AutoStartTurn reports TurnStarted when a turn is sent.
	AutoStartTurn bool
	// OnStartTurn, OnSteer and OnRespond, if set, run first thing in the
	// method of the same name, so a test can play the agent's side while
	// the call is on its way.
	OnStartTurn func(ctx context.Context)
	OnSteer     func(ctx context.Context)
	OnRespond   func(ctx context.Context)

	mu         sync.Mutex
	sink       native.Sink
	turns      []native.Input
	steers     []native.Input
	interrupts int
	responses  []Response
	done       chan error
	once       sync.Once
	closed     bool
	closing    chan struct{}
}

var _ native.Adapter = (*Adapter)(nil)

// New returns an adapter that supports everything.
func New() *Adapter {
	return &Adapter{
		Caps:          native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true},
		AutoStartTurn: true,
		done:          make(chan error, 1),
		closing:       make(chan struct{}),
	}
}

func (a *Adapter) Start(ctx context.Context, sink native.Sink) error {
	a.mu.Lock()
	a.sink = sink
	a.mu.Unlock()
	if a.StartBlock != nil {
		select {
		case <-a.StartBlock:
		case <-a.closing:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, warning := range a.StartWarnings {
		sink.Error(warning, st.ErrorLevelWarning)
	}
	if a.StartErr != nil {
		return a.StartErr
	}
	if a.Ref != "" {
		sink.SessionRef(a.Ref)
	}
	return nil
}

// Sink returns the sink the conversation passed to Start.
func (a *Adapter) Sink() native.Sink {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sink
}

func (a *Adapter) StartTurn(ctx context.Context, input native.Input) error {
	if a.OnStartTurn != nil {
		a.OnStartTurn(ctx)
	}
	if a.TurnErr != nil {
		return a.TurnErr
	}
	a.mu.Lock()
	a.turns = append(a.turns, input)
	sink := a.sink
	a.mu.Unlock()
	if a.AutoStartTurn {
		sink.TurnStarted()
	}
	return nil
}

func (a *Adapter) Steer(ctx context.Context, input native.Input) error {
	if a.OnSteer != nil {
		a.OnSteer(ctx)
	}
	if a.SteerErr != nil {
		return a.SteerErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steers = append(a.steers, input)
	return nil
}

func (a *Adapter) Interrupt(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.interrupts++
	return nil
}

func (a *Adapter) Respond(ctx context.Context, requestID string, response native.Response) error {
	if a.OnRespond != nil {
		a.OnRespond(ctx)
	}
	if a.RespondErr != nil {
		return a.RespondErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.responses = append(a.responses, Response{RequestID: requestID, Response: response})
	return nil
}

func (a *Adapter) Capabilities() native.Capabilities { return a.Caps }

func (a *Adapter) Wait() error { return <-a.done }

func (a *Adapter) Close() error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		close(a.closing)
	}
	a.mu.Unlock()
	a.Exit(nil)
	return nil
}

// Exit simulates the agent process exiting with err.
func (a *Adapter) Exit(err error) {
	a.once.Do(func() { a.done <- err })
}

// Closed reports whether Close was called.
func (a *Adapter) Closed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// Turns returns the inputs sent as turns.
func (a *Adapter) Turns() []native.Input {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]native.Input(nil), a.turns...)
}

// Steers returns the inputs sent with Steer.
func (a *Adapter) Steers() []native.Input {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]native.Input(nil), a.steers...)
}

// Interrupts returns how often Interrupt was called.
func (a *Adapter) Interrupts() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interrupts
}

// Responses returns the recorded Respond calls.
func (a *Adapter) Responses() []Response {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Response(nil), a.responses...)
}

// Factory creates adapters with New (its method New is a native.Factory)
// and records them, newest last.
type Factory struct {
	mu       sync.Mutex
	adapters []*Adapter
	// Configure, if set, adjusts each new adapter.
	Configure func(*Adapter)
}

// New implements native.Factory.
func (f *Factory) New(resumeRef string) (native.Adapter, error) {
	a := New()
	a.ResumeRef = resumeRef
	if f.Configure != nil {
		f.Configure(a)
	}
	f.mu.Lock()
	f.adapters = append(f.adapters, a)
	f.mu.Unlock()
	return a, nil
}

// Adapters returns the adapters created so far.
func (f *Factory) Adapters() []*Adapter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*Adapter(nil), f.adapters...)
}

// Last returns the newest adapter, or nil.
func (f *Factory) Last() *Adapter {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.adapters) == 0 {
		return nil
	}
	return f.adapters[len(f.adapters)-1]
}
