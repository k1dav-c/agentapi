package msgfmt

import "strings"

func IsAgentReadyForInitialPrompt(agentType AgentType, message string) bool {
	switch agentType {
	case AgentTypeClaude:
		return isClaudeAgentReadyForInitialPrompt(message)
	case AgentTypeGoose:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeAider:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeCodex:
		return isCodexAgentReadyForInitialPrompt(message)
	case AgentTypeGemini:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeCopilot:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeAmp:
		return isAmpAgentReadyForInitialPrompt(message)
	case AgentTypeCursor:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeAuggie:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeAmazonQ:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeOpencode:
		return isOpencodeAgentReadyForInitialPrompt(message)
	case AgentTypeKimi:
		return isGenericAgentReadyForInitialPrompt(message)
	case AgentTypeCustom:
		return isGenericAgentReadyForInitialPrompt(message)
	default:
		return true
	}
}

func isGenericAgentReadyForInitialPrompt(message string) bool {
	message = trimEmptyLines(message)
	messageWithoutInputBox := removeMessageBox(message)
	return len(messageWithoutInputBox) != len(message)
}

func isOpencodeAgentReadyForInitialPrompt(message string) bool {
	message = trimEmptyLines(message)
	messageWithoutInputBox := removeOpencodeMessageBox(message)
	return len(messageWithoutInputBox) != len(message)
}

func isCodexAgentReadyForInitialPrompt(message string) bool {
	message = trimEmptyLines(message)
	messageWithoutInputBox := removeCodexMessageBox(message)
	return len(messageWithoutInputBox) != len(message)
}

func isAmpAgentReadyForInitialPrompt(message string) bool {
	message = trimEmptyLines(message)
	messageWithoutInputBox := removeAmpMessageBox(message)
	return len(messageWithoutInputBox) != len(message)
}

func isClaudeAgentReadyForInitialPrompt(message string) bool {
	top, _ := findClaudeInputBox(strings.Split(message, "\n"))
	return top != -1 || isGenericAgentReadyForInitialPrompt(message)
}
