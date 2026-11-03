package msgfmt

import (
	"strings"
)

// toolCallRange is the lines [start, end) of a tool call in a message. A
// malformed one, only partly on the screen, is removed but not returned.
type toolCallRange struct {
	start     int
	end       int
	malformed bool
}

// cutToolCalls removes the tool calls, in order and not overlapping, from
// lines, and returns what is left and the tool calls, last first.
func cutToolCalls(lines []string, calls []toolCallRange) ([]string, []string) {
	removed := make([]string, 0)
	for i := len(calls) - 1; i >= 0; i-- {
		start, end := calls[i].start, calls[i].end
		if !calls[i].malformed {
			removed = append(removed, strings.Join(lines[start:end], "\n"))
		}
		lines = append(lines[:start], lines[end:]...)
	}
	return lines, removed
}

func removeClaudeReportTaskToolCall(msg string) (string, []string) {
	msg = "\n" + msg // This handles the case where the message starts with a tool call

	// Remove all tool calls that start with `● coder - coder_report_task (MCP)`
	lines := strings.Split(msg, "\n")

	toolCallStartIdx := -1
	newLineAfterToolCall := -1

	var toolCallIdxs []toolCallRange

	for i := 1; i < len(lines)-1; i++ {
		prevLine := strings.TrimSpace(lines[i-1])
		line := strings.Trim(strings.TrimSpace(lines[i]), "\n")
		nextLine := strings.TrimSpace(lines[i+1])

		if strings.Contains(line, "coder - coder_report_task (MCP)") {
			toolCallStartIdx = i
		} else if toolCallStartIdx != -1 {
			if line == "\"message\": \"Thanks for reporting!\"" && nextLine == "}" && strings.HasSuffix(prevLine, "{") {
				// Store [start, end] pair
				toolCallIdxs = append(toolCallIdxs, toolCallRange{toolCallStartIdx, min(len(lines), i+2), false})

				// Reset to find the next tool call
				toolCallStartIdx = -1
				newLineAfterToolCall = -1
			} else if len(line) == 0 {
				newLineAfterToolCall = i + 1
			}
		}
	}

	// Handle the malformed/partially rendered tool_calls
	if toolCallStartIdx != -1 {
		if newLineAfterToolCall != -1 {
			toolCallIdxs = append(toolCallIdxs, toolCallRange{toolCallStartIdx, newLineAfterToolCall, true})
		} else {
			toolCallIdxs = append(toolCallIdxs, toolCallRange{toolCallStartIdx, len(lines), true})
		}
	}

	// If no tool calls found, return original message
	if len(toolCallIdxs) == 0 {
		return strings.TrimLeft(msg, "\n"), []string{}
	}

	lines, toolCallMessages := cutToolCalls(lines, toolCallIdxs)
	return strings.TrimLeft(strings.Join(lines, "\n"), "\n"), toolCallMessages
}

func removeCodexReportTaskToolCall(msg string) (string, []string) {
	lines := strings.Split(msg, "\n")

	toolCallStartIdx := -1

	var toolCallIdxs []toolCallRange

	for idx := 0; idx < len(lines); idx++ {
		line := strings.TrimSpace(lines[idx])

		// Check for tool call start (requires looking at next line)
		if idx+1 < len(lines) {
			nextLine := strings.TrimSpace(lines[idx+1])
			if strings.ReplaceAll(line, " ", "") == "•Called" && strings.Contains(nextLine, "Coder.coder_report_task") {
				toolCallStartIdx = idx
			}
		}

		// Check for tool call end
		if toolCallStartIdx != -1 && line == "{\"message\": \"Thanks for reporting!\"}" {
			// Find the end of trailing empty lines after tool call
			endIdx := idx + 1
			for endIdx < len(lines) && strings.TrimSpace(lines[endIdx]) == "" {
				endIdx++
			}
			toolCallIdxs = append(toolCallIdxs, toolCallRange{start: toolCallStartIdx, end: endIdx})

			// Reset to find the next tool call
			toolCallStartIdx = -1
		}
	}

	// If no tool calls found, return original message
	if len(toolCallIdxs) == 0 {
		return strings.TrimRight(msg, "\n"), []string{}
	}

	lines, toolCallMessages := cutToolCalls(lines, toolCallIdxs)
	return strings.TrimRight(strings.Join(lines, "\n"), "\n"), toolCallMessages
}

// FormatToolCall removes the coder MCP server's report_task tool calls
// from the agent's message, and returns them.
func FormatToolCall(agentType AgentType, message string) (string, []string) {
	p, _ := profileOf(agentType)
	return p.reportTaskCalls(message)
}
