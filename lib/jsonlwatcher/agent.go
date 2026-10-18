package jsonlwatcher

import "time"

// Agent is what following one agent's session log takes.
type Agent struct {
	// Resolver finds the session log.
	Resolver SessionResolver
	// Parser turns its lines into rich messages, and Events into timeline
	// events.
	Parser LineParser
	Events SessionEventParser
	// Namer follows the name the agent gives the session.
	Namer SessionNamer
}

// ForAgent returns how to follow the session log of an agent of agentType
// ("claude", "codex" or "pi") that runs as process pid in cwd and started
// at started, or false for an agent that keeps none.
func ForAgent(agentType string, pid int, cwd string, started time.Time) (Agent, bool) {
	switch agentType {
	case "claude":
		return Agent{
			Resolver: &ClaudeResolver{PID: pid},
			Parser:   NewClaudeParser(),
			Events:   NewClaudeSessionEventParser(),
			Namer:    NewSessionNamer(agentType),
		}, true
	case "codex":
		return Agent{
			Resolver: &CodexResolver{CWD: cwd, NotBefore: started},
			Parser:   NewCodexParser(),
			Events:   NewCodexSessionEventParser(),
			Namer:    NewSessionNamer(agentType),
		}, true
	case "pi":
		return Agent{
			Resolver: &PiResolver{PID: pid, CWD: cwd, NotBefore: started},
			Parser:   NewPiParser(),
			Events:   NewPiSessionEventParser(),
			Namer:    NewSessionNamer(agentType),
		}, true
	}
	return Agent{}, false
}
