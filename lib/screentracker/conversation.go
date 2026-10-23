package screentracker

import (
	"strings"

	conv "github.com/coder/agentapi/lib/conversation"
)

// AgentIO is the agent's terminal: what is typed into it, and what it
// shows.
type AgentIO interface {
	Write(data []byte) (int, error)
	ReadScreen() string
}

// TerminalPart is a message part that types itself into the agent's
// terminal, such as a key press. Other parts are typed as their text, a
// hidden text part included.
type TerminalPart interface {
	conv.MessagePart
	Do(writer AgentIO) error
}

// typePart types part into the agent's terminal.
func typePart(writer AgentIO, part conv.MessagePart) error {
	switch p := part.(type) {
	case TerminalPart:
		return p.Do(writer)
	case conv.MessagePartText:
		_, err := writer.Write([]byte(p.Content))
		return err
	default:
		_, err := writer.Write([]byte(part.String()))
		return err
	}
}

func buildStringFromMessageParts(parts []conv.MessagePart) string {
	var sb strings.Builder
	for _, part := range parts {
		sb.WriteString(part.String())
	}
	return sb.String()
}
