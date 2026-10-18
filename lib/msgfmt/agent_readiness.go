package msgfmt

import "strings"

// IsAgentReadyForInitialPrompt reports whether the screen shows the agent
// ready for the initial prompt: by default, when its input box shows. An
// agent type msgfmt doesn't know is always ready.
func IsAgentReadyForInitialPrompt(agentType AgentType, message string) bool {
	p, known := profileOf(agentType)
	return !known || p.ready(message)
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
