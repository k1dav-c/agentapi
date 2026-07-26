// Package mcpconfig reads and replaces MCP server configuration for agents
// that support configuration through files.
package mcpconfig

import (
	"encoding/json"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"golang.org/x/xerrors"
)

var ErrUnsupportedAgent = xerrors.New("MCP configuration is not supported for this agent type")

type Servers map[string]json.RawMessage

type Store interface {
	Path() string
	Read() (Servers, error)
	Replace(servers Servers) error
}

func NewStore(agentType mf.AgentType, workDir string) (Store, error) {
	switch agentType {
	case mf.AgentTypeClaude:
		return newClaudeStore(workDir)
	case mf.AgentTypeCodex:
		return newCodexStore()
	default:
		return nil, ErrUnsupportedAgent
	}
}

func SupportedAgent(agentType mf.AgentType) bool {
	return agentType == mf.AgentTypeClaude || agentType == mf.AgentTypeCodex
}
