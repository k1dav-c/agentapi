package procutil

import (
	"log/slog"
	"time"

	"github.com/coder/quartz"
)

// StopOptions are the timeouts of Stop.
type StopOptions struct {
	// StdinGrace is how long the process gets to exit on its own once its
	// stdin is closed; zero means 3s.
	StdinGrace time.Duration
	// TermGrace is how long it gets after SIGTERM before SIGKILL; zero
	// means 2s.
	TermGrace time.Duration
	// Clock times the graces; nil means the real clock.
	Clock quartz.Clock
	// Logger and Name (e.g. "Claude Code") report the escalation.
	Logger *slog.Logger
	Name   string
}

// Stop ends p the polite way: closing its stdin lets the agent finish and
// exit on its own; if it hasn't after StdinGrace it gets SIGTERM, and
// TermGrace later SIGKILL. exited must be closed once p has exited. Stop
// doesn't wait after SIGKILL.
//
// Don't hold the lock that writes to stdin while calling Stop: closing
// stdin is what makes a blocked write fail.
func Stop(p Process, exited <-chan struct{}, o StopOptions) {
	if o.StdinGrace == 0 {
		o.StdinGrace = 3 * time.Second
	}
	if o.TermGrace == 0 {
		o.TermGrace = 2 * time.Second
	}
	if o.Clock == nil {
		o.Clock = quartz.NewReal()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	_ = p.Stdin().Close()
	if waitExit(exited, o.StdinGrace, o.Clock) {
		return
	}
	o.Logger.Info(o.Name + " did not exit after its input closed; terminating it")
	_ = p.Terminate()
	if waitExit(exited, o.TermGrace, o.Clock) {
		return
	}
	o.Logger.Warn(o.Name + " did not exit after SIGTERM; killing it")
	_ = p.Kill()
}

func waitExit(exited <-chan struct{}, d time.Duration, clock quartz.Clock) bool {
	timer := clock.NewTimer(d, "procutil", "stop")
	defer timer.Stop()
	select {
	case <-exited:
		return true
	case <-timer.C:
		return false
	}
}
