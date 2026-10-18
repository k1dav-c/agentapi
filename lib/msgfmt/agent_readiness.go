package msgfmt

import "strings"

// IsAgentReadyForInitialPrompt reports whether the screen shows the agent
// ready for the initial prompt: by default, when its input box shows. An
// agent type msgfmt doesn't know is always ready.
func IsAgentReadyForInitialPrompt(agentType AgentType, message string) bool {
	p, known := profileOf(agentType)
	return !known || p.ready(message)
}

// ReadyWhenStable reports whether an agent of agentType may never show an
// input box, so that a screen that stopped changing counts as ready for the
// initial prompt too: a custom agent can be any terminal program.
func ReadyWhenStable(agentType AgentType) bool {
	p, _ := profileOf(agentType)
	return p.readyWhenStable
}

func isGenericAgentReadyForInitialPrompt(message string) bool {
	message = trimEmptyLines(message)
	messageWithoutInputBox := removeMessageBox(message)
	return len(messageWithoutInputBox) != len(message)
}

func isClaudeAgentReadyForInitialPrompt(message string) bool {
	top, _ := findClaudeInputBox(strings.Split(message, "\n"))
	return top != -1 || isGenericAgentReadyForInitialPrompt(message)
}
