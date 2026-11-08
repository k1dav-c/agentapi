// Package agentstate holds the values that describe a native agent's state
// and what it waits on: its capabilities, login, settings, usage, requests,
// and the usage limit stop or retry wait that holds its queue. lib/native
// produces them; lib/convstate, lib/instanceapi, lib/webhook and the HTTP
// API serve them, without depending on lib/native.
package agentstate

// LoginRequired tells the user how to log the agent in. Native mode has no
// terminal, so the login happens in one the user opens themselves.
type LoginRequired struct {
	Agent   string `json:"agent" doc:"Agent that needs a login, e.g. Claude Code."`
	Message string `json:"message" doc:"What is wrong, e.g. Claude Code is not logged in."`
	Command string `json:"command" doc:"Command to run in a terminal to log in, e.g. claude auth login."`
	Detail  string `json:"detail,omitempty" doc:"More help, or the agent's own error message."`
	Host    string `json:"host,omitempty" doc:"Machine to run the command on: the one AgentAPI runs on."`
	User    string `json:"user,omitempty" doc:"User to run the command as: the one AgentAPI runs as."`
}

// Capabilities says which optional operations an adapter supports. Callers
// decide what to offer from these, never from the agent type.
type Capabilities struct {
	Steer     bool `json:"steer" doc:"A message can be added to the turn the agent is working on."`
	Interrupt bool `json:"interrupt" doc:"The turn the agent is working on can be stopped."`
	Resume    bool `json:"resume" doc:"The agent's session can be resumed after AgentAPI restarts."`
	Images    bool `json:"images" doc:"Messages can carry images."`
	// Settings: the session settings (permission mode, model, reasoning
	// effort) can change while the agent runs; see native.Configurable.
	Settings bool `json:"settings" doc:"The permission mode, model and reasoning effort can be changed while the agent runs (GET and PUT /settings)."`
	// Compact: the agent compacts its context on request; see
	// native.Compacter.
	Compact bool `json:"compact" doc:"The agent compacts its context on request (POST /compact). Claude Code compacts with the /compact message instead."`
}
