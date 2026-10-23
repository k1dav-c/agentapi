// Package jsonlwatcher tails the session log of an agent that runs in a
// terminal (the PTY transport), for the rich messages and timeline events
// its records hold.
package jsonlwatcher

import (
	"time"

	"github.com/coder/agentapi/lib/agentlog"
	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/agentlog/codexlog"
	"github.com/coder/agentapi/lib/agentlog/pilog"
)

// Agent is what following one agent's session log takes.
type Agent struct {
	// Resolver finds the session log.
	Resolver agentlog.SessionResolver
	// Parser turns its lines into rich messages, and Events into timeline
	// events.
	Parser agentlog.LineParser
	Events agentlog.SessionEventParser
	// Namer follows the name the agent gives the session.
	Namer agentlog.SessionNamer
}

// ForAgent returns how to follow the session log of an agent of agentType
// ("claude", "codex" or "pi") that runs as process pid in cwd and started
// at started, or false for an agent that keeps none.
func ForAgent(agentType string, pid int, cwd string, started time.Time) (Agent, bool) {
	switch agentType {
	case "claude":
		return Agent{
			Resolver: &claudelog.Resolver{PID: pid},
			Parser:   claudelog.NewParser(),
			Events:   claudelog.NewEventParser(),
			Namer:    claudelog.NewNamer(),
		}, true
	case "codex":
		return Agent{
			Resolver: &codexlog.Resolver{CWD: cwd, NotBefore: started},
			Parser:   codexlog.NewParser(),
			Events:   codexlog.NewEventParser(),
			Namer:    codexlog.NewNamer(),
		}, true
	case "pi":
		return Agent{
			Resolver: &pilog.Resolver{PID: pid, CWD: cwd, NotBefore: started},
			Parser:   pilog.NewParser(),
			Events:   pilog.NewEventParser(),
			Namer:    pilog.NewNamer(),
		}, true
	}
	return Agent{}, false
}
